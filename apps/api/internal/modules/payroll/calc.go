package payroll

import (
	"sort"
	"time"
)

// Pure payroll arithmetic. Everything is integer paise or basis points; no floats. Rates and ceilings come from the
// payroll.params tables (Params), never from constants here, so a CA can change them with data.

type Slab struct{ From, RateBP int64 }

type PTSlab struct {
	Upto            *int64 // inclusive bound on monthly gross x BasisMonths; nil = no bound
	Amount          int64
	Feb             *int64
	BasisMonths     int64
	Months          []int32
	StateName, Code string
}

type Params struct {
	PFCeiling, PFEeBP, PFErBP, EPSBP, EPSMax, ExclBP  int64
	ESICeiling, ESIEeBP, ESIErBP                      int64
	StdDed, RebateLimit, RebateMax, CessBP, SurchFrom int64
	Slabs                                             []Slab
}

type Input struct {
	Month                      time.Time // first day of the month
	Basic, HRA, Special, Other int64     // contractual monthly amounts
	LOPHalves                  int64     // loss of pay in half days
	AdjEarn, AdjDed            int64
	PF, ESI                    bool
	OldRegime, HasPAN          bool
	PT                         []PTSlab // the employee's state; empty = no PT table
	YTDGross, YTDTDS           int64    // earlier finalised runs of this financial year
}

type Line struct {
	DaysInMonth                       int64
	Basic, HRA, Special, Other, Gross int64 // payable after loss of pay
	PFWages, EPFEe, EPFEr, EPSEr      int64
	ESIWages, ESIEe, ESIEr            int64
	PT, TDS, Deductions, Net          int64
	Warnings                          []string
}

// divRound is round-half-up integer division for non-negative operands.
func divRound(a, b int64) int64 { return (2*a + b) / (2 * b) }

// bp applies basis points with half-up rounding to the paisa.
func bp(x, rate int64) int64 { return divRound(x*rate, 10000) }

// rupeeBP applies rate to x and rounds to a whole rupee (100 paise), half up or up (ceil).
func rupeeBP(x, rate int64, ceil bool) int64 {
	n := x * rate
	if ceil {
		return (n + 999999) / 1000000 * 100
	}
	return (n + 500000) / 1000000 * 100
}

func monthsLeft(m time.Month) int64 { return int64((3-int(m)+12)%12 + 1) } // this month to March

func Compute(p Params, in Input) Line {
	var l Line
	d := int64(in.Month.AddDate(0, 1, -1).Day())
	l.DaysInMonth = d
	lop := min(in.LOPHalves, 2*d)
	pay := func(x int64) int64 { return divRound(x*(2*d-lop), 2*d) }
	l.Basic, l.HRA, l.Special, l.Other = pay(in.Basic), pay(in.HRA), pay(in.Special), pay(in.Other)
	fixed := l.Basic + l.HRA + l.Special + l.Other
	l.Gross = fixed + in.AdjEarn

	if in.PF {
		// Code on Wages: exclusions (allowances) above ExclBP of remuneration count as wages.
		w := l.Basic
		if excl := fixed - l.Basic; excl > bp(fixed, p.ExclBP) {
			w += excl - bp(fixed, p.ExclBP)
		}
		l.PFWages = min(w, p.PFCeiling)
		l.EPFEe = rupeeBP(l.PFWages, p.PFEeBP, false)
		total := rupeeBP(l.PFWages, p.PFErBP, false)
		l.EPSEr = min(rupeeBP(l.PFWages, p.EPSBP, false), p.EPSMax)
		l.EPFEr = total - l.EPSEr
	}

	// ESI applies while the contractual monthly gross is within the ceiling; contributions are on wages paid, rounded up.
	if in.ESI && in.Basic+in.HRA+in.Special+in.Other <= p.ESICeiling {
		l.ESIWages = l.Gross
		l.ESIEe = rupeeBP(l.Gross, p.ESIEeBP, true)
		l.ESIEr = rupeeBP(l.Gross, p.ESIErBP, true)
	}

	l.PT = professionalTax(in.PT, l.Gross, in.Month.Month())
	if len(in.PT) == 0 {
		l.Warnings = append(l.Warnings, "no_pt_state")
	}

	switch {
	case in.OldRegime:
		l.Warnings = append(l.Warnings, "old_regime_tds_not_computed")
	default:
		left := monthsLeft(in.Month.Month())
		annual := max(0, in.YTDGross+l.Gross*left-p.StdDed)
		annual = divRound(annual, 1000) * 1000 // s.288A: total income to the nearest ten rupees
		if annual > p.SurchFrom {
			l.Warnings = append(l.Warnings, "surcharge_not_computed")
		}
		tax := annualTax(p, annual)
		l.TDS = divRound(max(0, tax-in.YTDTDS), left*100) * 100
		if !in.HasPAN && l.TDS > 0 {
			l.Warnings = append(l.Warnings, "no_pan_higher_rate_not_applied")
		}
	}

	l.Deductions = l.EPFEe + l.ESIEe + l.PT + l.TDS + in.AdjDed
	l.Net = l.Gross - l.Deductions
	if l.Net < 0 {
		l.Warnings = append(l.Warnings, "negative_net")
	}
	return l
}

func professionalTax(slabs []PTSlab, gross int64, m time.Month) int64 {
	if len(slabs) == 0 {
		return 0
	}
	s := append([]PTSlab(nil), slabs...)
	sort.Slice(s, func(i, j int) bool {
		if s[i].Upto == nil || s[j].Upto == nil {
			return s[j].Upto == nil && s[i].Upto != nil
		}
		return *s[i].Upto < *s[j].Upto
	})
	for _, x := range s {
		if len(x.Months) > 0 {
			in := false
			for _, mm := range x.Months {
				in = in || int(mm) == int(m)
			}
			if !in {
				return 0
			}
		}
		if x.Upto == nil || gross*max(x.BasisMonths, 1) <= *x.Upto {
			if m == time.February && x.Feb != nil {
				return *x.Feb
			}
			return x.Amount
		}
	}
	return 0
}

// annualTax is the new-regime tax on annual taxable income: slabs, rebate with marginal relief, then cess, whole rupees.
func annualTax(p Params, income int64) int64 {
	var tax int64
	for i, s := range p.Slabs {
		if income <= s.From {
			break
		}
		top := income
		if i+1 < len(p.Slabs) && p.Slabs[i+1].From < income {
			top = p.Slabs[i+1].From
		}
		tax += bp(top-s.From, s.RateBP)
	}
	if income <= p.RebateLimit {
		tax -= min(tax, p.RebateMax)
	} else if excess := income - p.RebateLimit; tax > excess {
		tax = excess // marginal relief: never more than the income above the rebate limit
	}
	return rupeeBP(tax, 10000+p.CessBP, false)
}
