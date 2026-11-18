package dataimport

import (
	"fmt"
	"net/mail"
	"strings"
)

// Contact is a validated contacts row. Kind is customer, vendor or both.
type Contact struct {
	Name, Kind, Email, Phone, Company string
	GSTIN, PAN, StateCode, StateName  string
	Address                           string
}

// ContactRow is one parsed row. Row is the record number in the file; the row is importable when Errors is empty.
type ContactRow struct {
	Row    int      `json:"row"`
	Errors []string `json:"errors,omitempty"`
	Contact
}

// Item is a validated items row. Quantities are thousandths, money is paise, GSTRateBP is basis points.
type Item struct {
	SKU, Name, HSN, Unit, Location string
	GSTRateBP                      int
	SalePriceMinor                 int64
	OpeningQtyMilli                int64
	OpeningValueMinor              int64
}

type ItemRow struct {
	Row    int      `json:"row"`
	Errors []string `json:"errors,omitempty"`
	Item
}

// OpeningDoc is an unpaid invoice (DocType "invoice", a customer owes us) or bill ("bill", we owe a vendor).
type OpeningDoc struct {
	DocType, Party, Number, Date, DueDate, GSTIN string
	AmountMinor                                  int64
}

type OpeningDocRow struct {
	Row    int      `json:"row"`
	Errors []string `json:"errors,omitempty"`
	OpeningDoc
}

type rowReader struct {
	t   *Table
	m   Mapping
	row []string
	err *[]string
}

func (r rowReader) get(key string) string {
	if i, ok := r.m[key]; ok && i < len(r.row) {
		return strings.TrimSpace(r.row[i])
	}
	return ""
}

func (r rowReader) fail(format string, a ...any) { *r.err = append(*r.err, fmt.Sprintf(format, a...)) }

// text reads a single-line capped text field.
func (r rowReader) text(key, label string, max int) string {
	s := clean(r.get(key))
	if len([]rune(s)) > max {
		r.fail("%s is longer than %d characters", label, max)
		return string([]rune(s)[:max])
	}
	return s
}

var kindWords = map[string]string{
	"customer": "customer", "customers": "customer", "client": "customer", "debtor": "customer", "debtors": "customer", "sundry debtors": "customer", "buyer": "customer",
	"vendor": "vendor", "vendors": "vendor", "supplier": "vendor", "creditor": "vendor", "creditors": "vendor", "sundry creditors": "vendor",
	"both": "both", "customer and vendor": "both", "customer vendor": "both",
}

func checkGSTINAndPAN(r rowReader, gstin, pan, state string) (g, p, code, stateName string) {
	if gstin != "" {
		if v, err := CheckGSTIN(gstin); err != nil {
			r.fail("%v", err)
		} else {
			g = v
		}
	}
	if pan != "" {
		if v, err := CheckPAN(pan); err != nil {
			r.fail("%v", err)
		} else {
			p = v
			if g != "" && g[2:12] != p {
				r.fail("PAN %s does not match the PAN inside the GSTIN (%s)", p, g[2:12])
			}
		}
	}
	if state != "" {
		if c, ok := StateCode(state); ok {
			code = c
		} else {
			r.fail("State %q is not a recognised Indian state or union territory", state)
		}
	}
	if g != "" {
		if code != "" && code != g[:2] {
			n, _ := StateName(g[:2])
			r.fail("State does not match the GSTIN, which is registered in %s", n)
		}
		code = g[:2]
	}
	stateName, _ = StateName(code)
	return
}

// ParseContacts validates every row. defaultKind (customer, vendor, both or "") fills a blank or unmapped Type column.
func ParseContacts(t *Table, m Mapping, defaultKind string) []ContactRow {
	out := make([]ContactRow, 0, len(t.Rows))
	for i, row := range t.Rows {
		cr := ContactRow{Row: t.RowNums[i]}
		r := rowReader{t, m, row, &cr.Errors}
		c := &cr.Contact
		if c.Name = r.text("name", "Name", 200); c.Name == "" {
			r.fail("Name is missing")
		}
		if k := strings.ToLower(clean(r.get("kind"))); k != "" {
			if c.Kind = kindWords[k]; c.Kind == "" {
				r.fail("Type %q is not customer, vendor or both", r.get("kind"))
			}
		} else if defaultKind != "" {
			c.Kind = defaultKind
		} else {
			r.fail("Type is missing (customer, vendor or both)")
		}
		if e := r.get("email"); e != "" {
			if a, err := mail.ParseAddress(e); err != nil || a.Address != e || len(e) > 254 {
				r.fail("Email %q does not look like an email address", e)
			} else {
				c.Email = e
			}
		}
		if p := r.get("phone"); p != "" {
			digits := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, p)
			if strings.Trim(p, "0123456789 +-()./") != "" || len(digits) < 7 || len(digits) > 15 {
				r.fail("Phone %q should have 7 to 15 digits", p)
			} else {
				c.Phone = p
			}
		}
		c.Company = r.text("company", "Company", 200)
		c.Address = r.text("address", "Address", 500)
		c.GSTIN, c.PAN, c.StateCode, c.StateName = checkGSTINAndPAN(r, r.get("gstin"), r.get("pan"), r.get("state"))
		out = append(out, cr)
	}
	return out
}

// ParseItems validates every row. There is no automatic SKU and no default GST rate: a missing one is an error.
func ParseItems(t *Table, m Mapping) []ItemRow {
	out := make([]ItemRow, 0, len(t.Rows))
	seen := map[string]int{}
	for i, row := range t.Rows {
		ir := ItemRow{Row: t.RowNums[i]}
		r := rowReader{t, m, row, &ir.Errors}
		it := &ir.Item
		if it.SKU = strings.ToUpper(clean(r.get("sku"))); it.SKU == "" {
			r.fail("SKU is missing (C Note does not make one up)")
		} else if len(it.SKU) > 64 {
			r.fail("SKU is longer than 64 characters")
		} else if first, dup := seen[it.SKU]; dup {
			r.fail("SKU %s is repeated; it first appears on row %d", it.SKU, first)
		} else {
			seen[it.SKU] = ir.Row
		}
		if it.Name = r.text("name", "Name", 200); it.Name == "" {
			r.fail("Name is missing")
		}
		if h := strings.ReplaceAll(r.get("hsn"), " ", ""); h != "" {
			if err := ValidateHSN(h); err != nil {
				r.fail("HSN/SAC %q: %v", h, err)
			} else {
				it.HSN = h
			}
		}
		if g := r.get("gst_rate"); g == "" {
			r.fail("GST rate is missing; use one of %s", gstRateHelp)
		} else if bp, err := ParseGSTRate(g); err != nil {
			r.fail("%v", err)
		} else {
			it.GSTRateBP = bp
		}
		it.Unit = r.text("unit", "Unit", 20)
		it.Location = r.text("location", "Location", 100)
		if s := r.get("sale_price"); s != "" {
			if v, err := ParseMoney(s); err != nil || v < 0 {
				r.fail("Sale price %q is not a valid amount", s)
			} else {
				it.SalePriceMinor = v
			}
		}
		if q := r.get("opening_qty"); q != "" {
			if v, err := ParseQty(q); err != nil {
				r.fail("Opening stock: %v (up to 3 decimals)", err)
			} else {
				it.OpeningQtyMilli = v
			}
		}
		if s := r.get("opening_value"); s != "" {
			if v, err := ParseMoney(s); err != nil || v < 0 {
				r.fail("Opening stock value %q is not a valid amount", s)
			} else {
				it.OpeningValueMinor = v
			}
		}
		if it.OpeningValueMinor > 0 && it.OpeningQtyMilli == 0 {
			r.fail("Opening stock value is given without an opening stock quantity")
		}
		out = append(out, ir)
	}
	return out
}

var docTypeWords = map[string]string{
	"invoice": "invoice", "sales invoice": "invoice", "sales": "invoice", "receivable": "invoice", "receivables": "invoice", "customer": "invoice",
	"sundry debtors": "invoice", "debtor": "invoice", "dr": "invoice",
	"bill": "bill", "purchase bill": "bill", "purchase": "bill", "payable": "bill", "payables": "bill", "vendor": "bill", "supplier": "bill",
	"sundry creditors": "bill", "creditor": "bill", "cr": "bill",
}

// ParseOpeningDocs validates unpaid invoices and bills. defaultType (invoice, bill or "") fills a blank or unmapped Type column.
// The amount is the outstanding amount; its sign and any Dr/Cr suffix are ignored but it must not be zero.
func ParseOpeningDocs(t *Table, m Mapping, defaultType string) []OpeningDocRow {
	out := make([]OpeningDocRow, 0, len(t.Rows))
	seen := map[string]int{}
	for i, row := range t.Rows {
		dr := OpeningDocRow{Row: t.RowNums[i]}
		r := rowReader{t, m, row, &dr.Errors}
		d := &dr.OpeningDoc
		if k := strings.ToLower(clean(r.get("doc_type"))); k != "" {
			if d.DocType = docTypeWords[k]; d.DocType == "" {
				r.fail("Type %q is not invoice or bill", r.get("doc_type"))
			}
		} else if defaultType != "" {
			d.DocType = defaultType
		} else {
			r.fail("Type is missing (invoice or bill)")
		}
		if d.Party = r.text("party", "Party name", 200); d.Party == "" {
			r.fail("Party name is missing")
		}
		if d.Number = r.text("number", "Document number", 64); d.Number == "" {
			r.fail("Document number is missing")
		} else if d.DocType != "" {
			key := d.DocType + "|" + strings.ToLower(d.Number)
			if first, dup := seen[key]; dup {
				r.fail("Document number %s is repeated; it first appears on row %d", d.Number, first)
			} else {
				seen[key] = dr.Row
			}
		}
		if s := r.get("date"); s == "" {
			r.fail("Date is missing")
		} else if v, err := ParseDate(s); err != nil {
			r.fail("%v", err)
		} else {
			d.Date = v
		}
		if s := r.get("due_date"); s != "" {
			if v, err := ParseDate(s); err != nil {
				r.fail("Due %v", err)
			} else if d.Date != "" && v < d.Date {
				r.fail("Due date is before the document date")
			} else {
				d.DueDate = v
			}
		}
		if s := r.get("amount"); s == "" {
			r.fail("Amount is missing")
		} else if mag, _, _, err := parseAmount(s); err != nil {
			r.fail("Amount %q %v", s, err)
		} else if mag == 0 {
			r.fail("Amount must be more than zero")
		} else {
			d.AmountMinor = mag
		}
		if g := r.get("gstin"); g != "" {
			if v, err := CheckGSTIN(g); err != nil {
				r.fail("%v", err)
			} else {
				d.GSTIN = v
			}
		}
		out = append(out, dr)
	}
	return out
}
