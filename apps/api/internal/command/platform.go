package command

import (
	"os"
	"strings"
)

// PlatformAdmin allows only a signed-in person whose user id is listed in CNOTE_PLATFORM_ADMIN_USER_IDS
// (comma-separated). Agents, API keys and every other id are refused. Read at call time.
func (c *Ctx) PlatformAdmin() error {
	if c.Principal.Kind == KindUser && c.Principal.UserID != nil {
		for _, v := range strings.Split(os.Getenv("CNOTE_PLATFORM_ADMIN_USER_IDS"), ",") {
			if v = strings.ToLower(strings.TrimSpace(v)); v != "" && v == strings.ToLower(*c.Principal.UserID) {
				return nil
			}
		}
	}
	return ErrForbidden("platform admins only")
}
