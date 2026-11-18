package reports

import (
	"testing"
	"time"
)

func d(s string) time.Time {
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestResolvePeriod(t *testing.T) {
	cases := []struct {
		name, preset, today, from, to string
		fy                            int
	}{
		{"month", "this_month", "2026-11-18", "2026-11-01", "2026-11-30", 4},
		{"leap feb", "this_month", "2028-02-10", "2028-02-01", "2028-02-29", 4},
		{"last month across year", "last_month", "2026-01-15", "2025-12-01", "2025-12-31", 4},
		{"q fy4 april", "this_quarter", "2026-04-01", "2026-04-01", "2026-06-30", 4},
		{"q fy4 march is Q4", "this_quarter", "2027-03-31", "2027-01-01", "2027-03-31", 4},
		{"q fy4 jan", "this_quarter", "2026-01-01", "2026-01-01", "2026-03-31", 4},
		{"q fy4 nov", "this_quarter", "2026-11-18", "2026-10-01", "2026-12-31", 4},
		{"last q fy4 april", "last_quarter", "2026-04-10", "2026-01-01", "2026-03-31", 4},
		{"last q fy4 jan", "last_quarter", "2026-01-10", "2025-10-01", "2025-12-31", 4},
		{"q fy1 may", "this_quarter", "2026-05-05", "2026-04-01", "2026-06-30", 1},
		{"last q fy1 jan", "last_quarter", "2026-01-05", "2025-10-01", "2025-12-31", 1},
		{"fy4 in november", "this_fy", "2026-11-18", "2026-04-01", "2027-03-31", 4},
		{"fy4 on 31 march", "this_fy", "2026-03-31", "2025-04-01", "2026-03-31", 4},
		{"fy4 on 1 april", "this_fy", "2026-04-01", "2026-04-01", "2027-03-31", 4},
		{"fy1", "this_fy", "2026-11-18", "2026-01-01", "2026-12-31", 1},
		{"last fy4", "last_fy", "2026-11-18", "2025-04-01", "2026-03-31", 4},
		{"last fy4 in march", "last_fy", "2026-03-10", "2024-04-01", "2025-03-31", 4},
		{"last fy1", "last_fy", "2026-01-01", "2025-01-01", "2025-12-31", 1},
		{"fy to date", "fy_to_date", "2026-11-18", "2026-04-01", "2026-11-18", 4},
		{"last 12", "last_12_months", "2026-11-18", "2025-12-01", "2026-11-18", 4},
		{"last 12 in jan", "last_12_months", "2026-01-31", "2025-02-01", "2026-01-31", 4},
	}
	for _, c := range cases {
		from, to, _, err := ResolvePeriod(Period{Preset: c.preset}, c.fy, d(c.today))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if from.Format(dateLayout) != c.from || to.Format(dateLayout) != c.to {
			t.Errorf("%s: got %s..%s want %s..%s", c.name, from.Format(dateLayout), to.Format(dateLayout), c.from, c.to)
		}
	}
}

func TestResolvePeriodLabelAndCustom(t *testing.T) {
	_, _, lbl, err := ResolvePeriod(Period{Preset: "this_fy"}, 4, d("2026-11-18"))
	if err != nil || lbl != "This FY (1 Apr 2026 – 31 Mar 2027)" {
		t.Fatalf("label %q %v", lbl, err)
	}
	_, _, lbl, _ = ResolvePeriod(Period{From: "2026-04-01", To: "2026-04-30"}, 4, d("2026-11-18"))
	if lbl != "Custom (1 Apr 2026 – 30 Apr 2026)" {
		t.Fatalf("custom label %q", lbl)
	}
	bad := []Period{
		{Preset: "nope"}, {}, {Preset: "custom", From: "2026-4-1", To: "2026-04-30"}, {Preset: "custom", From: "2026-05-01", To: "2026-04-30"},
		{Preset: "custom", From: "2010-01-01", To: "2020-01-02"}, {Preset: "custom", From: "2026-02-30", To: "2026-03-01"},
		{Preset: "custom", From: "'; drop table x;--", To: "2026-03-01"},
	}
	for _, p := range bad {
		if _, _, _, err := ResolvePeriod(p, 4, d("2026-11-18")); err == nil {
			t.Errorf("expected error for %+v", p)
		}
	}
	if _, _, _, err := ResolvePeriod(Period{Preset: "custom", From: "2016-01-01", To: "2026-01-01"}, 4, d("2026-11-18")); err != nil {
		t.Errorf("exactly 10 years should pass: %v", err)
	}
	if _, _, _, err := ResolvePeriod(Period{Preset: "this_fy"}, 13, d("2026-11-18")); err == nil {
		t.Error("fy month 13 must fail")
	}
}
