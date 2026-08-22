package provider

import "math"

// microsPerDollar is the fixed scale of the integer money representation:
// 1 USD == 1_000_000 micro-dollars.
const microsPerDollar = 1_000_000

// DollarsToMicros converts a floating-point USD amount (as decoded from a
// provider response or user config) into integer micro-dollars, rounding to
// the nearest micro (math.Round: half away from zero). This is the ONLY
// sanctioned float->money conversion; call it at the point where a dollar
// amount first appears as float64 and never accumulate floats afterwards.
func DollarsToMicros(v float64) int64 {
	return int64(math.Round(v * microsPerDollar))
}

// MicrosToDollars converts integer micro-dollars back to float64 USD for
// rendering (table/JSON/chart scaling). Exact for |m| <= 2^53 micros (~$9e9).
func MicrosToDollars(m int64) float64 {
	return float64(m) / microsPerDollar
}
