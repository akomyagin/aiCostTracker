package provider

import "testing"

func TestMoney_RoundTrip(t *testing.T) {
	// MicrosToDollars(DollarsToMicros(v)) == v for amounts with <= 6 decimals.
	values := []float64{1.25, 0.005, 0, 2.5, 0.9, 123.456789}
	for _, v := range values {
		if got := MicrosToDollars(DollarsToMicros(v)); got != v {
			t.Errorf("round-trip %v: got %v", v, got)
		}
	}
}

func TestDollarsToMicros(t *testing.T) {
	tests := []struct {
		in   float64
		want int64
	}{
		{1.25, 1_250_000},
		{0.005, 5_000},
		{0, 0},
		{-1.25, -1_250_000},
		// Rounding on ambiguous cases fixes math.Round (half away from zero).
		{0.0000005, 1},
		{0.0000004, 0},
		{-0.0000005, -1},
		{2.0001, 2_000_100},
	}
	for _, tt := range tests {
		if got := DollarsToMicros(tt.in); got != tt.want {
			t.Errorf("DollarsToMicros(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
