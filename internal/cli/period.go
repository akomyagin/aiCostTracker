package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

// parsePeriod turns a --period flag value into a day-aligned UTC Window ending at
// the start of "tomorrow" relative to now (so today's partial usage is included).
//
// Accepted forms:
//   - "7d" / "30d"  — the last N days up to and including today
//   - "month"       — from the 1st of the current month through today
//   - "today"       — just the current UTC day
//
// The window is half-open [Start, End); End is the UTC midnight after today.
func parsePeriod(period string, now time.Time) (provider.Window, error) {
	today := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	end := today.AddDate(0, 0, 1) // exclusive: start of tomorrow

	switch p := strings.ToLower(strings.TrimSpace(period)); p {
	case "", "today":
		return provider.Window{Start: today, End: end}, nil
	case "month":
		start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		return provider.Window{Start: start, End: end}, nil
	default:
		if strings.HasSuffix(p, "d") {
			n, err := strconv.Atoi(strings.TrimSuffix(p, "d"))
			if err != nil || n <= 0 {
				return provider.Window{}, fmt.Errorf("invalid period %q: expected a positive number of days like 7d", period)
			}
			// Last N days including today: today - (n-1) .. end.
			start := today.AddDate(0, 0, -(n - 1))
			return provider.Window{Start: start, End: end}, nil
		}
		return provider.Window{}, fmt.Errorf("invalid period %q: use Nd (e.g. 7d), month, or today", period)
	}
}

// previousWindow returns the comparison window immediately preceding the one
// parsePeriod builds for the same period string:
//   - "today"     -> yesterday, [today-1d, today)
//   - "month"     -> the full previous calendar month, [1st prev, 1st cur)
//   - "Nd"        -> the N days before the current window, [start-Nd, start)
//
// Note the deliberate asymmetry for "month": current is month-to-date while
// previous is the full month; the CLI prints both date ranges so this is
// visible to the user.
func previousWindow(period string, now time.Time) (provider.Window, error) {
	cur, err := parsePeriod(period, now)
	if err != nil {
		return provider.Window{}, err
	}

	if strings.ToLower(strings.TrimSpace(period)) == "month" {
		first := cur.Start // parsePeriod set Start to the 1st of the current month
		return provider.Window{Start: first.AddDate(0, -1, 0), End: first}, nil
	}

	// "today" and "Nd" (and the default empty period) shift back by the current
	// window's own length: previous = [start-length, start).
	length := cur.End.Sub(cur.Start)
	return provider.Window{Start: cur.Start.Add(-length), End: cur.Start}, nil
}

// formatWindow renders a half-open window as an inclusive date range,
// e.g. "2025-08-01..2025-08-15" (End is exclusive, so the last day is End-1d).
func formatWindow(w provider.Window) string {
	return w.Start.Format("2006-01-02") + ".." + w.End.AddDate(0, 0, -1).Format("2006-01-02")
}
