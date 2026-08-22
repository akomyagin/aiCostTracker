package cli

import (
	"testing"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

func TestParsePeriod(t *testing.T) {
	now := time.Date(2025, 8, 15, 13, 30, 0, 0, time.UTC)
	today := time.Date(2025, 8, 15, 0, 0, 0, 0, time.UTC)
	tomorrow := today.AddDate(0, 0, 1)

	tests := []struct {
		period    string
		wantStart time.Time
		wantErr   bool
	}{
		{"today", today, false},
		{"", today, false}, // default = today
		{"7d", today.AddDate(0, 0, -6), false},
		{"1d", today, false},
		{"30d", today.AddDate(0, 0, -29), false},
		{"month", time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), false},
		{"MONTH", time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC), false}, // case-insensitive
		{"0d", time.Time{}, true},
		{"-3d", time.Time{}, true},
		{"garbage", time.Time{}, true},
		{"7", time.Time{}, true}, // missing d suffix
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			w, err := parsePeriod(tt.period, now)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsePeriod(%q) expected error", tt.period)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePeriod(%q): %v", tt.period, err)
			}
			if !w.Start.Equal(tt.wantStart) {
				t.Errorf("Start = %v, want %v", w.Start, tt.wantStart)
			}
			if !w.End.Equal(tomorrow) {
				t.Errorf("End = %v, want %v (start of tomorrow)", w.End, tomorrow)
			}
		})
	}
}

func TestPreviousWindow(t *testing.T) {
	now := time.Date(2025, 8, 15, 13, 30, 0, 0, time.UTC)
	utc := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

	tests := []struct {
		name      string
		period    string
		now       time.Time
		wantStart time.Time
		wantEnd   time.Time
		wantErr   bool
	}{
		{name: "today", period: "today", now: now, wantStart: utc(2025, 8, 14), wantEnd: utc(2025, 8, 15)},
		{name: "7d", period: "7d", now: now, wantStart: utc(2025, 8, 2), wantEnd: utc(2025, 8, 9)},
		{name: "month", period: "month", now: now, wantStart: utc(2025, 7, 1), wantEnd: utc(2025, 8, 1)},
		{name: "month year boundary", period: "month", now: time.Date(2025, 1, 10, 0, 0, 0, 0, time.UTC), wantStart: utc(2024, 12, 1), wantEnd: utc(2025, 1, 1)},
		{name: "garbage", period: "garbage", now: now, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := previousWindow(tt.period, tt.now)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("previousWindow(%q) expected error", tt.period)
				}
				return
			}
			if err != nil {
				t.Fatalf("previousWindow(%q): %v", tt.period, err)
			}
			if !w.Start.Equal(tt.wantStart) {
				t.Errorf("Start = %v, want %v", w.Start, tt.wantStart)
			}
			if !w.End.Equal(tt.wantEnd) {
				t.Errorf("End = %v, want %v", w.End, tt.wantEnd)
			}
		})
	}
}

func TestFormatWindow(t *testing.T) {
	w := provider.Window{
		Start: time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),
		End:   time.Date(2025, 8, 16, 0, 0, 0, 0, time.UTC),
	}
	if got := formatWindow(w); got != "2025-08-01..2025-08-15" {
		t.Errorf("formatWindow = %q, want %q", got, "2025-08-01..2025-08-15")
	}
}
