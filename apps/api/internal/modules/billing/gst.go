// Package billing ports GST invoicing, the double-entry ledger and finance reports from
// crates/cnote-finance (billing.rs, ledger.rs, gst.rs, irn.rs, reports.rs). Money is int64 paise.
package billing

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

var states = []struct{ code, name string }{
	{"01", "Jammu and Kashmir"}, {"02", "Himachal Pradesh"}, {"03", "Punjab"}, {"04", "Chandigarh"},
	{"05", "Uttarakhand"}, {"06", "Haryana"}, {"07", "Delhi"}, {"08", "Rajasthan"}, {"09", "Uttar Pradesh"},
	{"10", "Bihar"}, {"11", "Sikkim"}, {"12", "Arunachal Pradesh"}, {"13", "Nagaland"}, {"14", "Manipur"},
	{"15", "Mizoram"}, {"16", "Tripura"}, {"17", "Meghalaya"}, {"18", "Assam"}, {"19", "West Bengal"},
	{"20", "Jharkhand"}, {"21", "Odisha"}, {"22", "Chhattisgarh"}, {"23", "Madhya Pradesh"}, {"24", "Gujarat"},
	{"25", "Daman and Diu"}, {"26", "Dadra and Nagar Haveli and Daman and Diu"}, {"27", "Maharashtra"},
	{"28", "Andhra Pradesh (before division)"}, {"29", "Karnataka"}, {"30", "Goa"}, {"31", "Lakshadweep"},
	{"32", "Kerala"}, {"33", "Tamil Nadu"}, {"34", "Puducherry"}, {"35", "Andaman and Nicobar Islands"},
	{"36", "Telangana"}, {"37", "Andhra Pradesh"}, {"38", "Ladakh"}, {"97", "Other Territory"}, {"99", "Centre Jurisdiction"},
}

func stateName(code string) (string, bool) {
	for _, s := range states {
		if s.code == code {
			return s.name, true
		}
	}
	return "", false
}

// gstErr carries the Rust GstError Display text.
type gstErr struct{ msg string }

func (e *gstErr) Error() string { return e.msg }

var (
	errGstinLength   = &gstErr{"GSTIN must be 15 characters"}
	errGstinFormat   = &gstErr{"GSTIN format is invalid"}
	errGstinChecksum = &gstErr{"GSTIN checksum does not match"}
	errQuantity      = &gstErr{"quantity must be greater than zero"}
	errPrice         = &gstErr{"unit price cannot be negative"}
	errRate          = &gstErr{"GST rate must be between 0 and 28% (basis points 0-2800)"}
	errHsn           = &gstErr{"HSN/SAC must be 4, 6 or 8 digits"}
	errOverflow      = &gstErr{"amount out of range"}
)

const b36 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func gstinCheckChar(first14 string) byte {
	sum := 0
	for i := 0; i < len(first14); i++ {
		v := strings.IndexByte(b36, first14[i])
		if v < 0 {
			v = 0
		}
		if i%2 == 1 {
			v *= 2
		}
		sum += v/36 + v%36
	}
	return b36[(36-sum%36)%36]
}

// Rust's to_uppercase uses full case mapping; Go's lacks these multi-letter results.
var fullUpper = strings.NewReplacer("ß", "SS", "ﬀ", "FF", "ﬁ", "FI", "ﬂ", "FL", "ﬃ", "FFI", "ﬄ", "FFL", "ﬅ", "ST", "ﬆ", "ST")

func upper(s string) string { return strings.ToUpper(fullUpper.Replace(s)) }

func completeGSTIN(first14 string) string {
	p := upper(first14)
	return p + string(gstinCheckChar(p))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isAlnum(c byte) bool { return isDigit(c) || isUpper(c) || (c >= 'a' && c <= 'z') }

func validateGSTIN(s string) (string, error) {
	g := upper(strings.TrimSpace(s))
	if len(g) != 15 {
		return "", errGstinLength
	}
	ok := isDigit(g[0]) && isDigit(g[1])
	for i := 2; i < 7; i++ {
		ok = ok && isUpper(g[i])
	}
	for i := 7; i < 11; i++ {
		ok = ok && isDigit(g[i])
	}
	ok = ok && isUpper(g[11]) && g[12] != '0' && isAlnum(g[12]) && g[13] == 'Z' && isAlnum(g[14])
	if !ok {
		return "", errGstinFormat
	}
	if _, found := stateName(g[:2]); !found {
		return "", &gstErr{"GSTIN has unknown state code " + g[:2]}
	}
	if gstinCheckChar(g[:14]) != g[14] {
		return "", errGstinChecksum
	}
	return g, nil
}

func gstinState(g string) string {
	if len(g) < 2 {
		return ""
	}
	return g[:2]
}

func validateState(code string) error {
	if _, ok := stateName(code); !ok {
		return &gstErr{"unknown GST state code " + code}
	}
	return nil
}

func validateHSN(h string) error {
	if n := len(h); n == 4 || n == 6 || n == 8 {
		for i := 0; i < n; i++ {
			if !isDigit(h[i]) {
				return errHsn
			}
		}
		return nil
	}
	return errHsn
}

func financialYear(d time.Time) string {
	start := d.Year()
	if d.Month() < 4 {
		start--
	}
	return fmt.Sprintf("%d-%02d", start, (start+1)%100)
}

// dec is a non-negative-scale decimal: value = unscaled / 10^scale.
type dec struct {
	unscaled *big.Int
	scale    int
}

func parseDec(s string) (dec, bool) {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	scale := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		scale = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	u, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return dec{}, false
	}
	if neg {
		u.Neg(u)
	}
	return dec{u, scale}, true
}

var maxI64 = big.NewInt(1<<63 - 1)

// taxablePaise is qty x unit price rounded half up (inputs are non-negative).
func taxablePaise(q dec, price int64) (int64, error) {
	if q.unscaled.Sign() <= 0 {
		return 0, errQuantity
	}
	if price < 0 {
		return 0, errPrice
	}
	p := new(big.Int).Mul(q.unscaled, big.NewInt(price))
	d := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(q.scale)), nil)
	half := new(big.Int).Div(d, big.NewInt(2))
	// midpoint away from zero: (p + d/2) / d for p >= 0; d/2 is exact because d is 1 or a multiple of 10
	r := new(big.Int).Div(new(big.Int).Add(p, half), d)
	if r.Cmp(maxI64) > 0 {
		return 0, errOverflow
	}
	return r.Int64(), nil
}

func taxPaise(taxable int64, rateBP uint32) (int64, error) {
	x := new(big.Int).Mul(big.NewInt(taxable), big.NewInt(int64(rateBP)))
	x.Add(x, big.NewInt(5000)).Div(x, big.NewInt(10000))
	if x.Cmp(maxI64) > 0 {
		return 0, errOverflow
	}
	return x.Int64(), nil
}

type supply int

const (
	intra supply = iota // CGST + SGST
	inter               // IGST
)

func supplyKind(supplier, place string) (supply, error) {
	if err := validateState(supplier); err != nil {
		return 0, err
	}
	if err := validateState(place); err != nil {
		return 0, err
	}
	if supplier == place {
		return intra, nil
	}
	return inter, nil
}

type gstLine struct {
	qty     dec
	price   int64
	rateBP  uint32
	hsn     *string
	qtyText string
}

type lineTax struct{ taxable, cgst, sgst, igst int64 }

// lineTaxOf: the tax is rounded once, then split; CGST gets the floor half and SGST the remainder.
func lineTaxOf(taxable int64, rateBP uint32, s supply, reverse bool) (lineTax, error) {
	if rateBP > 2800 {
		return lineTax{}, errRate
	}
	t := lineTax{taxable: taxable}
	if reverse {
		return t, nil
	}
	tax, err := taxPaise(taxable, rateBP)
	if err != nil {
		return t, err
	}
	if s == intra {
		t.cgst, t.sgst = tax/2, tax-tax/2
	} else {
		t.igst = tax
	}
	return t, nil
}

type invoiceTax struct {
	lines                             []lineTax
	supply                            supply
	reverse                           bool
	subtotal, cgst, sgst, igst, total int64
}

func addChecked(acc *int64, v int64) error {
	if (v > 0 && *acc > 1<<63-1-v) || (v < 0 && *acc < -1<<63-v) {
		return errOverflow
	}
	*acc += v
	return nil
}

func computeInvoice(lines []gstLine, supplier, place string, reverse bool) (invoiceTax, error) {
	s, err := supplyKind(supplier, place)
	if err != nil {
		return invoiceTax{}, err
	}
	out := invoiceTax{lines: make([]lineTax, 0, len(lines)), supply: s, reverse: reverse}
	for _, l := range lines {
		if l.hsn != nil {
			if err := validateHSN(*l.hsn); err != nil {
				return out, err
			}
		}
		tx, err := taxablePaise(l.qty, l.price)
		if err != nil {
			return out, err
		}
		t, err := lineTaxOf(tx, l.rateBP, s, reverse)
		if err != nil {
			return out, err
		}
		for _, a := range []struct {
			acc *int64
			v   int64
		}{{&out.subtotal, t.taxable}, {&out.cgst, t.cgst}, {&out.sgst, t.sgst}, {&out.igst, t.igst}} {
			if err := addChecked(a.acc, a.v); err != nil {
				return out, err
			}
		}
		out.lines = append(out.lines, t)
	}
	out.total = out.subtotal + out.cgst + out.sgst + out.igst
	return out, nil
}

// CompleteGSTIN appends the check character to a 14-character GSTIN prefix (test fixtures).
func CompleteGSTIN(first14 string) string { return completeGSTIN(first14) }
