package dataimport

import (
	"encoding/csv"
	"strings"
)

// Field is one importable column.
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Required bool     `json:"required"`
	Example  string   `json:"example"`
	words    []string // normalised header keywords, most specific first
}

// Mapping assigns a field key to the index of the file column that holds it. Unmapped fields are absent.
type Mapping map[string]int

var catalog = map[Kind][]Field{
	KindContacts: {
		{Key: "name", Label: "Name", Required: true, Example: "Sharma Traders", words: []string{"party name", "name of party", "ledger name", "customer name", "vendor name", "supplier name", "display name", "contact name", "full name", "name", "party", "ledger"}},
		{Key: "kind", Label: "Type", Example: "customer", words: []string{"type", "party type", "contact type", "kind", "category", "under", "group", "parent"}},
		{Key: "email", Label: "Email", Example: "accounts@sharmatraders.example", words: []string{"email", "e mail", "email id", "email address", "mail"}},
		{Key: "phone", Label: "Phone", Example: "9876543210", words: []string{"phone", "mobile", "mobile no", "phone no", "contact no", "telephone", "contact number", "mobile number", "phone number"}},
		{Key: "company", Label: "Company", Example: "Sharma Traders Pvt Ltd", words: []string{"company", "company name", "organization", "organisation", "business name"}},
		{Key: "gstin", Label: "GSTIN", Example: "27AAPFU0939F1ZV", words: []string{"gstin", "gstin uin", "gst no", "gst number", "gstin number", "gst identification number", "gst in", "gstno", "gst"}},
		{Key: "pan", Label: "PAN", Example: "AAPFU0939F", words: []string{"pan", "pan no", "pan number", "income tax no", "income tax number", "it pan"}},
		{Key: "state", Label: "State", Example: "Maharashtra", words: []string{"state", "state name", "place of supply", "state code"}},
		{Key: "address", Label: "Billing address", Example: "12 MG Road, Pune 411001", words: []string{"billing address", "address", "mailing address", "address line 1", "address 1", "street"}},
	},
	KindItems: {
		{Key: "sku", Label: "SKU", Required: true, Example: "WIDGET-01", words: []string{"sku", "sku code", "item code", "item no", "product code", "code", "part no", "part number", "item id"}},
		{Key: "name", Label: "Name", Required: true, Example: "Steel widget 10mm", words: []string{"name of item", "item name", "product name", "stock item", "name", "description", "item", "particulars"}},
		{Key: "hsn", Label: "HSN/SAC", Example: "7318", words: []string{"hsn sac", "hsn sac code", "hsn code", "sac code", "hsn", "sac"}},
		{Key: "gst_rate", Label: "GST rate", Required: true, Example: "18", words: []string{"gst rate", "rate of tax", "tax rate", "igst rate", "rate of gst", "gst percent", "gst", "tax percent"}},
		{Key: "unit", Label: "Unit", Example: "Nos", words: []string{"unit", "units", "uom", "unit of measure", "unit of measurement", "base units", "primary unit"}},
		{Key: "sale_price", Label: "Sale price", Example: "250.00", words: []string{"sale price", "sales price", "selling price", "sale rate", "selling rate", "unit price", "mrp", "price", "rate"}},
		{Key: "opening_qty", Label: "Opening stock", Example: "100", words: []string{"opening stock", "opening qty", "opening quantity", "closing quantity", "closing qty", "closing stock", "stock quantity", "current stock", "quantity", "qty", "stock"}},
		{Key: "opening_value", Label: "Opening stock value", Example: "20000.00", words: []string{"opening stock value", "opening value", "opening amount", "closing value", "stock value", "inventory value", "value"}},
		{Key: "location", Label: "Location", Example: "Main warehouse", words: []string{"location", "godown", "warehouse", "store", "branch"}},
	},
	KindOpeningDocs: {
		{Key: "doc_type", Label: "Type", Example: "invoice", words: []string{"type", "bill type", "doc type", "document type", "party type", "kind", "side"}},
		{Key: "party", Label: "Party name", Required: true, Example: "Sharma Traders", words: []string{"party name", "name of party", "customer name", "vendor name", "supplier name", "ledger name", "party", "particulars", "customer", "vendor", "name"}},
		{Key: "number", Label: "Document number", Required: true, Example: "INV-0042", words: []string{"document number", "bill no", "invoice no", "invoice number", "bill number", "ref no", "reference no", "document no", "doc no", "voucher no", "bill ref", "bill reference", "number"}},
		{Key: "date", Label: "Date", Required: true, Example: "15-03-2026", words: []string{"bill date", "invoice date", "document date", "doc date", "ref date", "dated", "date"}},
		{Key: "due_date", Label: "Due date", Example: "14-04-2026", words: []string{"due date", "due on", "due by", "payment due date", "due"}},
		{Key: "amount", Label: "Amount", Required: true, Example: "1,18,000.00", words: []string{"pending amount", "outstanding amount", "balance due", "amount due", "bill amount", "invoice amount", "closing balance", "opening amount", "outstanding", "amount", "balance", "pending", "grand total", "total"}},
		{Key: "gstin", Label: "GSTIN", Example: "27AAPFU0939F1ZV", words: []string{"gstin", "gstin uin", "gst no", "gst number"}},
	},
}

// Fields returns the field catalog for kind, in column order.
func Fields(kind Kind) []Field { return catalog[kind] }

// Label returns the display label of a field key.
func Label(kind Kind, key string) string {
	for _, f := range catalog[kind] {
		if f.Key == key {
			return f.Label
		}
	}
	return key
}

// SampleCSV is a two-line CSV (labels and an example row) that AutoMap and the parsers accept without errors.
func SampleCSV(kind Kind) string {
	var h, e []string
	for _, f := range catalog[kind] {
		h, e = append(h, f.Label), append(e, f.Example)
	}
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write(h)
	_ = w.Write(e)
	w.Flush()
	return sb.String()
}

func normHeader(s string) string {
	s = strings.TrimPrefix(s, string(rune(0xFEFF)))
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 && r != '₹' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return ' '
	}, s)), " ")
}

// AutoMap matches headers to fields by keyword: exact header matches first, then whole-word containment, each header used once.
func AutoMap(kind Kind, headers []string) Mapping {
	m := Mapping{}
	used := map[int]bool{}
	norm := make([]string, len(headers))
	for i, h := range headers {
		norm[i] = normHeader(h)
	}
	for pass := 0; pass < 2; pass++ {
		for _, f := range catalog[kind] {
			if _, done := m[f.Key]; done {
				continue
			}
		search:
			for _, w := range f.words {
				for i, h := range norm {
					if used[i] || h == "" {
						continue
					}
					if pass == 0 && h == w || pass == 1 && strings.Contains(" "+h+" ", " "+w+" ") {
						m[f.Key], used[i] = i, true
						break search
					}
				}
			}
		}
	}
	return m
}

// MissingRequired lists the labels of required fields that mapping m leaves unmapped.
func MissingRequired(kind Kind, m Mapping) []string {
	var out []string
	for _, f := range catalog[kind] {
		if _, ok := m[f.Key]; f.Required && !ok {
			out = append(out, f.Label)
		}
	}
	return out
}

// CSVSafe neutralises spreadsheet formula injection: cells starting with = + - @ tab or CR get a leading '.
func CSVSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// SafeCSV renders rows as CSV with every cell passed through CSVSafe (for the "rows that need fixing" download).
func SafeCSV(rows [][]string) string {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	for _, r := range rows {
		out := make([]string, len(r))
		for i, c := range r {
			out[i] = CSVSafe(c)
		}
		_ = w.Write(out)
	}
	w.Flush()
	return sb.String()
}
