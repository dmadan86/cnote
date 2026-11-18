package dataimport

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/text/encoding/unicode"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAutoMapTallyAndVyaparHeaders(t *testing.T) {
	for _, c := range []struct {
		kind    Kind
		headers []string
		want    map[string]int
	}{
		{KindContacts, []string{"Party Name", "Under", "GSTIN/UIN", "State", "Mobile No", "Address"}, map[string]int{"name": 0, "kind": 1, "gstin": 2, "state": 3, "phone": 4, "address": 5}},
		{KindContacts, []string{"\xef\xbb\xbfCustomer Name", "Email ID", "Company Name", "PAN No."}, map[string]int{"name": 0, "email": 1, "company": 2, "pan": 3}},
		{KindItems, []string{"Name of Item", "HSN/SAC", "Rate of Tax", "UOM", "Rate", "Qty", "Value"}, map[string]int{"name": 0, "hsn": 1, "gst_rate": 2, "unit": 3, "sale_price": 4, "opening_qty": 5, "opening_value": 6}},
		{KindItems, []string{"Item Code", "Item Name", "GST %", "Selling Price"}, map[string]int{"sku": 0, "name": 1, "gst_rate": 2, "sale_price": 3}},
		{KindOpeningDocs, []string{"Bill No", "Bill Date", "Party Name", "Closing Balance", "Due on"}, map[string]int{"number": 0, "date": 1, "party": 2, "amount": 3, "due_date": 4}},
		{KindOpeningDocs, []string{"Date", "Ref. No.", "Party Name", "Pending Amount", "Due Date"}, map[string]int{"date": 0, "number": 1, "party": 2, "amount": 3, "due_date": 4}},
	} {
		got := AutoMap(c.kind, c.headers)
		if fmt.Sprint(got) != fmt.Sprint(Mapping(c.want)) {
			t.Errorf("%s %v\n got %v\nwant %v", c.kind, c.headers, got, c.want)
		}
	}
}

func TestSampleCSVRoundTrips(t *testing.T) {
	for _, k := range []Kind{KindContacts, KindItems, KindOpeningDocs} {
		s := SampleCSV(k)
		tb, err := ReadCSV([]byte(s), k)
		if err != nil || len(tb.Rows) != 1 || tb.HeaderRow != 1 {
			t.Fatalf("%s: %v %+v", k, err, tb)
		}
		m := AutoMap(k, tb.Headers)
		if len(m) != len(Fields(k)) || len(MissingRequired(k, m)) != 0 {
			t.Errorf("%s: sample headers not all auto-mapped: %v", k, m)
		}
		var errs [][]string
		switch k {
		case KindContacts:
			for _, r := range ParseContacts(tb, m, "") {
				errs = append(errs, r.Errors)
			}
		case KindItems:
			for _, r := range ParseItems(tb, m) {
				errs = append(errs, r.Errors)
			}
		default:
			for _, r := range ParseOpeningDocs(tb, m, "") {
				errs = append(errs, r.Errors)
			}
		}
		if len(errs) != 1 || len(errs[0]) != 0 {
			t.Errorf("%s sample row has errors: %v", k, errs)
		}
	}
}

func TestReadCSV(t *testing.T) {
	t.Run("BOM preamble header detect and multiline cell", func(t *testing.T) {
		tb, err := ReadCSV(fixture(t, "contacts_tally.csv"), KindContacts)
		if err != nil {
			t.Fatal(err)
		}
		if tb.HeaderRow != 2 || tb.Headers[0] != "Party Name" || len(tb.Rows) != 4 || tb.RowNums[0] != 3 {
			t.Fatalf("%+v", tb)
		}
		if !strings.Contains(tb.Rows[0][5], "Pune") {
			t.Error("quoted multi-line address lost:", tb.Rows[0])
		}
		cols := tb.Columns(AutoMap(KindContacts, tb.Headers))
		if cols[0].Field != "name" || len(cols[0].Samples) != 3 || cols[2].Samples[0] != "27AAPFU0939F1ZV" {
			t.Errorf("%+v", cols[:3])
		}
	})
	t.Run("delimiters", func(t *testing.T) {
		for d, s := range map[rune]string{';': "Name;Type\nA;customer\n", '\t': "Name\tType\nA\tcustomer\n", '|': "Name|Type\nA|customer\n", ',': "Name,Type\r\nA,customer\r\n"} {
			tb, err := ReadCSV([]byte(s), KindContacts)
			if err != nil || tb.Delimiter != d || len(tb.Rows) != 1 || tb.Rows[0][1] != "customer" {
				t.Errorf("%q: %v %+v", d, err, tb)
			}
		}
	})
	t.Run("utf16", func(t *testing.T) {
		enc, _ := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte("Name,Type\nA,vendor\n"))
		tb, err := ReadCSV(enc, KindContacts)
		if err != nil || tb.Rows[0][1] != "vendor" {
			t.Fatal(err, tb)
		}
	})
	t.Run("blank rows dropped, no header match falls back to row 1", func(t *testing.T) {
		tb, err := ReadCSV([]byte("foo,bar\n\n,\n1,2\n"), KindItems)
		if err != nil || tb.HeaderRow != 1 || len(tb.Rows) != 1 {
			t.Fatal(err, tb)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if _, err := ReadCSV(nil, KindItems); err == nil {
			t.Error("empty file should fail")
		}
	})
	t.Run("caps", func(t *testing.T) {
		if _, err := ReadCSV(bytes.Repeat([]byte("a"), MaxBytes+1), KindItems); !errors.Is(err, ErrTooLarge) {
			t.Error(err)
		}
		mk := func(n int) []byte { return []byte("Name,Type\n" + strings.Repeat("A,customer\n", n)) }
		if tb, err := ReadCSV(mk(MaxRows), KindContacts); err != nil || len(tb.Rows) != MaxRows {
			t.Error("5000 rows should pass", err)
		}
		if _, err := ReadCSV(mk(MaxRows+1), KindContacts); !errors.Is(err, ErrTooManyRows) {
			t.Error(err)
		}
	})
}

func TestMissingRequired(t *testing.T) {
	got := MissingRequired(KindItems, Mapping{"name": 0})
	if fmt.Sprint(got) != "[SKU GST rate]" {
		t.Error(got)
	}
}
