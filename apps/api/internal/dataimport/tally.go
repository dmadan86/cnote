package dataimport

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const maxXMLDepth = 64

// Ledger classes. Sundry Debtors are customers, Sundry Creditors vendors; the rest are accounts.
const (
	ClassCustomer  = "customer"
	ClassVendor    = "vendor"
	ClassBank      = "bank"
	ClassTax       = "tax"
	ClassEquity    = "equity"
	ClassLiability = "liability"
	ClassAsset     = "asset"
	ClassRevenue   = "revenue"
	ClassExpense   = "expense"
)

// TallyLedger is one ledger. OpeningDr / OpeningCr are paise; at most one is non-zero.
type TallyLedger struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	PrimaryGroup string `json:"primary_group"`
	Class        string `json:"class"`
	OpeningDr    int64  `json:"opening_dr"`
	OpeningCr    int64  `json:"opening_cr"`
	GSTIN        string `json:"gstin,omitempty"`
	PAN          string `json:"pan,omitempty"`
	StateName    string `json:"state_name,omitempty"`
	StateCode    string `json:"state_code,omitempty"`
	Address      string `json:"address,omitempty"`
	Email        string `json:"email,omitempty"`
	Phone        string `json:"phone,omitempty"`
}

type TallyGroup struct {
	Name         string `json:"name"`
	Parent       string `json:"parent"`
	IsRevenue    bool   `json:"is_revenue"`
	IsDeemedPos  bool   `json:"is_deemed_positive"`
	PrimaryGroup string `json:"primary_group"`
}

// TallyItem is a stock item. Qty is thousandths, money is paise; GSTRateBP is valid only when HasGSTRate.
type TallyItem struct {
	Name       string `json:"name"`
	Parent     string `json:"parent"`
	BaseUnit   string `json:"base_unit"`
	QtyMilli   int64  `json:"qty_milli"`
	RateMinor  int64  `json:"rate_minor"`
	ValueMinor int64  `json:"value_minor"`
	HSN        string `json:"hsn,omitempty"`
	GSTRateBP  int    `json:"gst_rate_bp"`
	HasGSTRate bool   `json:"has_gst_rate"`
}

type TallyGodown struct {
	Name   string `json:"name"`
	Parent string `json:"parent,omitempty"`
}

// Found is the "what we found" summary. Ledgers counts non-party ledgers (accounts).
type Found struct {
	Customers int `json:"customers"`
	Vendors   int `json:"vendors"`
	Ledgers   int `json:"ledgers"`
	Items     int `json:"items"`
	Units     int `json:"units"`
	Godowns   int `json:"godowns"`
	Groups    int `json:"groups"`
}

type Tally struct {
	Ledgers  []TallyLedger `json:"ledgers"`
	Groups   []TallyGroup  `json:"groups"`
	Items    []TallyItem   `json:"items"`
	Units    []string      `json:"units"`
	Godowns  []TallyGodown `json:"godowns"`
	Found    Found         `json:"found"`
	Warnings []string      `json:"warnings"`
}

var (
	ErrDoctype   = errors.New("this XML contains a DOCTYPE/DTD declaration, which C Note does not accept; export the masters again from Tally")
	ErrNoMasters = errors.New("no ledgers, stock items, units or godowns were found; export Masters (All Masters) from Tally as XML")
	numRefRe     = regexp.MustCompile(`&#(?:[xX]([0-9a-fA-F]{1,6})|([0-9]{1,7}));`)
)

// scrub drops numeric character references to control characters (Tally writes &#4; for its "Primary" marker) and raw control bytes.
func scrub(b []byte) []byte {
	b = numRefRe.ReplaceAllFunc(b, func(m []byte) []byte {
		g := numRefRe.FindSubmatch(m)
		var n int64
		if len(g[1]) > 0 {
			n, _ = strconv.ParseInt(string(g[1]), 16, 32)
		} else {
			n, _ = strconv.ParseInt(string(g[2]), 10, 32)
		}
		if n < 32 && n != 9 && n != 10 && n != 13 || n == 0xFFFE || n == 0xFFFF {
			return nil
		}
		return m
	})
	return bytes.Map(func(r rune) rune {
		if r < 32 && r != 9 && r != 10 && r != 13 {
			return -1
		}
		return r
	}, b)
}

type node struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*node
}

func decodeNode(d *xml.Decoder, se xml.StartElement, depth int) (*node, error) {
	if depth > maxXMLDepth {
		return nil, errors.New("the XML is nested too deeply")
	}
	n := &node{name: se.Name.Local}
	if len(se.Attr) > 0 {
		n.attrs = map[string]string{}
		for _, a := range se.Attr {
			n.attrs[a.Name.Local] = a.Value
		}
	}
	var sb strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			k, err := decodeNode(d, t, depth+1)
			if err != nil {
				return nil, err
			}
			n.kids = append(n.kids, k)
		case xml.CharData:
			sb.Write(t)
		case xml.Directive:
			return nil, ErrDoctype
		case xml.EndElement:
			n.text = clean(sb.String())
			return n, nil
		}
	}
}

// first returns the text of the first direct child called name.
func (n *node) first(name string) string {
	for _, k := range n.kids {
		if k.name == name {
			return k.text
		}
	}
	return ""
}

func (n *node) walk(fn func(*node)) {
	for _, k := range n.kids {
		fn(k)
		k.walk(fn)
	}
}

// last returns the text of the last non-empty descendant with one of names (Prime 3 lists keep one entry per effective date).
func (n *node) last(names ...string) string {
	out := ""
	n.walk(func(k *node) {
		for _, w := range names {
			if k.name == w && k.text != "" {
				out = k.text
			}
		}
	})
	return out
}

func (n *node) nameOf() string {
	if v := clean(n.attrs["NAME"]); v != "" {
		return v
	}
	if v := n.first("NAME"); v != "" {
		return v
	}
	for _, k := range n.kids {
		if k.name == "LANGUAGENAME.LIST" {
			for _, l := range k.kids {
				if l.name == "NAME.LIST" {
					if v := l.first("NAME"); v != "" {
						return v
					}
				}
			}
		}
	}
	return ""
}

func yes(s string) bool { return strings.EqualFold(s, "yes") }

func (n *node) address() string {
	var lists []*node
	n.walk(func(k *node) {
		if k.name == "ADDRESS.LIST" {
			lists = append(lists, k)
		}
	})
	for i := len(lists) - 1; i >= 0; i-- {
		var lines []string
		for _, k := range lists[i].kids {
			if k.name == "ADDRESS" && k.text != "" {
				lines = append(lines, k.text)
			}
		}
		if len(lines) > 0 {
			return strings.Join(lines, ", ")
		}
	}
	return ""
}

// primaryOf names the reserved group a group chain ends in ("" when it cannot be resolved).
var reserved = map[string]string{
	"branchdivisions": ClassLiability, "capitalaccount": ClassEquity, "reservessurplus": ClassEquity, "retainedearnings": ClassEquity,
	"currentassets": ClassAsset, "fixedassets": ClassAsset, "investments": ClassAsset, "deposits": ClassAsset, "depositsasset": ClassAsset, "loansadvances": ClassAsset, "loansadvancesasset": ClassAsset,
	"stockinhand": ClassAsset, "miscexpensesasset": ClassAsset,
	"currentliabilities": ClassLiability, "loansliability": ClassLiability, "securedloans": ClassLiability, "unsecuredloans": ClassLiability,
	"bankodac": ClassLiability, "provisions": ClassLiability, "suspenseac": ClassLiability,
	"dutiestaxes": ClassTax, "bankaccounts": ClassBank, "cashinhand": ClassBank,
	"sundrydebtors": ClassCustomer, "sundrycreditors": ClassVendor,
	"salesaccounts": ClassRevenue, "directincomes": ClassRevenue, "incomedirect": ClassRevenue, "indirectincomes": ClassRevenue, "incomeindirect": ClassRevenue,
	"purchaseaccounts": ClassExpense, "directexpenses": ClassExpense, "expensesdirect": ClassExpense, "indirectexpenses": ClassExpense, "expensesindirect": ClassExpense,
}

// classify walks PARENT links to a reserved group. Unknown chains fall back to the nearest group's ISREVENUE / ISDEEMEDPOSITIVE flags.
func classify(parent string, groups map[string]*TallyGroup) (primary, class string, guessed bool) {
	name := parent
	var top *TallyGroup
	for i := 0; i < 50 && name != "" && !strings.EqualFold(name, "primary"); i++ {
		if c, ok := reserved[alnum(name)]; ok {
			return name, c, false
		}
		g := groups[alnum(name)]
		if g == nil {
			break
		}
		if top == nil {
			top = g
		}
		name = g.Parent
	}
	if top == nil {
		return parent, "", true
	}
	switch {
	case top.IsRevenue && top.IsDeemedPos:
		class = ClassExpense
	case top.IsRevenue:
		class = ClassRevenue
	case top.IsDeemedPos:
		class = ClassAsset
	default:
		class = ClassLiability
	}
	return name, class, true
}

// tallyOpening reads an opening balance: Tally writes debits as negative numbers; an explicit Dr/Cr suffix wins.
func tallyOpening(s string) (dr, cr int64, err error) {
	if strings.TrimSpace(s) == "" {
		return 0, 0, nil
	}
	mag, neg, side, err := parseAmount(s)
	if err != nil {
		return 0, 0, err
	}
	if side == 'D' || side == 0 && neg {
		return mag, 0, nil
	}
	return 0, mag, nil
}

var qtyRe = regexp.MustCompile(`^\s*(-?[\d,]*\.?\d+)\s*(.*)$`)

// tallyQty reads "100 Nos", "1,000.500 Kg" or "5 Box = 60 Nos" (the part after "=" is in base units).
func tallyQty(s string) (milli int64, unit string, err error) {
	if i := strings.LastIndex(s, "="); i >= 0 {
		s = s[i+1:]
	}
	m := qtyRe.FindStringSubmatch(s)
	if m == nil {
		return 0, "", errors.New("not a quantity")
	}
	milli, err = parseScaled(strings.TrimPrefix(strings.ReplaceAll(m[1], ",", ""), "-"), 3)
	return milli, strings.TrimSpace(m[2]), err
}

func pl(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ParseTally reads a TallyPrime / ERP 9 masters XML export (UTF-8 or UTF-16). It streams tokens, rejects any DOCTYPE/DTD,
// and caps size (10 MB) and nesting depth. Vouchers are ignored.
func ParseTally(data []byte) (*Tally, error) {
	b, err := decodeText(data)
	if err != nil {
		return nil, err
	}
	d := xml.NewDecoder(bytes.NewReader(scrub(b)))
	d.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil } // already transcoded to UTF-8
	t := &Tally{Units: []string{}, Warnings: []string{}}
	var ledgers, groups, items []*node
	var stack []string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("this is not valid Tally XML: %w", err)
		}
		switch e := tok.(type) {
		case xml.Directive:
			return nil, ErrDoctype
		case xml.StartElement:
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			switch e.Name.Local {
			case "LEDGER", "GROUP", "STOCKITEM", "UNIT", "GODOWN":
				if parent == "TALLYMESSAGE" || parent == "COLLECTION" {
					n, err := decodeNode(d, e, len(stack)+1)
					if err != nil {
						if err == ErrDoctype {
							return nil, err
						}
						return nil, fmt.Errorf("this is not valid Tally XML: %w", err)
					}
					switch n.name {
					case "LEDGER":
						ledgers = append(ledgers, n)
					case "GROUP":
						groups = append(groups, n)
					case "STOCKITEM":
						items = append(items, n)
					case "UNIT":
						if v := n.nameOf(); v != "" {
							t.Units = append(t.Units, v)
						}
					case "GODOWN":
						if v := n.nameOf(); v != "" {
							t.Godowns = append(t.Godowns, TallyGodown{Name: v, Parent: clean(n.first("PARENT"))})
						}
					}
					continue
				}
			}
			if len(stack) >= maxXMLDepth {
				return nil, errors.New("this is not valid Tally XML: nested too deeply")
			}
			stack = append(stack, e.Name.Local)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	gmap := map[string]*TallyGroup{}
	for _, n := range groups {
		g := TallyGroup{Name: n.nameOf(), Parent: n.first("PARENT"), IsRevenue: yes(n.first("ISREVENUE")), IsDeemedPos: yes(n.first("ISDEEMEDPOSITIVE"))}
		if g.Name == "" {
			continue
		}
		t.Groups = append(t.Groups, g)
	}
	for i := range t.Groups {
		gmap[alnum(t.Groups[i].Name)] = &t.Groups[i]
	}
	for i := range t.Groups {
		t.Groups[i].PrimaryGroup, _, _ = classify(t.Groups[i].Name, gmap)
	}
	var guessed, badOpen, badGSTIN, noHSN, badQty int
	for _, n := range ledgers {
		l := TallyLedger{Name: n.nameOf(), Parent: n.first("PARENT")}
		if l.Name == "" {
			continue
		}
		var g bool
		l.PrimaryGroup, l.Class, g = classify(l.Parent, gmap)
		if alnum(l.Name) == "profitlossac" && l.Class == "" {
			l.Class, g = ClassEquity, false
		}
		if g {
			guessed++
		}
		if l.Class == "" {
			l.Class = ClassAsset
		}
		var err error
		if l.OpeningDr, l.OpeningCr, err = tallyOpening(n.first("OPENINGBALANCE")); err != nil {
			badOpen++
		}
		l.GSTIN = strings.ToUpper(n.last("PARTYGSTIN", "GSTIN"))
		l.PAN = strings.ToUpper(n.last("INCOMETAXNUMBER"))
		l.StateName = n.last("LEDSTATENAME", "STATE")
		if l.StateName == "" {
			l.StateName = n.last("PLACEOFSUPPLY")
		}
		l.StateCode, _ = StateCode(l.StateName)
		if l.GSTIN != "" {
			if v, err := CheckGSTIN(l.GSTIN); err == nil {
				l.StateCode = v[:2]
			} else if l.Class == ClassCustomer || l.Class == ClassVendor {
				badGSTIN++
			}
		}
		if l.StateCode != "" {
			l.StateName, _ = StateName(l.StateCode)
		}
		l.Address = n.address()
		l.Email = n.last("EMAIL")
		if l.Phone = n.last("LEDGERMOBILE"); l.Phone == "" {
			l.Phone = n.last("LEDGERPHONE")
		}
		switch l.Class {
		case ClassCustomer:
			t.Found.Customers++
		case ClassVendor:
			t.Found.Vendors++
		default:
			t.Found.Ledgers++
		}
		t.Ledgers = append(t.Ledgers, l)
	}
	names := map[string]int{}
	for _, n := range items {
		it := TallyItem{Name: n.nameOf(), Parent: n.first("PARENT"), BaseUnit: n.first("BASEUNITS")}
		if it.Name == "" {
			continue
		}
		names[strings.ToLower(it.Name)]++
		if q := n.first("OPENINGBALANCE"); q != "" {
			v, u, err := tallyQty(q)
			if err != nil {
				badQty++
			}
			it.QtyMilli = v
			if it.BaseUnit == "" {
				it.BaseUnit = u
			}
		}
		if r, _, _ := strings.Cut(n.first("OPENINGRATE"), "/"); r != "" {
			it.RateMinor, _, _, _ = parseAmount(r)
		}
		if v := n.first("OPENINGVALUE"); v != "" {
			it.ValueMinor, _, _, _ = parseAmount(v)
		}
		it.HSN = strings.ReplaceAll(n.last("HSNCODE", "HSN"), " ", "")
		if it.HSN == "" {
			noHSN++
		}
		rates := map[string]int{}
		n.walk(func(k *node) {
			if k.name == "RATEDETAILS.LIST" {
				if bp, err := ParseGSTRate(k.first("GSTRATE")); err == nil && k.first("GSTRATE") != "" {
					rates[strings.ToUpper(k.first("GSTRATEDUTYHEAD"))] = bp
				}
			}
		})
		if bp, ok := rates["IGST"]; ok {
			it.GSTRateBP, it.HasGSTRate = bp, true
		} else if c, ok := rates["CGST"]; ok {
			s, ok2 := rates["SGST/UTGST"]
			if !ok2 {
				s, ok2 = rates["SGST"]
			}
			if ok2 && gstRatesBP[c+s] {
				it.GSTRateBP, it.HasGSTRate = c+s, true
			}
		}
		t.Items = append(t.Items, it)
	}
	t.Found.Items, t.Found.Units, t.Found.Godowns, t.Found.Groups = len(t.Items), len(t.Units), len(t.Godowns), len(t.Groups)
	if len(t.Ledgers)+len(t.Items)+len(t.Units)+len(t.Godowns) == 0 {
		return nil, ErrNoMasters
	}
	dupes := 0
	for _, c := range names {
		if c > 1 {
			dupes += c
		}
	}
	for _, w := range []struct {
		n         int
		one, many string
	}{
		{noHSN, "item has no HSN", "items have no HSN"},
		{dupes, "item shares its name with another item", "items share a name with another item"},
		{badQty, "item has an opening quantity we could not read (more than 3 decimals?)", "items have an opening quantity we could not read (more than 3 decimals?)"},
		{badGSTIN, "customer or vendor has a GSTIN that fails the format or check-character test", "customers or vendors have a GSTIN that fails the format or check-character test"},
		{badOpen, "ledger has an opening balance we could not read", "ledgers have an opening balance we could not read"},
		{guessed, "ledger sits under a group we did not recognise; it was placed by that group's debit/credit and income/expense flags", "ledgers sit under groups we did not recognise; they were placed by those groups' debit/credit and income/expense flags"},
	} {
		if w.n > 0 {
			t.Warnings = append(t.Warnings, pl(w.n, w.one, w.many))
		}
	}
	return t, nil
}
