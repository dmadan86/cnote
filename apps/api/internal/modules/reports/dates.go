package reports

import (
	"fmt"
	"time"

	"github.com/cnote/api/internal/command"
)

const dateLayout = "2006-01-02"

var presetNames = map[string]string{
	"this_month": "This month", "last_month": "Last month", "this_quarter": "This quarter", "last_quarter": "Last quarter",
	"this_fy": "This FY", "last_fy": "Last FY", "fy_to_date": "FY to date", "last_12_months": "Last 12 months", "custom": "Custom",
}

// ResolvePeriod turns a period into inclusive IST dates. today is billing.TodayIST(): its Y/M/D already are the IST date.
// Quarters follow the financial year (FY start April: Q1 = Apr-Jun). last_12_months runs from the first day of the
// month 11 months ago to today.
func ResolvePeriod(p Period, fyStartMonth int, today time.Time) (from, to time.Time, label string, err error) {
	if fyStartMonth < 1 || fyStartMonth > 12 {
		return from, to, "", command.ErrInvalid("the financial year start month must be 1 to 12")
	}
	y, m, d := today.Date()
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }
	today = at(y, m, d)
	preset := p.Preset
	if preset == "" && (p.From != "" || p.To != "") {
		preset = "custom"
	}
	fyY := y // calendar year in which the current FY starts
	if int(m) < fyStartMonth {
		fyY--
	}
	fyStart := at(fyY, time.Month(fyStartMonth), 1)
	qStart := at(y, m-time.Month((int(m)-fyStartMonth+12)%12%3), 1)
	monthEnd := func(s time.Time) time.Time { return s.AddDate(0, 1, -1) }
	switch preset {
	case "this_month":
		from = at(y, m, 1)
		to = monthEnd(from)
	case "last_month":
		from = at(y, m-1, 1)
		to = monthEnd(from)
	case "this_quarter":
		from, to = qStart, qStart.AddDate(0, 3, -1)
	case "last_quarter":
		from = qStart.AddDate(0, -3, 0)
		to = qStart.AddDate(0, 0, -1)
	case "this_fy":
		from, to = fyStart, fyStart.AddDate(1, 0, -1)
	case "last_fy":
		from, to = fyStart.AddDate(-1, 0, 0), fyStart.AddDate(0, 0, -1)
	case "fy_to_date":
		from, to = fyStart, today
	case "last_12_months":
		from, to = at(y, m-11, 1), today
	case "custom":
		if from, err = time.Parse(dateLayout, p.From); err != nil {
			return from, to, "", command.ErrInvalid("the start date must look like 2026-04-01")
		}
		if to, err = time.Parse(dateLayout, p.To); err != nil {
			return from, to, "", command.ErrInvalid("the end date must look like 2026-04-01")
		}
		if to.Before(from) {
			return from, to, "", command.ErrInvalid("the start date must be on or before the end date")
		}
		if to.After(from.AddDate(10, 0, 0)) {
			return from, to, "", command.ErrInvalid("a period can be at most 10 years long")
		}
	default:
		return from, to, "", command.ErrInvalid("choose a period: this month, last month, this quarter, last quarter, this FY, last FY, FY to date, last 12 months or custom dates")
	}
	span := fmt.Sprintf("%s – %s", from.Format("2 Jan 2006"), to.Format("2 Jan 2006"))
	return from, to, presetNames[preset] + " (" + span + ")", nil
}
