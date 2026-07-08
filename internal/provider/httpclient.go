package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// StatusError is a typed HTTP error carrying the status code and whether the
// request may be retried. Adapters classify provider responses into this type so
// the retry loop can decide via errors.As, not string matching.
type StatusError struct {
	StatusCode int
	Retryable  bool
	// Body is a short, key-free excerpt of the response body for diagnostics.
	Body string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("http status %d (retryable=%t): %s", e.StatusCode, e.Retryable, e.Body)
}

// retryableStatus reports whether an HTTP status code should be retried.
// 429 (rate limit) and 5xx (server/overloaded) are retryable; 4xx (bad request,
// auth, forbidden, not found) are fatal.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// httpDoer is the minimal http.Client surface the retrying client needs; it lets
// tests inject a stub without a real network round-trip.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// retryClient performs HTTP requests with exponential backoff + jitter on
// retryable failures. It never logs the request (which may carry the admin key
// in a header) and honours ctx cancellation between attempts.
type retryClient struct {
	doer       httpDoer
	maxRetries int
	// baseDelay is the first backoff step; grows exponentially per attempt.
	baseDelay time.Duration
	// maxDelay caps a single backoff step so a large maxRetries can't sleep for
	// minutes on one attempt.
	maxDelay time.Duration
	// rng is the jitter source; seeded per client so concurrent providers don't
	// share global rand state (kept deterministic-injectable for tests).
	rng *rand.Rand
	// maxBodyBytes caps how much of a response body is read into memory to guard
	// against a hostile or misbehaving endpoint.
	maxBodyBytes int64
}

// newRetryClient builds a retryClient with sane defaults for a CLI: a bounded
// per-request timeout is expected to live on the injected http.Client.
func newRetryClient(doer httpDoer, maxRetries int) *retryClient {
	if maxRetries < 0 {
		maxRetries = 0
	}
	return &retryClient{
		doer:         doer,
		maxRetries:   maxRetries,
		baseDelay:    250 * time.Millisecond,
		maxDelay:     30 * time.Second,
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
		maxBodyBytes: 8 << 20, // 8 MiB
	}
}

// doJSON issues the request (built fresh per attempt by buildReq so the body can
// be replayed) and returns the successful response body bytes. On a fatal status
// it returns a *StatusError; on a retryable status or network error it retries up
// to maxRetries with exponential backoff + jitter before returning the last error.
//
// buildReq must set the URL, method, auth header and any body; it receives ctx so
// cancellation propagates into the request.
func (c *retryClient) doJSON(ctx context.Context, buildReq func(context.Context) (*http.Request, error)) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			if err := c.sleepBackoff(ctx, attempt); err != nil {
				return nil, err // ctx cancelled while waiting
			}
		}

		req, err := buildReq(ctx)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}

		body, err := c.attempt(req)
		if err == nil {
			return body, nil
		}
		lastErr = err

		// Fatal StatusError: stop immediately.
		var se *StatusError
		if errors.As(err, &se) && !se.Retryable {
			return nil, err
		}
		// ctx cancellation is not worth retrying.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// Otherwise (retryable status or transient network error) loop.
	}

	return nil, fmt.Errorf("exhausted %d retries: %w", c.maxRetries, lastErr)
}

// attempt performs a single HTTP round-trip and classifies the outcome.
func (c *retryClient) attempt(req *http.Request) ([]byte, error) {
	resp, err := c.doer.Do(req)
	if err != nil {
		// Network/transport errors are treated as retryable (the caller's loop
		// decides based on remaining attempts). Do not include req in the message
		// — its headers carry the admin key.
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, c.maxBodyBytes))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if readErr != nil {
			return nil, fmt.Errorf("read response body: %w", readErr)
		}
		return body, nil
	}

	return nil, &StatusError{
		StatusCode: resp.StatusCode,
		Retryable:  retryableStatus(resp.StatusCode),
		Body:       redactSecrets(excerpt(body, 256), req),
	}
}

// redactSecrets scrubs any occurrence of the credential(s) sent on req from an
// error-diagnostics string. The provider APIs we call never echo the caller's
// key back, but a misbehaving or compromised endpoint could — and Body ends up
// in CLI stderr, so this is defense in depth, not reliance on provider good
// behavior. Covers both auth schemes used by adapters in this package: bearer
// tokens (Authorization) and raw API-key headers (e.g. Anthropic's x-api-key).
func redactSecrets(s string, req *http.Request) string {
	if auth := req.Header.Get("Authorization"); auth != "" {
		s = strings.ReplaceAll(s, strings.TrimPrefix(auth, "Bearer "), "[REDACTED]")
		s = strings.ReplaceAll(s, auth, "[REDACTED]")
	}
	if key := req.Header.Get("x-api-key"); key != "" {
		s = strings.ReplaceAll(s, key, "[REDACTED]")
	}
	return s
}

// sleepBackoff waits an exponentially growing, jittered delay, aborting early if
// ctx is cancelled.
func (c *retryClient) sleepBackoff(ctx context.Context, attempt int) error {
	// attempt is 1-based here (0 is the first try, no sleep).
	exp := c.baseDelay * time.Duration(math.Pow(2, float64(attempt-1)))
	if exp > c.maxDelay || exp <= 0 {
		exp = c.maxDelay
	}
	// Full jitter: sleep a random duration in [0, exp].
	delay := time.Duration(c.rng.Int63n(int64(exp) + 1))

	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// excerpt returns at most n bytes of b as a string, for error diagnostics.
func excerpt(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}
