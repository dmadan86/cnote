package publiclinks

import (
	"os"
	"strings"
)

func getenv(k string) string { return strings.TrimSpace(os.Getenv(k)) }
