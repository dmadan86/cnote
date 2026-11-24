package sites

import (
	_ "embed"
	"regexp"
	"strings"
)

//go:embed blocklist.txt
var blocklistText string

var (
	subdomainRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	blockExact  = map[string]bool{}
	blockWords  []string
	leet        = strings.NewReplacer("-", "", "0", "o", "1", "l", "3", "e", "5", "s")
)

func init() {
	section := ""
	for _, l := range strings.Split(blocklistText, "\n") {
		switch l = strings.TrimSpace(l); {
		case l == "" || strings.HasPrefix(l, "#"):
		case l == "[exact]" || l == "[contains]":
			section = l
		case section == "[exact]":
			blockExact[leet.Replace(l)] = true
		default:
			blockWords = append(blockWords, leet.Replace(l))
		}
	}
}

// CheckSubdomain lowercases and trims s and says why it cannot be used: "invalid" (shape or length) or "reserved"
// (blocklist). An empty reason means the name is acceptable; whether it is taken is a database question.
func CheckSubdomain(s string) (string, string) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 3 || len(s) > 40 || !subdomainRe.MatchString(s) {
		return s, "invalid"
	}
	n := leet.Replace(s)
	if blockExact[n] {
		return s, "reserved"
	}
	for _, w := range blockWords {
		if strings.Contains(n, w) {
			return s, "reserved"
		}
	}
	return s, ""
}
