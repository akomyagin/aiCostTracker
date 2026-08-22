package report

import (
	"fmt"
	"io"
	"math"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/akomyagin/aiCostTracker/internal/provider"
)

// chartWidth is the maximum bar length in "█" (U+2588) runes: the day with the
// highest spend fills exactly this many blocks, every other day is scaled to it.
const chartWidth = 40

// DayTotal is total spend across all providers/models for one UTC day.
type DayTotal struct {
	Day     time.Time // UTC midnight
	CostUSD float64
}

// AggregateByDay sums records into one DayTotal per UTC day of the window,
// including zero-spend days, so charts have no gaps. Records outside
// [w.Start, w.End) are ignored. Result is ordered chronologically.
func AggregateByDay(records []provider.UsageRecord, w provider.Window) []DayTotal {
	byDay := map[time.Time]float64{}
	for _, r := range records {
		d := r.Day.UTC().Truncate(24 * time.Hour)
		if d.Before(w.Start) || !d.Before(w.End) {
			continue
		}
		byDay[d] += r.CostUSD
	}

	var days []DayTotal
	for d := w.Start; d.Before(w.End); d = d.AddDate(0, 0, 1) {
		key := d.UTC().Truncate(24 * time.Hour)
		days = append(days, DayTotal{Day: key, CostUSD: byDay[key]})
	}
	return days
}

// BarChart renders day totals as an aligned horizontal bar chart. Bars are
// scaled to the maximum day (longest bar = chartWidth runes); any non-zero
// day gets at least one block so small spend stays visible. When every day is
// zero the CHART column is empty for all rows (no division by zero).
func BarChart(w io.Writer, days []DayTotal) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	if _, err := fmt.Fprintln(tw, "DAY\tCOST (USD)\tCHART"); err != nil {
		return err
	}

	var max float64
	for _, d := range days {
		if d.CostUSD > max {
			max = d.CostUSD
		}
	}

	for _, d := range days {
		n := 0
		if max > 0 && d.CostUSD > 0 {
			n = int(math.Round(d.CostUSD / max * chartWidth))
			if n < 1 {
				n = 1
			}
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n",
			d.Day.Format("2006-01-02"), formatUSD(d.CostUSD), strings.Repeat("█", n)); err != nil {
			return err
		}
	}

	return tw.Flush()
}
