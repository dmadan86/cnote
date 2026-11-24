package sites

import (
	"crypto/sha256"
	"fmt"
	"os"
	"strings"

	"github.com/cnote/api/internal/command"
)

func I18nCommands() []command.Command { return nil }
func Fingerprint(s string) string     { sum := sha256.Sum256([]byte(s)); return fmt.Sprintf("%x", sum[:8]) }
func SupportedLang(code string) bool {
	switch code {
	case "en", "hi", "ta", "te", "kn", "mr", "bn", "gu", "ml":
		return true
	}
	if code != "pa" && code != "or" {
		return false
	}
	for _, v := range strings.Split(os.Getenv("SITES_EXTRA_LANGS"), ",") {
		if strings.TrimSpace(v) == code {
			return true
		}
	}
	return false
}
