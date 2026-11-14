package payroll

import (
	"slices"
	"testing"
	"time"
)

func R(rupees int64) int64 { return rupees * 100 }

var testParams = Params{
	PFCeiling: R(15000), PFEeBP: 1200, PFErBP: 1200, EPSBP: 833, EPSMax: R(1250), ExclBP: 5000,
	ESICeiling: R(21000), ESIEeBP: 75, ESIErBP: 325,
	StdDed: R(75000), RebateLimit: R(1200000), RebateMax: R(60000), CessBP: 400, SurchFrom: R(5000000),
	Slabs: []Slab{{0, 0}, {R(400000), 500}, {R(800000), 1000}, {R(1200000), 1500}, {R(1600000), 2000}, {R(2000000), 2500}, {R(2400000), 3000}},
}

func month(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }

func eq(t *testing.T, what string, got, want int64) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", what, got, want)
	}
}

func up(v int64) *int64 { return &v }

func TestPFCeilingAndEPSSplit(t *testing.T) {
	// April 2026, 30 days, no loss of pay. Basic 25,000 is half of 50,000, so the 50% rule adds nothing.
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(25000), HRA: R(10000), Special: R(15000), PF: true})
	eq(t, "gross", l.Gross, R(50000))
	eq(t, "pf wages capped", l.PFWages, R(15000))
	eq(t, "epf employee 12%", l.EPFEe, R(1800))
	eq(t, "eps 8.33% of 15000 = 1249.5 rounds to 1250", l.EPSEr, R(1250))
	eq(t, "epf employer share", l.EPFEr, R(550))
	eq(t, "no esi above ceiling", l.ESIEe+l.ESIEr, 0)
	eq(t, "net", l.Net, R(50000)-R(1800))
}

func TestPFBelowCeiling(t *testing.T) {
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(10000), HRA: R(5000), PF: true})
	eq(t, "pf wages = basic", l.PFWages, R(10000))
	eq(t, "ee", l.EPFEe, R(1200))
	eq(t, "eps 833.0", l.EPSEr, R(833))
	eq(t, "epf er", l.EPFEr, R(1200)-R(833))
}

func TestFiftyPercentWageRule(t *testing.T) {
	// Basic 6,000 + allowances 8,000 = 14,000: allowances exceed 7,000 (50%) by 1,000, so wages are 7,000.
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(6000), Special: R(8000), PF: true})
	eq(t, "wages", l.PFWages, R(7000))
	eq(t, "epf employee", l.EPFEe, R(840))
	// exactly 50% is not above 50%
	l = Compute(testParams, Input{Month: month(2026, 4), Basic: R(7000), Special: R(7000), PF: true})
	eq(t, "wages at exactly 50%", l.PFWages, R(7000))
	// not applicable: nothing
	l = Compute(testParams, Input{Month: month(2026, 4), Basic: R(7000), Special: R(7000)})
	eq(t, "pf off", l.EPFEe+l.PFWages, 0)
}

func TestLossOfPayProRataOnCalendarDays(t *testing.T) {
	// 30-day month, 2 days lost: 28/30 of every component.
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(30000), HRA: R(15000), LOPHalves: 4})
	eq(t, "basic", l.Basic, R(28000))
	eq(t, "hra", l.HRA, R(14000))
	eq(t, "gross", l.Gross, R(42000))
	// half a day in a 31-day month: 10,000 x 61/62 = 9838.709... rounds to 9838.71
	l = Compute(testParams, Input{Month: month(2026, 5), Basic: R(10000), LOPHalves: 1})
	eq(t, "half day", l.Basic, 983871)
	// more loss than the month has is capped at the whole month
	l = Compute(testParams, Input{Month: month(2026, 5), Basic: R(10000), LOPHalves: 500})
	eq(t, "capped", l.Gross, 0)
	// February of a leap year has 29 days
	eq(t, "days", Compute(testParams, Input{Month: month(2028, 2)}).DaysInMonth, 29)
}

func TestPFWagesFollowLossOfPay(t *testing.T) {
	// Basic 20,000 lose 15 of 30 days: wages 10,000, below the ceiling.
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(20000), LOPHalves: 30, PF: true})
	eq(t, "wages", l.PFWages, R(10000))
	eq(t, "ee", l.EPFEe, R(1200))
}

func TestESIThresholdEdge(t *testing.T) {
	in := Input{Month: month(2026, 4), Basic: R(12000), HRA: R(9000), ESI: true}
	l := Compute(testParams, in) // exactly 21,000: covered
	eq(t, "wages", l.ESIWages, R(21000))
	eq(t, "ee 157.50 rounds up", l.ESIEe, R(158))
	eq(t, "er 682.50 rounds up", l.ESIEr, R(683))
	in.HRA += 100 // 21,001: not covered
	l = Compute(testParams, in)
	eq(t, "above ceiling", l.ESIEe+l.ESIEr+l.ESIWages, 0)
	// the test uses the contractual gross: a covered employee with loss of pay still pays on wages paid
	in = Input{Month: month(2026, 4), Basic: R(21000), ESI: true, LOPHalves: 30}
	l = Compute(testParams, in)
	eq(t, "paid half", l.ESIWages, R(10500))
	eq(t, "ee on 10,500 = 78.75 up", l.ESIEe, R(79))
	// flag off
	eq(t, "off", Compute(testParams, Input{Month: month(2026, 4), Basic: R(10000)}).ESIEe, 0)
}

func TestProfessionalTaxSlabEdges(t *testing.T) {
	ka := []PTSlab{{Upto: up(R(24999)), Amount: 0, BasisMonths: 1}, {Amount: R(200), Feb: up(R(300)), BasisMonths: 1}}
	mh := []PTSlab{{Upto: up(R(7500)), BasisMonths: 1}, {Upto: up(R(10000)), Amount: R(175), BasisMonths: 1}, {Amount: R(200), Feb: up(R(300)), BasisMonths: 1}}
	tn := []PTSlab{{Upto: up(R(21000)), BasisMonths: 6, Months: []int32{9, 3}}, {Upto: up(R(30000)), Amount: R(135), BasisMonths: 6, Months: []int32{9, 3}},
		{Amount: R(1250), BasisMonths: 6, Months: []int32{9, 3}}}
	for _, c := range []struct {
		name  string
		slabs []PTSlab
		m     time.Month
		gross int64
		want  int64
	}{
		{"KA 24,999", ka, 4, R(24999), 0}, {"KA 25,000", ka, 4, R(25000), R(200)}, {"KA Feb", ka, 2, R(25000), R(300)}, {"KA Feb low", ka, 2, R(20000), 0},
		{"MH 7,500", mh, 4, R(7500), 0}, {"MH 7,501", mh, 4, R(7501), R(175)}, {"MH 10,000", mh, 4, R(10000), R(175)}, {"MH 10,001", mh, 4, R(10001), R(200)},
		{"MH Feb", mh, 2, R(10001), R(300)}, {"MH Feb mid slab has no override", mh, 2, R(9000), R(175)},
		{"TN 3,500 x 6 = 21,000", tn, 9, R(3500), 0}, {"TN 3,501 in Sep", tn, 9, R(3501), R(135)}, {"TN not in October", tn, 10, R(30000), 0},
		{"TN March top", tn, 3, R(20000), R(1250)}, {"no table", nil, 4, R(90000), 0},
	} {
		eq(t, c.name, professionalTax(c.slabs, c.gross, c.m), c.want)
	}
	if l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(10000)}); !slices.Contains(l.Warnings, "no_pt_state") {
		t.Errorf("a missing PT table must warn, got %v", l.Warnings)
	}
}

func TestTDSWorkedExample(t *testing.T) {
	// Gross 2,00,000 a month from April: projected 24,00,000 - 75,000 = 23,25,000.
	// 4-8L 5% = 20,000; 8-12L 10% = 40,000; 12-16L 15% = 60,000; 16-20L 20% = 80,000; 20L-23.25L 25% = 81,250
	// = 2,81,250; cess 4% = 11,250; annual 2,92,500; a twelfth is 24,375.
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(100000), HRA: R(50000), Special: R(50000), HasPAN: true})
	eq(t, "tds", l.TDS, R(24375))
	// next month, after April's TDS was deducted, nothing moves
	l2 := Compute(testParams, Input{Month: month(2026, 5), Basic: R(100000), HRA: R(50000), Special: R(50000), HasPAN: true, YTDGross: R(200000), YTDTDS: R(24375)})
	eq(t, "tds may", l2.TDS, R(24375))
	// a bonus in the last month lifts the projection; the remainder is deducted in March
	l3 := Compute(testParams, Input{Month: month(2027, 3), Basic: R(100000), HRA: R(50000), Special: R(50000), HasPAN: true, YTDGross: R(2200000), YTDTDS: R(24375 * 11)})
	eq(t, "march true-up", l3.TDS, R(24375))
	l4 := Compute(testParams, Input{Month: month(2027, 3), Basic: R(100000), HRA: R(50000), Special: R(50000), AdjEarn: R(100000), YTDGross: R(2200000), YTDTDS: R(24375 * 11)})
	// 25,00,000 - 75,000 = 24,25,000: 2,81,250 -> 20L..24L 25% = 1,00,000, 24L-24.25L 30% = 7,500: 3,07,500 + cess = 3,19,800
	eq(t, "march with bonus", l4.TDS, R(319800)-R(24375*11))
	if !slices.Contains(Compute(testParams, Input{Month: month(2026, 4), Basic: R(200000)}).Warnings, "no_pan_higher_rate_not_applied") {
		t.Error("TDS without PAN must warn")
	}
}

func TestTDSAroundRebateThreshold(t *testing.T) {
	// March, so the whole year's tax falls in one month. Projected annual = YTD + March gross.
	at := func(annualGross int64) int64 {
		return Compute(testParams, Input{Month: month(2027, 3), Basic: R(10000), HasPAN: true, YTDGross: R(annualGross) - R(10000)}).TDS
	}
	eq(t, "taxable exactly 12,00,000: rebate wipes the tax", at(1275000), 0)
	eq(t, "taxable 11,99,990", at(1274990), 0)
	// taxable 12,10,000: tax 61,500 but marginal relief caps it at 10,000, cess 400 = 10,400
	eq(t, "taxable 12,10,000: marginal relief", at(1285000), R(10400))
	// taxable 12,01,000 (rounded to ten rupees already): relief caps at 1,000, + cess = 1,040
	eq(t, "taxable 12,01,000", at(1276000), R(1040))
	// high up the relief is gone: 13,75,000 taxable: 20,000+40,000+ 15% of 1,75,000 = 26,250 = 86,250 + cess 3,450 = 89,700
	eq(t, "no relief", at(1450000), R(89700))
	// salary under the basic exemption
	eq(t, "below std", at(60000), 0)
}

func TestTDSOldRegimeIsNotComputed(t *testing.T) {
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(300000), OldRegime: true, HasPAN: true})
	eq(t, "tds", l.TDS, 0)
	if !slices.Contains(l.Warnings, "old_regime_tds_not_computed") {
		t.Errorf("warnings %v", l.Warnings)
	}
}

func TestAdjustmentsAndNet(t *testing.T) {
	l := Compute(testParams, Input{Month: month(2026, 4), Basic: R(20000), PF: true, AdjEarn: R(2000), AdjDed: R(500), HasPAN: true})
	eq(t, "gross includes one-off earning", l.Gross, R(22000))
	eq(t, "pf wages ignore one-offs", l.PFWages, R(15000))
	eq(t, "deductions", l.Deductions, R(1800)+R(500))
	eq(t, "net", l.Net, R(22000)-R(2300))
	l = Compute(testParams, Input{Month: month(2026, 4), Basic: R(1000), AdjDed: R(5000)})
	if !slices.Contains(l.Warnings, "negative_net") {
		t.Errorf("want negative_net, got %v", l.Warnings)
	}
}

func TestMonthsLeft(t *testing.T) {
	for m, want := range map[time.Month]int64{4: 12, 5: 11, 12: 4, 1: 3, 3: 1} {
		eq(t, m.String(), monthsLeft(m), want)
	}
}
