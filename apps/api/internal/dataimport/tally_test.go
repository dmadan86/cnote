package dataimport

import (
	"errors"
	"strings"
	"testing"
)

func ledgerByName(t *testing.T, tl *Tally, name string) TallyLedger {
	t.Helper()
	for _, l := range tl.Ledgers {
		if l.Name == name {
			return l
		}
	}
	t.Fatalf("ledger %q not found in %+v", name, tl.Ledgers)
	return TallyLedger{}
}

func TestTallyPrime2Flat(t *testing.T) {
	for _, file := range []string{"masters_prime2_utf8.xml", "masters_prime2_utf16le.xml"} {
		t.Run(file, func(t *testing.T) {
			tl, err := ParseTally(fixture(t, file))
			if err != nil {
				t.Fatal(err)
			}
			want := Found{Customers: 1, Vendors: 1, Ledgers: 4, Items: 2, Units: 1, Godowns: 1, Groups: 3}
			if tl.Found != want {
				t.Errorf("found %+v want %+v", tl.Found, want)
			}
			s := ledgerByName(t, tl, "Sharma Traders & Sons")
			if s.Class != ClassCustomer || s.PrimaryGroup != "Sundry Debtors" || s.OpeningDr != 11800000 || s.OpeningCr != 0 ||
				s.GSTIN != "27AAPFU0939F1ZV" || s.PAN != "AAPFU0939F" || s.StateCode != "27" || s.StateName != "Maharashtra" ||
				s.Address != "12 MG Road, Pune 411001" || s.Email != "accounts@sharma.example" || s.Phone != "9876543210" {
				t.Errorf("%+v", s)
			}
			if v := ledgerByName(t, tl, "Bangalore Steels"); v.Class != ClassVendor || v.OpeningCr != 4500050 || v.StateCode != "29" {
				t.Errorf("%+v", v)
			}
			if b := ledgerByName(t, tl, "HDFC Bank"); b.Class != ClassBank || b.OpeningDr != 25000000 {
				t.Errorf("%+v", b)
			}
			if r := ledgerByName(t, tl, "Interest Received"); r.Class != ClassRevenue {
				t.Errorf("revenue via ISREVENUE flags: %+v", r)
			}
			if r := ledgerByName(t, tl, "Sales - Domestic"); r.Class != ClassRevenue {
				t.Errorf("%+v", r)
			}
			if m := ledgerByName(t, tl, "Mystery Ledger"); m.Class != ClassAsset || m.OpeningDr != 10000 {
				t.Errorf("unknown group falls back to deemed-positive flags: %+v", m)
			}
			for _, l := range tl.Ledgers {
				if l.Name == "Inside voucher" {
					t.Error("ledgers inside vouchers must be ignored")
				}
			}
			it := tl.Items[0]
			if it.Name != "Steel widget 10mm" || it.BaseUnit != "Nos" || it.QtyMilli != 100000 || it.ValueMinor != 2000000 || it.RateMinor != 20000 || it.HSN != "7318" || it.HasGSTRate {
				t.Errorf("%+v", it)
			}
			if tl.Items[1].HSN != "" || tl.Items[1].QtyMilli != 12500 {
				t.Errorf("%+v", tl.Items[1])
			}
			if tl.Units[0] != "Nos" || tl.Godowns[0].Name != "Main Location" || tl.Godowns[0].Parent != "Primary" {
				t.Errorf("units %v godowns %v (control chars must be stripped)", tl.Units, tl.Godowns)
			}
			joined := strings.Join(tl.Warnings, "|")
			if !strings.Contains(joined, "1 item has no HSN") || !strings.Contains(joined, "2 ledgers sit under groups we did not recognise") {
				t.Errorf("warnings: %v", tl.Warnings)
			}
		})
	}
}

func TestTallyPrime3Nested(t *testing.T) {
	tl, err := ParseTally(fixture(t, "masters_prime3_nested.xml"))
	if err != nil {
		t.Fatal(err)
	}
	m := ledgerByName(t, tl, "Mehta Exports")
	if m.Class != ClassCustomer || m.GSTIN != "07AABCS1234D1ZX" || m.StateCode != "07" || m.StateName != "Delhi" || m.Address != "Plot 5, Okhla, New Delhi" || m.OpeningDr != 500000 {
		t.Errorf("latest effective-date entry wins, GSTIN state beats place of supply: %+v", m)
	}
	for name, class := range map[string]string{"Output CGST": ClassTax, "Capital Reserve": ClassEquity, "Office Rent": ClassExpense} {
		if l := ledgerByName(t, tl, name); l.Class != class {
			t.Errorf("%s: %s want %s", name, l.Class, class)
		}
	}
	if c := ledgerByName(t, tl, "Capital Reserve"); c.OpeningCr != 30000000 {
		t.Errorf("%+v", c)
	}
	l := tl.Items[0]
	if l.HSN != "8471" || !l.HasGSTRate || l.GSTRateBP != 1800 || l.QtyMilli != 5000 || l.ValueMinor != 25000000 {
		t.Errorf("%+v", l)
	}
	if tl.Items[1].HSN != "" || !strings.Contains(strings.Join(tl.Warnings, "|"), "1 item has no HSN") {
		t.Errorf("%+v %v", tl.Items[1], tl.Warnings)
	}
}

func TestTallyRejectsAndLimits(t *testing.T) {
	if _, err := ParseTally(fixture(t, "masters_doctype.xml")); !errors.Is(err, ErrDoctype) {
		t.Errorf("DOCTYPE must be rejected: %v", err)
	}
	for name, in := range map[string]string{
		"nothing found": `<ENVELOPE><BODY></BODY></ENVELOPE>`,
		"not xml":       `hello`,
		"truncated":     `<ENVELOPE><TALLYMESSAGE><LEDGER NAME="A"><PARENT>x`,
		"bad entity":    `<ENVELOPE><TALLYMESSAGE><LEDGER NAME="A&nbsp;"/></TALLYMESSAGE></ENVELOPE>`,
		"too deep":      `<ENVELOPE><TALLYMESSAGE><LEDGER NAME="A">` + strings.Repeat("<X>", 200) + strings.Repeat("</X>", 200) + `</LEDGER></TALLYMESSAGE></ENVELOPE>`,
		"deep prefix":   strings.Repeat("<X>", 200) + strings.Repeat("</X>", 200),
		"too big":       strings.Repeat(" ", MaxBytes+1),
	} {
		if _, err := ParseTally([]byte(in)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	if _, err := ParseTally([]byte(`<ENVELOPE><TALLYMESSAGE><LEDGER NAME="A&#4;B"><PARENT>Sundry Debtors</PARENT><OPENINGBALANCE>10 Cr</OPENINGBALANCE></LEDGER></TALLYMESSAGE></ENVELOPE>`)); err != nil {
		t.Errorf("control reference should be scrubbed: %v", err)
	}
}

func TestTallyHelpers(t *testing.T) {
	for in, c := range map[string][2]int64{"-100.00": {10000, 0}, "100.00": {0, 10000}, "100 Dr": {10000, 0}, "100 Cr": {0, 10000}, "": {0, 0}} {
		dr, cr, err := tallyOpening(in)
		if err != nil || dr != c[0] || cr != c[1] {
			t.Errorf("tallyOpening(%q) = %d %d %v", in, dr, cr, err)
		}
	}
	for in, want := range map[string]struct {
		q int64
		u string
	}{"100 Nos": {100000, "Nos"}, " 1,000.500 Kg": {1000500, "Kg"}, "5 Box = 60 Nos": {60000, "Nos"}, "7": {7000, ""}} {
		q, u, err := tallyQty(in)
		if err != nil || q != want.q || u != want.u {
			t.Errorf("tallyQty(%q) = %d %q %v", in, q, u, err)
		}
	}
	if _, _, err := tallyQty("1.23456 Nos"); err == nil {
		t.Error("more than 3 decimals should be unreadable")
	}
}

func TestClassifyTable(t *testing.T) {
	for parent, want := range map[string]string{
		"Sundry Debtors": ClassCustomer, "Sundry Creditors": ClassVendor, "Bank Accounts": ClassBank, "Cash-in-Hand": ClassBank,
		"Duties & Taxes": ClassTax, "Capital Account": ClassEquity, "Reserves & Surplus": ClassEquity,
		"Loans (Liability)": ClassLiability, "Current Liabilities": ClassLiability, "Provisions": ClassLiability, "Suspense A/c": ClassLiability,
		"Secured Loans": ClassLiability, "Fixed Assets": ClassAsset, "Current Assets": ClassAsset, "Investments": ClassAsset, "Deposits (Asset)": ClassAsset,
		"Loans & Advances (Asset)": ClassAsset, "Stock-in-Hand": ClassAsset, "Sales Accounts": ClassRevenue, "Direct Incomes": ClassRevenue,
		"Indirect Incomes": ClassRevenue, "Income (Direct)": ClassRevenue, "Purchase Accounts": ClassExpense, "Direct Expenses": ClassExpense,
		"Indirect Expenses": ClassExpense, "Expenses (Indirect)": ClassExpense,
	} {
		if _, got, guessed := classify(parent, nil); got != want || guessed {
			t.Errorf("classify(%q) = %q guessed=%v; want %q", parent, got, guessed, want)
		}
	}
	// A cycle between unknown groups must terminate.
	g := map[string]*TallyGroup{"a": {Name: "A", Parent: "B"}, "b": {Name: "B", Parent: "A"}}
	if _, c, guessed := classify("A", g); c != ClassLiability || !guessed {
		t.Error(c, guessed)
	}
}
