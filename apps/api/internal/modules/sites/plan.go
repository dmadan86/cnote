package sites

import "time"

// Plan constants (design 17.12): AI edits per calendar month in Asia/Kolkata.
const (
	editsIncluded = 100
	editsPro      = 500
)

func editLimit(pro bool) int {
	if pro {
		return editsPro
	}
	return editsIncluded
}

var ist = time.FixedZone("IST", 330*60)

// monthWindow is the IST calendar month containing now: its start and the start of the next one.
func monthWindow(now time.Time) (time.Time, time.Time) {
	n := now.In(ist)
	start := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, ist)
	return start, start.AddDate(0, 1, 0)
}
