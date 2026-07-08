package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// stubDoer returns queued responses/errors in order.
type stubDoer struct {
	calls    int
	statuses []int // per-call status code (ignored when errs[i] != nil)
	bodies   []string
	errs     []error
}

func (s *stubDoer) Do(req *http.Request) (*http.Response, error) {
	i := s.calls
	s.calls++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	status := http.StatusOK
	if i < len(s.statuses) {
		status = s.statuses[i]
	}
	body := ""
	if i < len(s.bodies) {
		body = s.bodies[i]
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}, nil
}

func buildReq(ctx context.Context) (*http.Request, error) {
	return http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/x", nil)
}

func TestRetryClient_RetriesNetworkErrorThenSucceeds(t *testing.T) {
	doer := &stubDoer{
		errs:     []error{errors.New("dial timeout"), nil},
		statuses: []int{0, http.StatusOK},
		bodies:   []string{"", "ok-body"},
	}
	c := newRetryClient(doer, 3)
	c.baseDelay = time.Millisecond

	body, err := c.doJSON(context.Background(), buildReq)
	if err != nil {
		t.Fatalf("doJSON: %v", err)
	}
	if string(body) != "ok-body" {
		t.Errorf("body = %q, want ok-body", body)
	}
	if doer.calls != 2 {
		t.Errorf("calls = %d, want 2", doer.calls)
	}
}

func TestRetryClient_FatalStatusStopsImmediately(t *testing.T) {
	doer := &stubDoer{statuses: []int{http.StatusBadRequest}, bodies: []string{"bad"}}
	c := newRetryClient(doer, 5)
	c.baseDelay = time.Millisecond

	_, err := c.doJSON(context.Background(), buildReq)
	if err == nil {
		t.Fatal("expected error")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want *StatusError, got %T", err)
	}
	if se.StatusCode != http.StatusBadRequest || se.Retryable {
		t.Errorf("StatusError = %+v", se)
	}
	if doer.calls != 1 {
		t.Errorf("fatal status retried: calls = %d, want 1", doer.calls)
	}
}

func TestRetryClient_ExhaustsRetries(t *testing.T) {
	doer := &stubDoer{
		statuses: []int{http.StatusTooManyRequests, http.StatusTooManyRequests, http.StatusTooManyRequests},
	}
	c := newRetryClient(doer, 2) // 1 initial + 2 retries = 3 attempts
	c.baseDelay = time.Millisecond

	_, err := c.doJSON(context.Background(), buildReq)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if doer.calls != 3 {
		t.Errorf("calls = %d, want 3", doer.calls)
	}
}

func TestRetryClient_RespectsContextCancel(t *testing.T) {
	doer := &stubDoer{statuses: []int{http.StatusTooManyRequests}}
	c := newRetryClient(doer, 5)
	c.baseDelay = time.Hour // force a long sleep so cancel wins the race

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before the first backoff sleep completes

	_, err := c.doJSON(ctx, buildReq)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestStatusError_Message(t *testing.T) {
	se := &StatusError{StatusCode: 500, Retryable: true, Body: "oops"}
	if got := se.Error(); got == "" || !bytes.Contains([]byte(got), []byte("500")) {
		t.Errorf("Error() = %q", got)
	}
}

// TestRetryClient_RedactsCredentialReflectedInErrorBody guards against a
// misbehaving or compromised upstream that echoes the caller's own credential
// back in an error response body (StatusError.Body is surfaced to CLI stderr,
// so a naive excerpt would leak it). The real Anthropic/OpenAI APIs are not
// known to do this — this is defense in depth, not a reaction to an observed
// leak — but it directly exercises the redaction path in attempt(), which the
// existing secret tests (fixed 403 bodies with no credential in them) do not.
func TestRetryClient_RedactsCredentialReflectedInErrorBody(t *testing.T) {
	const bearer = "sk-ant-admin-super-secret-token"
	const apiKey = "sk-raw-api-key-secret"

	buildReqWithAuth := func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/x", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("x-api-key", apiKey)
		return req, nil
	}

	doer := &stubDoer{
		statuses: []int{http.StatusForbidden},
		bodies: []string{
			fmt.Sprintf(`{"error":"forbidden for token %s and key %s"}`, bearer, apiKey),
		},
	}
	c := newRetryClient(doer, 0)

	_, err := c.doJSON(context.Background(), buildReqWithAuth)
	if err == nil {
		t.Fatal("expected error")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("want *StatusError, got %T", err)
	}
	if bytesContainsAny(se.Body, bearer, apiKey) {
		t.Errorf("StatusError.Body leaked a credential: %q", se.Body)
	}
	if bytesContainsAny(se.Error(), bearer, apiKey) {
		t.Errorf("StatusError.Error() leaked a credential: %q", se.Error())
	}
}

func bytesContainsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if bytes.Contains([]byte(s), []byte(sub)) {
			return true
		}
	}
	return false
}

func TestExcerpt(t *testing.T) {
	long := fmt.Sprintf("%0300d", 0) // 300 chars
	if got := excerpt([]byte(long), 256); len(got) != 256 {
		t.Errorf("excerpt len = %d, want 256", len(got))
	}
	if got := excerpt([]byte("short"), 256); got != "short" {
		t.Errorf("excerpt = %q", got)
	}
}
