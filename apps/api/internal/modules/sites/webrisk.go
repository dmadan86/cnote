package sites

import (
	"errors"
	"net/url"
	"strings"

	"github.com/cnote/api/internal/command"
	"golang.org/x/net/idna"
)

func WebRiskCommands() []command.Command { return nil }
func CanonicalURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "", errors.New("invalid URL")
	}
	host, err := idna.Lookup.ToUnicode(strings.ToLower(u.Hostname()))
	if err != nil {
		return "", err
	}
	if port := u.Port(); port != "" && !((u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")) {
		host += ":" + port
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return strings.ToLower(u.Scheme) + "://" + host + path, nil
}
