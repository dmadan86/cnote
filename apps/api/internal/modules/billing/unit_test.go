package billing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
)

// Ports of the #[cfg(test)] modules in crates/cnote-finance/src (gst.rs, irn.rs, ledger.rs, billing.rs, reports.rs).

func d(s string) dec {
	v, ok := parseDec(s)
	if !ok {
		panic(s)
	}
	return v
}

func line(qty string, price int64, rate uint32) gstLine {
	return gstLine{qty: d(qty), price: price, rateBP: rate}
}

func want[T comparable](t *testing.T, got, w T) {
	t.Helper()
	if got != w {
		t.Fatalf("got %v want %v", got, w)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestTaxableRoundsHalfUp(t *testing.T) {
	for _, c := range []struct {
		q    string
		p, w int64
	}{{"1", 12_345, 12_345}, {"2.5", 101, 253}, {"0.5", 3, 2}, {"0.333", 100, 33}, {"0.335", 100, 34}, {"0.001", 100, 0}, {"10.25", 0, 0}} {
		want(t, must(taxablePaise(d(c.q), c.p)), c.w)
	}
}

func TestTaxableRejectsBadInput(t *testing.T) {
	_, err := taxablePaise(d("0"), 1)
	want(t, err, error(errQuantity))
	_, err = taxablePaise(d("-1"), 1)
	want(t, err, error(errQuantity))
	_, err = taxablePaise(d("1"), -1)
	want(t, err, error(errPrice))
	_, err = taxablePaise(d("1000000000000"), 1<<63-1)
	want(t, err, error(errOverflow))
	_, err = taxablePaise(d("2"), 1<<63-1)
	want(t, err, error(errOverflow))
}

func TestTaxRoundsHalfUp(t *testing.T) {
	for _, c := range []struct {
		tx   int64
		r    uint32
		want int64
	}{{10_000, 1800, 1800}, {50, 500, 3}, {49, 500, 2}, {1, 1800, 0}, {3, 1800, 1}, {0, 1800, 0}} {
		want(t, must(taxPaise(c.tx, c.r)), c.want)
	}
}

func TestIntraStateSplitsCGSTSGST(t *testing.T) {
	x := must(lineTaxOf(10_000, 1800, intra, false))
	want(t, [3]int64{x.cgst, x.sgst, x.igst}, [3]int64{900, 900, 0})
}

func TestOddPaisaGoesToSGSTAndNothingIsLost(t *testing.T) {
	x := must(lineTaxOf(105, 1800, intra, false))
	want(t, [2]int64{x.cgst, x.sgst}, [2]int64{9, 10})
	for taxable := int64(0); taxable < 2000; taxable++ {
		for _, rate := range []uint32{0, 25, 300, 500, 1200, 1800, 2800} {
			x := must(lineTaxOf(taxable, rate, intra, false))
			want(t, x.cgst+x.sgst, must(taxPaise(taxable, rate)))
			if x.sgst-x.cgst > 1 || x.sgst < x.cgst {
				t.Fatalf("split %d %d", taxable, rate)
			}
		}
	}
}

func TestInterStateIsAllIGST(t *testing.T) {
	x := must(lineTaxOf(10_000, 1800, inter, false))
	want(t, [3]int64{x.cgst, x.sgst, x.igst}, [3]int64{0, 0, 1800})
}

func TestReverseChargeChargesNoTax(t *testing.T) {
	x := must(lineTaxOf(10_000, 1800, intra, true))
	want(t, [3]int64{x.cgst, x.sgst, x.igst}, [3]int64{0, 0, 0})
	inv := must(computeInvoice([]gstLine{line("1", 10_000, 1800)}, "29", "29", true))
	want(t, inv.reverse, true)
	want(t, inv.total, 10_000)
}

func TestRateAbove28PercentRejected(t *testing.T) {
	_, err := lineTaxOf(100, 2801, inter, false)
	want(t, err, error(errRate))
}

func TestInvoiceIntraTotals(t *testing.T) {
	inv := must(computeInvoice([]gstLine{line("2", 50_000, 1800), line("1.5", 20_001, 500)}, "29", "29", false))
	want(t, inv.supply, intra)
	want(t, inv.lines[0].taxable, 100_000)
	want(t, inv.lines[1].taxable, 30_002)
	want(t, inv.subtotal, 130_002)
	want(t, inv.cgst, 9_000+750)
	want(t, inv.sgst, 9_000+750)
	want(t, inv.igst, 0)
	want(t, inv.total, inv.subtotal+inv.cgst+inv.sgst)
}

func TestInvoiceInterTotalsAndStateErrors(t *testing.T) {
	inv := must(computeInvoice([]gstLine{line("1", 100_000, 1200)}, "29", "27", false))
	want(t, inv.supply, inter)
	want(t, [2]int64{inv.igst, inv.total}, [2]int64{12_000, 112_000})
	_, err := computeInvoice(nil, "29", "00", false)
	want(t, err.Error(), "unknown GST state code 00")
	want(t, must(computeInvoice(nil, "29", "29", false)).total, 0)
}

func TestHSNIsValidatedPerLine(t *testing.T) {
	h := "998314"
	l := line("1", 100, 1800)
	l.hsn = &h
	if _, err := computeInvoice([]gstLine{l}, "29", "29", false); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"99", "99831", "99831x", "", "123456789"} {
		b := bad
		l.hsn = &b
		_, err := computeInvoice([]gstLine{l}, "29", "29", false)
		want(t, err, error(errHsn))
	}
	if validateHSN("8471") != nil || validateHSN("84713010") != nil {
		t.Fatal("hsn")
	}
}

func TestGSTINKnownValidAndRoundtrip(t *testing.T) {
	want(t, must(validateGSTIN("27AAPFU0939F1ZV")), "27AAPFU0939F1ZV")
	want(t, must(validateGSTIN(" 27aapfu0939f1zv ")), "27AAPFU0939F1ZV")
	for _, p := range []string{"29ABCDE1234F1Z", "07AAACC1206D1Z", "33AABCU9603R1Z", "99ABCDE1234F2Z"} {
		g := completeGSTIN(p)
		want(t, must(validateGSTIN(g)), g)
	}
}

func TestGSTINRejectsBadValues(t *testing.T) {
	for _, c := range []struct {
		in   string
		want error
	}{
		{"", errGstinLength}, {"27AAPFU0939F1Z", errGstinLength}, {"27AAPFU0939F1ZVX", errGstinLength},
		{"27AAPFU0939F1ZX", errGstinChecksum}, {"2XAAPFU0939F1ZV", errGstinFormat}, {"27AAPF10939F1ZV", errGstinFormat},
		{"27AAPFU0939F1YV", errGstinFormat}, {"27AAPFU0939F0ZV", errGstinFormat}, {"27AAPFU0938F1ZV", errGstinChecksum},
	} {
		_, err := validateGSTIN(c.in)
		if err != c.want {
			t.Fatalf("%q: got %v want %v", c.in, err, c.want)
		}
	}
	_, err := validateGSTIN(completeGSTIN("00AAPFU0939F1Z"))
	want(t, err.Error(), "GSTIN has unknown state code 00")
}

func TestStateTable(t *testing.T) {
	n, _ := stateName("29")
	want(t, n, "Karnataka")
	n, _ = stateName("27")
	want(t, n, "Maharashtra")
	if _, ok := stateName("00"); ok {
		t.Fatal("00")
	}
	if _, ok := stateName("39"); ok {
		t.Fatal("39")
	}
	seen := map[string]bool{}
	for _, s := range states {
		if len(s.code) != 2 || seen[s.code] {
			t.Fatal(s.code)
		}
		seen[s.code] = true
	}
	want(t, gstinState("29ABCDE1234F1Z5"), "29")
	if validateState("97") != nil || validateState("abc") == nil {
		t.Fatal("validateState")
	}
}

func TestFinancialYearBoundaries(t *testing.T) {
	fy := func(y int, m time.Month, dd int) string {
		return financialYear(time.Date(y, m, dd, 0, 0, 0, 0, time.UTC))
	}
	want(t, fy(2026, 4, 1), "2026-27")
	want(t, fy(2027, 3, 31), "2026-27")
	want(t, fy(2027, 4, 1), "2027-28")
	want(t, fy(2026, 1, 1), "2025-26")
	want(t, fy(2099, 12, 31), "2099-00")
}

func TestIRNWindowBoundaries(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	w := func(n int, e bool) (window, int64) { return irnWindow(base, base.AddDate(0, 0, n), e) }
	check := func(n int, e bool, ww window, left int64) {
		t.Helper()
		g, l := w(n, e)
		if g != ww || l != left {
			t.Fatalf("day %d: %v %d", n, g, l)
		}
	}
	check(0, true, windowOpen, 0)
	check(19, true, windowOpen, 0)
	check(20, true, windowWarn, 10)
	check(28, true, windowWarn, 2)
	check(29, true, windowBlocked, 0)
	check(45, true, windowBlocked, 0)
	check(45, false, windowNotEnforced, 0)
	check(-3, true, windowOpen, 0)
}

func TestSandboxIsDeterministic(t *testing.T) {
	r := irnRequest{SupplierGSTIN: "27AAPFU0939F1ZV", DocType: "INV", Number: "INV/2026-27/0001", FinancialYear: "2026-27",
		InvoiceDate: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), BuyerGSTIN: "29AABCT1332L1ZS", TotalMinor: 118_000}
	a := must(sandboxGSP{}.GenerateIRN(context.Background(), r))
	b := must(sandboxGSP{}.GenerateIRN(context.Background(), r))
	want(t, a.IRN, b.IRN)
	want(t, a.AckNo, b.AckNo)
	h := sha256.Sum256([]byte("27AAPFU0939F1ZVINVINV/2026-27/00012026-27"))
	want(t, a.IRN, hex.EncodeToString(h[:]))
	r2 := r
	r2.DocType = "CRN"
	if must(sandboxGSP{}.GenerateIRN(context.Background(), r2)).IRN == a.IRN {
		t.Fatal("doc type must change the IRN")
	}
	if !strings.Contains(a.SignedQR, ".") {
		t.Fatal("qr")
	}
}

func TestSaleLegsBalanceBothWays(t *testing.T) {
	for _, rev := range []bool{false, true} {
		var dd, cc int64
		for _, l := range saleLegs(10_000, 900, 901, 0, rev) {
			dd += l.debit
			cc += l.credit
		}
		want(t, dd, cc)
	}
}

func TestNumbersAndSettlement(t *testing.T) {
	want(t, formatNumber("INV", "2026-27", 1), "INV/2026-27/0001")
	want(t, formatNumber("CN", "2026-27", 12345), "CN/2026-27/12345")
	want(t, settledStatus(100, 0, 0, 0, "irn_generated"), "irn_generated")
	want(t, settledStatus(100, 40, 0, 0, "issued"), "partly_paid")
	want(t, settledStatus(100, 100, 0, 0, "issued"), "paid")
	want(t, settledStatus(100, 40, 60, 0, "partly_paid"), "paid")
	want(t, settledStatus(100, 0, 0, 100, "issued"), "paid")
}

func TestLineParsing(t *testing.T) {
	s := func(v string) *string { return &v }
	l := func(q *string, p int64, h *string) lineIn {
		return lineIn{description: "Work", hsn: h, qty: q, price: p}
	}
	ok := must(parseLines([]lineIn{l(s("2.5"), 100, s("998314"))}))
	want(t, ok[0].g.qtyText, "2.5")
	want(t, ok[0].g.rateBP, 1800)
	want(t, must(parseLines([]lineIn{l(nil, 100, nil)}))[0].g.qtyText, "1")
	for _, bad := range [][]lineIn{nil, {l(s("0"), 100, nil)}, {l(nil, -1, nil)}, {l(nil, 1, s("12"))}} {
		if _, err := parseLines(bad); err == nil {
			t.Fatalf("expected error for %v", bad)
		}
	}
	_, err := parseLines([]lineIn{l(nil, 1, nil), {description: "  ", price: 1}})
	want(t, err.(*command.Error).Message, "line description is required")
}

func TestAgeBuckets(t *testing.T) {
	for _, c := range []struct {
		d int64
		b string
	}{{-5, "0-30"}, {0, "0-30"}, {30, "0-30"}, {31, "31-60"}, {60, "31-60"}, {61, "61-90"}, {90, "61-90"}, {91, "90+"}} {
		want(t, ageBucket(c.d), c.b)
	}
}

func TestMoneyJSON(t *testing.T) {
	want(t, rupees(118_050).String(), "1180.5")
	want(t, rupees(5).String(), "0.05")
	want(t, rupees(-250).String(), "-2.5")
	want(t, rupees(1800).String(), "18.0")
	want(t, tallyAmount(-118_000), "-1180.00")
}

func TestPeriodsAndXML(t *testing.T) {
	f, to, err := monthRange("2026-02")
	if err != nil {
		t.Fatal(err)
	}
	want(t, fmtDate(f)+" "+fmtDate(to), "2026-02-01 2026-02-28")
	_, to, _ = monthRange("2026-12")
	want(t, fmtDate(to), "2026-12-31")
	for _, bad := range []string{"2026-13", "x", "2026-00", "++2026-01", "2026--1", ""} {
		if _, _, err := monthRange(bad); err == nil {
			t.Fatalf("%q", bad)
		}
	}
	want(t, xmlEscape(`A & B <"c">`), "A &amp; B &lt;&quot;c&quot;&gt;")
}

func TestB2CLLimitSwitchesOn2024Aug1(t *testing.T) {
	want(t, b2clLimitMinor(time.Date(2024, 7, 31, 0, 0, 0, 0, time.UTC)), 25_000_000)
	want(t, b2clLimitMinor(time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)), 10_000_000)
}

func TestQuantityText(t *testing.T) {
	for in, w := range map[string]string{"2.5": "2.5", "2.50": "2.5", "1.0": "1.0", "10": "10", "-0": "-0.0", "0.5": "0.5", "10.00": "10.0"} {
		got, err := quantityText([]byte(in))
		if err != nil || got != w {
			t.Fatalf("%s: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"1e2", "0.00001", "0.1234567890123456", "12345678901234567890"} {
		if _, err := quantityText([]byte(in)); err == nil {
			t.Fatalf("%s should fall back", in)
		}
	}
}
