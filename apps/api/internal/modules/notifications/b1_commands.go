package notifications

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
	su "github.com/cnote/api/internal/schemautil"
)

// Phase B1 command contract for notification preferences (docs-parity-b 4.3 and 13.2.5). Stubs until P3 registers the real
// handlers from an init() in notifications/prefs.go: b1Handlers["notify_prefs.get"] = prefsGet.
//
// Both belong to a person: a principal with no user is refused, as in notification.list. Preferences are per workspace.
// The columns direct_off and digest_off are set only by the one-click unsubscribe link and by these commands; the mail job
// reads them at send time together with email_mode.

var sPrefs = su.Obj([]string{"email_mode", "digest_hour", "email_configured", "direct_off", "digest_off"},
	"email_mode", su.Enum("smart", "digest", "off"), "digest_hour", su.IntRange(0, 23), "email_configured", su.Bool,
	"direct_off", su.Bool, "digest_off", su.Bool)

var b1Specs = []su.Spec{
	{Name: "notify_prefs.get", Group: command.GroupRead, MinRole: command.Guest,
		Desc: "The caller's email notification settings in this workspace. email_mode: smart (mentions, tasks, shares and access requests within minutes, comments and page updates in the daily digest; the default), digest (everything in the daily digest) or off. digest_hour is the hour of the digest in IST. direct_off and digest_off are set by the unsubscribe link in a mail. email_configured is false when the server cannot send mail. Belongs to a person: a bare API key is refused. Returns {email_mode, digest_hour, email_configured, direct_off, digest_off}.",
		In:   su.Obj(nil), Out: sPrefs},
	{Name: "notify_prefs.set", Group: command.GroupWriteNotes, MinRole: command.Guest,
		Desc: "Change the caller's email notification settings: email_mode (smart, digest, off), digest_hour (0 to 23, IST), and direct_off or digest_off (false turns mail of that kind back on after an unsubscribe). Takes effect for mail not yet sent too: the mail job reads the settings when it builds each message. Belongs to a person: a bare API key is refused. Returns the settings as notify_prefs.get does.",
		In:   su.Obj(nil, "email_mode", su.Enum("smart", "digest", "off"), "digest_hour", su.IntRange(0, 23), "direct_off", su.Bool, "digest_off", su.Bool),
		Out:  sPrefs},
}

var b1Handlers = map[string]command.Handler{}

func b1Commands() []command.Command { return su.Commands(b1Specs, b1Handlers) }

// B1OutputSchema is the JSON Schema of a B1 command's result; apps/web/src/lib/docs.ts mirrors it.
func B1OutputSchema(name string) json.RawMessage { return su.OutputSchema(b1Specs, name) }
