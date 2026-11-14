package crm

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cnote/api/internal/command"
)

var gstLayout = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)

func validState(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && len(s) == 2 && ((n >= 1 && n <= 38) || s == "97" || s == "99") && fmt.Sprintf("%02d", n) == s
}
func checkChar(s string) byte {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	sum := 0
	for i, b := range []byte(s) {
		v := strings.IndexByte(alphabet, b)
		if v < 0 {
			v = 0
		}
		v *= 1 + i%2
		sum += v/36 + v%36
	}
	return alphabet[(36-sum%36)%36]
}
func checkTaxIDs(g, state *string) (*string, *string, error) {
	if g != nil {
		v := strings.TrimSpace(*g)
		if v == "" {
			g = nil
		} else {
			// Full Unicode uppercase mappings are delegated to the reference serde/handler path.
			for _, r := range v {
				if r > 127 {
					return nil, nil, command.ErrFallback
				}
			}
			v = strings.ToUpper(v)
			switch {
			case len(v) != 15:
				return nil, nil, command.ErrInvalid("GSTIN must be 15 characters")
			case !gstLayout.MatchString(v):
				return nil, nil, command.ErrInvalid("GSTIN format is invalid")
			case !validState(v[:2]):
				return nil, nil, command.ErrInvalid("GSTIN has unknown state code " + v[:2])
			case checkChar(v[:14]) != v[14]:
				return nil, nil, command.ErrInvalid("GSTIN checksum does not match")
			}
			g = &v
		}
	}
	if state != nil && strings.TrimSpace(*state) == "" {
		state = nil
	}
	if state != nil && !validState(*state) {
		return nil, nil, command.ErrInvalid("unknown GST state code " + *state)
	}
	if state == nil && g != nil {
		v := (*g)[:2]
		state = &v
	}
	if g != nil && state != nil && (*g)[:2] != *state {
		return nil, nil, command.ErrInvalid("state_code does not match the GSTIN")
	}
	return g, state, nil
}
