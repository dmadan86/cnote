package bank

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// mapping names the statement's own header cells. Banks differ (HDFC "Withdrawal Amt." / "Deposit Amt.", SBI "Debit" /
// "Credit", ICICI "Dr/Cr" beside one "Amount", Kotak signed "Amount"), so the first import detects it and the account keeps it.
type mapping struct {
	Date      string `json:"date"`
	Narration string `json:"narration"`
	Debit     string `json:"debit"`
	Credit    string `json:"credit"`
	Amount    string `json:"amount"`
	Type      string `json:"type"` // Dr/Cr marker beside a single Amount column
	Balance   string `json:"balance"`
	Reference string `json:"reference"`
}

// Keywords are tried in order against the normalised header; the first column containing one wins.
var keywords = []struct {
	field func(*mapping) *string
	words []string
}{
	{func(m *mapping) *string { return &m.Date }, []string{"txn date", "transaction date", "tran date", "posting date", "date", "value date"}},
	{func(m *mapping) *string { return &m.Debit }, []string{"withdrawal", "debit", "dr amount", "paid out", "money out"}},
	{func(m *mapping) *string { return &m.Credit }, []string{"deposit", "credit", "cr amount", "paid in", "money in"}},
	{func(m *mapping) *string { return &m.Balance }, []string{"closing balance", "running balance", "balance"}},
	{func(m *mapping) *string { return &m.Type }, []string{"dr cr", "cr dr", "drcr", "crdr", "transaction type", "txn type", "type"}},
	{func(m *mapping) *string { return &m.Reference }, []string{"chq ref no", "ref no", "reference", "cheque no", "chq no", "utr"}},
	{func(m *mapping) *string { return &m.Narration }, []string{"narration", "description", "particulars", "transaction remarks", "remarks", "details"}},
	{func(m *mapping) *string { return &m.Amount }, []string{"transaction amount", "amount"}},
}

const bom = "\xef\xbb\xbf"

func norm(s string) string {
	s = strings.ToLower(strings.TrimPrefix(s, bom))
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, s)
	return strings.Join(strings.Fields(s), " ")
}

func detect(headers []string) mapping {
	var m mapping
	used := map[int]bool{}
	for _, k := range keywords {
		if k.field(&m) == &m.Amount && (m.Debit != "" || m.Credit != "") {
			continue // debit/credit columns already say it; "Withdrawal Amount" must not be taken as a third
		}
	search:
		for _, w := range k.words {
			for i, h := range headers {
				if !used[i] && strings.Contains(norm(h), w) {
					*k.field(&m) = strings.TrimSpace(h)
					used[i] = true
					break search
				}
			}
		}
	}
	return m
}

type cols struct{ date, narr, debit, credit, amount, typ, bal, ref int }

// resolve maps header names to column indexes. ok is false unless a date and some way to get an amount are present.
func resolve(headers []string, m mapping) (cols, bool) {
	find := func(name string) int {
		if strings.TrimSpace(name) == "" {
			return -1
		}
		for i, h := range headers {
			if norm(h) == norm(name) {
				return i
			}
		}
		return -2 // named but absent
	}
	c := cols{find(m.Date), find(m.Narration), find(m.Debit), find(m.Credit), find(m.Amount), find(m.Type), find(m.Balance), find(m.Reference)}
	for _, i := range []int{c.date, c.narr, c.debit, c.credit, c.amount, c.typ, c.bal, c.ref} {
		if i == -2 {
			return c, false
		}
	}
	return c, c.date >= 0 && (c.debit >= 0 || c.credit >= 0 || c.amount >= 0)
}

type parsedLine struct {
	Date      time.Time
	Amount    int64 // credit positive
	Narration string
	Reference string
	Balance   *int64
	Hash      string
	Row       int
}

type parseResult struct {
	Lines   []parsedLine
	Used    mapping
	Skipped int      // rows that are not transactions (preamble, totals, separators)
	Errors  []string // rows that look like transactions but could not be read
}

func sniffDelimiter(text string) rune {
	first := text
	for _, l := range strings.Split(text, "\n") { // the first line with several separators (preamble lines may have none)
		if strings.Count(l, ",")+strings.Count(l, ";")+strings.Count(l, "\t") >= 2 {
			first = l
			break
		}
	}
	best, n := ',', strings.Count(first, ",")
	for _, d := range []rune{';', '\t'} {
		if c := strings.Count(first, string(d)); c > n {
			best, n = d, c
		}
	}
	return best
}

// parseStatement reads a bank CSV. given (an explicit or saved mapping) is tried first, then auto-detection.
func parseStatement(text string, given *mapping) (parseResult, error) {
	var res parseResult
	text = strings.ReplaceAll(strings.TrimPrefix(text, bom), "\r\n", "\n")
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = sniffDelimiter(text)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		return res, fmt.Errorf("not a readable CSV: %v", err)
	}
	var c cols
	hdr := -1
	for i := 0; i < len(recs) && i < 40 && hdr < 0; i++ {
		tries := []mapping{detect(recs[i])}
		if given != nil && *given != (mapping{}) {
			tries = []mapping{*given, tries[0]}
		}
		for _, m := range tries {
			if cc, ok := resolve(recs[i], m); ok {
				c, hdr, res.Used = cc, i, m
				break
			}
		}
	}
	if hdr < 0 {
		return res, fmt.Errorf("no header row with a date and a debit/credit or amount column in the first 40 rows; send a mapping")
	}
	cell := func(rec []string, i int) string {
		if i < 0 || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}
	seen := map[string]int{}
	for i := hdr + 1; i < len(recs); i++ {
		rec := recs[i]
		d, ok := parseDate(cell(rec, c.date))
		amt, err := rowAmount(rec, c, cell)
		if !ok {
			if err == nil && amt != 0 && cell(rec, c.date) != "" && len(res.Errors) < 20 { // a readable amount with no date; anything else is a separator or total
				res.Errors = append(res.Errors, fmt.Sprintf("row %d: date %q not recognised (use DD/MM/YYYY)", i+1, cell(rec, c.date)))
			} else {
				res.Skipped++
			}
			continue
		}
		if err != nil {
			if len(res.Errors) < 20 {
				res.Errors = append(res.Errors, fmt.Sprintf("row %d: %v", i+1, err))
			}
			continue
		}
		if amt == 0 {
			res.Skipped++
			continue
		}
		l := parsedLine{Date: d, Amount: amt, Narration: strings.Join(strings.Fields(cell(rec, c.narr)), " "), Reference: cell(rec, c.ref), Row: i + 1}
		if b := cell(rec, c.bal); b != "" {
			if v, _, err := parseMinor(b); err == nil {
				l.Balance = &v
			}
		}
		bal := ""
		if l.Balance != nil {
			bal = fmt.Sprint(*l.Balance)
		}
		key := fmt.Sprintf("%s|%d|%s|%s", d.Format("2006-01-02"), amt, strings.ToLower(l.Narration), bal)
		// Identical lines on one day (no balance column to tell them apart) get an occurrence number, so a re-import
		// still maps each to the same hash.
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d", key, seen[key])))
		seen[key]++
		l.Hash = hex.EncodeToString(sum[:])
		res.Lines = append(res.Lines, l)
	}
	if n := len(res.Lines); n > 1 && res.Lines[0].Date.After(res.Lines[n-1].Date) { // newest-first export: store chronologically
		for a, b := 0, n-1; a < b; a, b = a+1, b-1 {
			res.Lines[a], res.Lines[b] = res.Lines[b], res.Lines[a]
		}
	}
	return res, nil
}

// rowAmount: credit positive.
func rowAmount(rec []string, c cols, cell func([]string, int) string) (int64, error) {
	if c.debit >= 0 || c.credit >= 0 {
		dv, _, derr := parseMinor(cell(rec, c.debit))
		cv, _, cerr := parseMinor(cell(rec, c.credit))
		if derr != nil || cerr != nil {
			return 0, fmt.Errorf("amount not readable: %q / %q", cell(rec, c.debit), cell(rec, c.credit))
		}
		if dv != 0 && cv != 0 {
			return 0, fmt.Errorf("both debit and credit are filled")
		}
		return abs(cv) - abs(dv), nil
	}
	raw := cell(rec, c.amount)
	v, _, err := parseMinor(raw)
	if err != nil {
		return 0, fmt.Errorf("amount %q not readable", raw)
	}
	if t := strings.ToLower(cell(rec, c.typ)); t != "" {
		if (strings.HasPrefix(t, "d") && !strings.HasPrefix(t, "dep")) || strings.HasPrefix(t, "w") {
			return -abs(v), nil
		}
		return abs(v), nil
	}
	return v, nil
}

var timeTail = regexp.MustCompile(`(?i)[ T]+\d{1,2}:\d{2}(:\d{2})?\s*([ap]m)?$`)

var dateLayouts = []string{"2/1/2006", "2/1/06", "2-1-2006", "2-1-06", "2006-01-02", "2.1.2006", "2-Jan-2006", "2-Jan-06", "2 Jan 2006", "2 Jan 06",
	"2 January 2006", "2-January-2006", "Jan 2, 2006"}

// parseDate reads Indian day-first dates; "03/04/2024" is 3 April, never March 4.
func parseDate(s string) (time.Time, bool) {
	s = timeTail.ReplaceAllString(strings.TrimSpace(s), "")
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// parseMinor reads an amount into paise: "1,23,456.78", "Rs. 500", "(250.00)", "-90", "1,000.00 Dr". marker is -1 for a
// Dr suffix, +1 for Cr, 0 otherwise; a Dr amount comes back negative. Empty text is zero.
func parseMinor(s string) (v int64, marker int, err error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, 0, nil
	}
	neg := false
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		neg, t = true, t[1:len(t)-1]
	}
	l := strings.ToLower(t)
	switch {
	case strings.HasSuffix(l, "dr"), strings.HasSuffix(l, "dr."):
		marker, t = -1, t[:strings.LastIndex(l, "dr")]
	case strings.HasSuffix(l, "cr"), strings.HasSuffix(l, "cr."):
		marker, t = 1, t[:strings.LastIndex(l, "cr")]
	}
	t = strings.ToLower(t)
	for _, p := range []string{"₹", "rs.", "rs", "inr"} {
		t = strings.ReplaceAll(t, p, "")
	}
	t = strings.NewReplacer(",", "", " ", "", " ", "").Replace(t)
	if strings.HasPrefix(t, "-") {
		neg, t = true, t[1:]
	} else {
		t = strings.TrimPrefix(t, "+")
	}
	whole, frac, _ := strings.Cut(t, ".")
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 {
		if strings.Trim(frac[2:], "0") != "" {
			return 0, 0, fmt.Errorf("more than two decimals")
		}
		frac = frac[:2]
	}
	frac += strings.Repeat("0", 2-len(frac))
	if len(whole) > 15 {
		return 0, 0, fmt.Errorf("amount too large")
	}
	for _, r := range whole + frac {
		if r < '0' || r > '9' {
			return 0, 0, fmt.Errorf("not a number")
		}
	}
	v, _ = strconv.ParseInt(whole+frac, 10, 64)
	if neg || marker < 0 {
		v = -v
	}
	return v, marker, nil
}
