package report

import (
	"fmt"
	"io"
	"math"
)

// CompareTables renders two aggregated tables (current and previous period)
// with window labels, followed by a one-line TOTAL cost delta. Labels are
// human-readable date ranges supplied by the caller. The existing Table
// renderer is reused so both tables match the default report format exactly.
func CompareTables(w io.Writer, curLabel, prevLabel string, cur, prev []Row) error {
	if _, err := fmt.Fprintf(w, "CURRENT [%s]\n", curLabel); err != nil {
		return err
	}
	if err := Table(w, cur); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "PREVIOUS [%s]\n", prevLabel); err != nil {
		return err
	}
	if err := Table(w, prev); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}

	curTotal := totalCost(cur)
	prevTotal := totalCost(prev)
	delta := curTotal - prevTotal
	// Snap to whole cents so a sub-cent float residue (e.g. -0.0001 from summed
	// rounding error) can't surface as a spurious "-$0.00"/"-0.0%" negative zero.
	if math.Round(delta*100) == 0 {
		delta = 0
	}

	pct := "n/a"
	if prevTotal != 0 {
		p := delta / prevTotal * 100
		if math.Round(p*10) == 0 {
			p = 0 // avoid a "-0.0%" artifact from a tiny negative float
		}
		pct = fmt.Sprintf("%+.1f%%", p)
	}

	if _, err := fmt.Fprintf(w, "TOTAL: %s vs %s (%s, %s)\n",
		formatUSD(curTotal), formatUSD(prevTotal), signedUSD(delta), pct); err != nil {
		return err
	}
	return nil
}

// totalCost sums CostUSD across rows.
func totalCost(rows []Row) float64 {
	var t float64
	for _, r := range rows {
		t += r.CostUSD
	}
	return t
}

// signedUSD renders a delta with an explicit leading sign, e.g. "+$1.50",
// "-$0.75", "+$0.00".
func signedUSD(v float64) string {
	if v < 0 {
		return "-" + formatUSD(-v)
	}
	return "+" + formatUSD(v)
}
