package cli

import (
	"testing"
	"time"
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
