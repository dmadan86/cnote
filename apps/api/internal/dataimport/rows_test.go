package dataimport

import (
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string, k Kind) (*Table, Mapping) {
	t.Helper()
	tb, err := ReadCSV(fixture(t, name), k)
	if err != nil {
		t.Fatal(err)
	}
	return tb, AutoMap(k, tb.Headers)
}

func hasErr(errs []string, frag string) bool {
	for _, e := range errs {
		if strings.Contains(e, frag) {
			return true
		}
	}
	return false
}

func TestParseContactsFixture(t *testing.T) {
	tb, m := readFixture(t, "contacts_tally.csv", KindContacts)
	rows := ParseContacts(tb, m, "")
	if len(rows) != 4 {
		t.Fatal(len(rows))
	}
	a := rows[0]
	if len(a.Errors) != 0 || a.Name != "Sharma Traders" || a.Kind != "customer" || a.GSTIN != "27AAPFU0939F1ZV" || a.StateCode != "27" ||
		a.StateName != "Maharashtra" || a.Phone != "98765 43210" || a.Address != "12 MG Road Pune 411001" || a.Row != 3 {
		t.Errorf("%+v", a)
	}
	if b := rows[1]; len(b.Errors) != 0 || b.Kind != "vendor" || b.StateCode != "29" {
		t.Errorf("%+v", b)
	}
	if c := rows[2]; len(c.Errors) != 0 || c.StateCode != "21" || c.StateName != "Odisha" {
		t.Errorf("Orissa should map to Odisha: %+v", c)
	}
	if d := rows[3]; !hasErr(d.Errors, "check character") {
		t.Errorf("%+v", d)
	}
}

func TestParseContactsRules(t *testing.T) {
	tb := &Table{Headers: []string{"n", "k", "e", "p", "g", "pan", "s"}}
	add := func(r ...string) { tb.Rows = append(tb.Rows, r); tb.RowNums = append(tb.RowNums, len(tb.Rows)+1) }
	m := Mapping{"name": 0, "kind": 1, "email": 2, "phone": 3, "gstin": 4, "pan": 5, "state": 6}
	for _, c := range []struct {
		row  []string
		want string // "" means no errors
	}{
		{[]string{"A", "client", "a@b.in", "+91 98765-43210", "", "", ""}, ""},
		{[]string{"", "customer", "", "", "", "", ""}, "Name is missing"},
		{[]string{"A", "", "", "", "", "", ""}, "Type is missing"},
		{[]string{"A", "friend", "", "", "", "", ""}, "not customer, vendor or both"},
		{[]string{"A", "both", "not-an-email", "", "", "", ""}, "Email"},
		{[]string{"A", "both", "", "12", "", "", ""}, "Phone"},
		{[]string{"A", "both", "", "call me", "", "", ""}, "Phone"},
		{[]string{"A", "both", "", "", "", "ABCDE123", ""}, "PAN must be"},
		{[]string{"A", "both", "", "", "27AAPFU0939F1ZV", "ABCDE1234F", ""}, "does not match the PAN inside the GSTIN"},
		{[]string{"A", "both", "", "", "27AAPFU0939F1ZV", "aapfu0939f", "Maharashtra"}, ""},
		{[]string{"A", "both", "", "", "27AAPFU0939F1ZV", "", "Karnataka"}, "does not match the GSTIN"},
		{[]string{"A", "both", "", "", "", "", "Narnia"}, "not a recognised"},
		{[]string{"A", "both", "", "", "27AAPFU0939F1ZX", "", ""}, "check character"},
	} {
		tb.Rows, tb.RowNums = nil, nil
		add(c.row...)
		got := ParseContacts(tb, m, "")[0].Errors
		if (c.want == "") != (len(got) == 0) || (c.want != "" && !hasErr(got, c.want)) {
			t.Errorf("%v: errors %v, want %q", c.row, got, c.want)
		}
	}
	tb.Rows, tb.RowNums = nil, nil
	add("A", "", "", "", "29ABCDE1234F1ZW", "", "")
	r := ParseContacts(tb, m, "vendor")[0]
	if len(r.Errors) != 0 || r.Kind != "vendor" || r.StateCode != "29" || r.PAN != "" {
		t.Errorf("default kind / state from GSTIN: %+v", r)
	}
}

func TestParseItemsFixture(t *testing.T) {
	tb, m := readFixture(t, "items_vyapar.csv", KindItems)
	if tb.Delimiter != ';' {
		t.Fatal("semicolon not sniffed")
	}
	rows := ParseItems(tb, m)
	a := rows[0]
	if len(a.Errors) != 0 || a.SKU != "WID-10" || a.HSN != "7318" || a.GSTRateBP != 1800 || a.SalePriceMinor != 25000 || a.OpeningQtyMilli != 100000 || a.OpeningValueMinor != 2000000 {
		t.Errorf("%+v", a)
	}
	if b := rows[1]; len(b.Errors) != 0 || b.GSTRateBP != 500 || b.SalePriceMinor != 125050 || b.OpeningQtyMilli != 12500 {
		t.Errorf("%+v", b)
	}
	if !hasErr(rows[2].Errors, "SKU is missing") {
		t.Errorf("no automatic SKU: %+v", rows[2])
	}
	if !hasErr(rows[3].Errors, "GST rate") {
		t.Errorf("%+v", rows[3])
	}
	if !hasErr(rows[4].Errors, "HSN/SAC") {
		t.Errorf("%+v", rows[4])
	}
}

func TestParseItemsRules(t *testing.T) {
	m := Mapping{"sku": 0, "name": 1, "gst_rate": 2, "opening_qty": 3, "opening_value": 4, "sale_price": 5}
	tb := &Table{Rows: [][]string{
		{"a-1", "One", "", "", "", ""},
		{"A-1", "Dup", "18", "", "", ""},
		{"B", "Qty", "18", "1.2345", "", ""},
		{"C", "ValueNoQty", "18", "", "100", ""},
		{"D", "Neg price", "18", "", "", "-5"},
		{"E", "", "18", "", "", ""},
		{"F", "Fine", "0", "0.001", "5", "0"},
	}}
	for i := range tb.Rows {
		tb.RowNums = append(tb.RowNums, i+2)
	}
	rows := ParseItems(tb, m)
	for i, frag := range []string{"GST rate is missing", "repeated; it first appears on row 2", "Opening stock", "without an opening stock quantity", "Sale price", "Name is missing", ""} {
		if (frag == "") != (len(rows[i].Errors) == 0) || (frag != "" && !hasErr(rows[i].Errors, frag)) {
			t.Errorf("row %d: %v want %q", i, rows[i].Errors, frag)
		}
	}
	if rows[0].SKU != "A-1" {
		t.Error("SKU should be upper-cased for case-insensitive duplicate detection:", rows[0].SKU)
	}
}

func TestParseOpeningDocsFixture(t *testing.T) {
	tb, m := readFixture(t, "opening_docs_tally.csv", KindOpeningDocs)
	rows := ParseOpeningDocs(tb, m, "invoice")
	a := rows[0]
	if len(a.Errors) != 0 || a.Date != "2025-04-01" || a.DueDate != "2025-04-30" || a.AmountMinor != 11800000 || a.Number != "INV-001" || a.DocType != "invoice" {
		t.Errorf("%+v", a)
	}
	if b := rows[1]; len(b.Errors) != 0 || b.Date != "2025-03-15" || b.DueDate != "2025-04-14" || b.AmountMinor != 118000 {
		t.Errorf("%+v", b)
	}
	if !hasErr(rows[2].Errors, "not a valid date") {
		t.Errorf("%+v", rows[2])
	}
	if r := ParseOpeningDocs(tb, m, "")[0]; !hasErr(r.Errors, "Type is missing") {
		t.Errorf("%+v", r)
	}
}

func TestParseOpeningDocsRules(t *testing.T) {
	m := Mapping{"doc_type": 0, "party": 1, "number": 2, "date": 3, "due_date": 4, "amount": 5, "gstin": 6}
	tb := &Table{Rows: [][]string{
		{"Bill", "V", "B-1", "01-04-2025", "", "500 Cr", ""},
		{"bill", "V", "b-1", "01-04-2025", "", "500", ""},
		{"invoice", "V", "B-1", "01-04-2025", "31-03-2025", "500", ""},
		{"invoice", "", "", "", "", "0", "bad"},
		{"gift", "V", "X", "01-04-2025", "", "1", ""},
	}}
	for i := range tb.Rows {
		tb.RowNums = append(tb.RowNums, i+2)
	}
	rows := ParseOpeningDocs(tb, m, "")
	if len(rows[0].Errors) != 0 || rows[0].AmountMinor != 50000 || rows[0].DocType != "bill" {
		t.Errorf("%+v", rows[0])
	}
	for i, frag := range map[int]string{1: "repeated", 2: "Due date is before", 4: "not invoice or bill"} {
		if !hasErr(rows[i].Errors, frag) {
			t.Errorf("row %d: %v want %q", i, rows[i].Errors, frag)
		}
	}
	for _, frag := range []string{"Party name is missing", "Document number is missing", "Date is missing", "more than zero", "GSTIN"} {
		if !hasErr(rows[3].Errors, frag) {
			t.Errorf("row 3 missing %q: %v", frag, rows[3].Errors)
		}
	}
}
