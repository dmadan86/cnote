package dataimport

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// gstRatesBP are the GST slabs accepted, in basis points.
var gstRatesBP = map[int]bool{0: true, 10: true, 25: true, 150: true, 300: true, 500: true, 1200: true, 1800: true, 2800: true, 4000: true}

const gstRateHelp = "0, 0.1, 0.25, 1.5, 3, 5, 12, 18, 28 or 40"

var (
	currencyRe = regexp.MustCompile(`(?i)^(?:₹|rs\.?|inr)\s*`)
	sideRe     = regexp.MustCompile(`(?i)\s*(dr|cr)\.?$`)
	panRe      = regexp.MustCompile(`^[A-Z]{5}[0-9]{4}[A-Z]$`)
	timeTailRe = regexp.MustCompile(`[ T]\d{1,2}:\d{2}.*$`)
)

// parseScaled reads an unsigned decimal ("1180", "1180.5") as an integer of 10^-scale units. Extra non-zero decimals are an error.
func parseScaled(s string, scale int) (int64, error) {
	if s == "" {
		return 0, errors.New("is empty")
	}
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" && frac == "" || strings.Contains(frac, ".") {
		return 0, errors.New("is not a number")
	}
	for _, p := range []string{whole, frac} {
		for _, c := range p {
			if c < '0' || c > '9' {
				return 0, errors.New("is not a number")
			}
		}
	}
	if len(frac) > scale {
		if strings.Trim(frac[scale:], "0") != "" {
			return 0, fmt.Errorf("has more than %d decimal places", scale)
		}
		frac = frac[:scale]
	}
	if len(strings.TrimLeft(whole, "0")) > 15 {
		return 0, errors.New("is too large")
	}
	frac += strings.Repeat("0", scale-len(frac))
	var n int64
	for _, c := range whole + frac {
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

// parseAmount reads an Indian-formatted amount: "1,18,000.00", "₹ 1180", "Rs. 500", "(500)", "-500", "1180 Dr", "250 Cr".
// It returns the magnitude in paise, whether a minus sign or brackets were used, and the Dr/Cr suffix ('D', 'C' or 0).
func parseAmount(s string) (mag int64, neg bool, side byte, err error) {
	t := currencyRe.ReplaceAllString(strings.TrimSpace(s), "")
	if m := sideRe.FindStringSubmatch(t); m != nil {
		side = 'D'
		if strings.EqualFold(m[1], "cr") {
			side = 'C'
		}
		t = strings.TrimSpace(t[:len(t)-len(m[0])])
	}
	switch {
	case strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")"):
		neg, t = true, t[1:len(t)-1]
	case strings.HasPrefix(t, "-"):
		neg, t = true, t[1:]
	case strings.HasPrefix(t, "+"):
		t = t[1:]
	case strings.HasSuffix(t, "-"):
		neg, t = true, t[:len(t)-1]
	}
	t = currencyRe.ReplaceAllString(strings.TrimSpace(t), "")
	if neg && side != 0 {
		return 0, false, 0, errors.New("has both a minus sign and Dr/Cr")
	}
	mag, err = parseScaled(strings.ReplaceAll(strings.ReplaceAll(t, ",", ""), " ", ""), 2)
	return
}

// ParseMoney reads an amount into signed paise: Dr and plain positive numbers are positive, Cr and negatives are negative.
func ParseMoney(s string) (int64, error) {
	mag, neg, side, err := parseAmount(s)
	if err != nil {
		return 0, err
	}
	if neg || side == 'C' {
		return -mag, nil
	}
	return mag, nil
}

var dateLayouts = []string{
	"2-1-2006", "2/1/2006", "2.1.2006", "2006-1-2", "2006/1/2", "2-Jan-2006", "2-Jan-06", "2 Jan 2006", "2 January 2006",
	"2-January-2006", "2/Jan/2006", "2-1-06", "2/1/06",
}

// ParseDate reads a calendar date (DD-MM-YYYY, DD/MM/YYYY, YYYY-MM-DD, 1-Apr-2025) and returns it as YYYY-MM-DD.
func ParseDate(s string) (string, error) {
	t := timeTailRe.ReplaceAllString(strings.TrimSpace(s), "")
	for _, l := range dateLayouts {
		if d, err := time.Parse(l, t); err == nil {
			if d.Year() < 1990 || d.Year() > 2100 {
				return "", fmt.Errorf("date %q has an unlikely year", s)
			}
			return d.Format("2006-01-02"), nil
		}
	}
	return "", fmt.Errorf("date %q is not a valid date (use DD-MM-YYYY)", s)
}

// ParseQty reads a quantity of at most three decimals into thousandths.
func ParseQty(s string) (int64, error) {
	n, err := parseScaled(strings.ReplaceAll(strings.TrimSpace(s), ",", ""), 3)
	if err != nil {
		return 0, fmt.Errorf("quantity %q %v", s, err)
	}
	return n, nil
}

// ParseGSTRate reads a GST rate ("18", "18%", "1.5", "Exempt") into basis points; only the standard slabs are accepted.
func ParseGSTRate(s string) (int, error) {
	t := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")))
	if t == "exempt" || t == "nil" || t == "nil rated" || t == "exempted" {
		return 0, nil
	}
	n, err := parseScaled(t, 2)
	if err != nil || !gstRatesBP[int(n)] {
		return 0, fmt.Errorf("GST rate %q is not one of %s", s, gstRateHelp)
	}
	return int(n), nil
}

// ValidateHSN accepts a 4, 6 or 8 digit HSN/SAC code.
func ValidateHSN(h string) error {
	if n := len(h); n == 4 || n == 6 || n == 8 {
		for i := 0; i < n; i++ {
			if h[i] < '0' || h[i] > '9' {
				return errHSN
			}
		}
		return nil
	}
	return errHSN
}

var errHSN = errors.New("HSN/SAC must be 4, 6 or 8 digits")

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

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }

// CheckGSTIN checks a GSTIN's length, shape, state code and check character and returns it upper-cased.
// A pass means the number is well formed; it does not mean the GST portal knows it.
func CheckGSTIN(s string) (string, error) {
	g := strings.ToUpper(strings.TrimSpace(s))
	if len(g) != 15 {
		return "", errors.New("GSTIN must be 15 characters")
	}
	ok := isDigit(g[0]) && isDigit(g[1])
	for i := 2; i < 7; i++ {
		ok = ok && isUpper(g[i])
	}
	for i := 7; i < 11; i++ {
		ok = ok && isDigit(g[i])
	}
	ok = ok && isUpper(g[11]) && g[12] != '0' && (isDigit(g[12]) || isUpper(g[12])) && g[13] == 'Z' && (isDigit(g[14]) || isUpper(g[14]))
	if !ok {
		return "", errors.New("GSTIN format is not valid")
	}
	if _, found := StateName(g[:2]); !found {
		return "", errors.New("GSTIN has an unknown state code " + g[:2])
	}
	if gstinCheckChar(g[:14]) != g[14] {
		return "", errors.New("GSTIN check character does not match (look for a typing mistake)")
	}
	return g, nil
}

// CheckPAN validates the PAN shape and upper-cases it.
func CheckPAN(s string) (string, error) {
	p := strings.ToUpper(strings.TrimSpace(s))
	if !panRe.MatchString(p) {
		return "", errors.New("PAN must be 5 letters, 4 digits and 1 letter (like ABCDE1234F)")
	}
	return p, nil
}

// clean collapses runs of whitespace (including newlines) into single spaces.
func clean(s string) string { return strings.Join(strings.Fields(s), " ") }
