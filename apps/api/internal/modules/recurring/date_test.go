package recurring

import (
	"testing"
	"time"
)

func TestRecurringNextDate(t *testing.T) {
	for _, tt := range []struct {
		start, current, freq string
		interval             int
		want                 string
	}{
		{"2025-01-31", "2025-01-31", "monthly", 1, "2025-02-28"},
		{"2025-01-31", "2025-02-28", "monthly", 1, "2025-03-31"},
		{"2024-01-31", "2024-01-31", "monthly", 1, "2024-02-29"},
		{"2024-02-29", "2024-02-29", "yearly", 1, "2025-02-28"},
		{"2024-02-29", "2027-02-28", "yearly", 1, "2028-02-29"},
		{"2025-01-31", "2025-01-31", "quarterly", 1, "2025-04-30"},
		{"2025-01-31", "2025-04-30", "quarterly", 1, "2025-07-31"},
		{"2025-12-31", "2025-12-31", "monthly", 2, "2026-02-28"},
	} {
		t.Run(tt.current+tt.freq+tt.want, func(t *testing.T) {
			s, _ := time.Parse(time.DateOnly, tt.start)
			c, _ := time.Parse(time.DateOnly, tt.current)
			if got := NextDate(s, c, tt.freq, tt.interval).Format(time.DateOnly); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}
