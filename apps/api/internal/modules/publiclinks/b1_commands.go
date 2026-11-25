package publiclinks

import (
	"encoding/json"

	"github.com/cnote/api/internal/command"
	su "github.com/cnote/api/internal/schemautil"
)

// Phase B1 command contract for public page links (docs-parity-b 4.2, 6 and 13). Stubs until P2 registers the real handlers
// from an init() in publiclinks/page.go: b1Handlers["page_link.get"] = pageLinkGet.
//
// Rules that bind the handlers:
//   - The token is shown once, in the result of page_link.create (ReturnsSecret). Only its SHA-256 is stored. It is never
//     in an event, a log line, an error or the idempotency table.
//   - A link serves only while every rule of 4.2 holds, computed per request: the resolver (core.resolve_page_link), the
//     creator still a member with edit access, no view restriction on the page or an ancestor, depth at most 50.
//   - page_link.list filters by the caller's full page access before counting or returning anything (13.2.3). The space
//     admin's "turn off all public links" is space.update public_links=false and returns nothing about pages.
//   - The authorization and the body or file read of a public request happen in one transaction that holds FOR SHARE on the
//     link row; revoke and replace take FOR UPDATE (13.2.4).

var (
	sPageLink = su.Obj([]string{"id", "status", "created_by_name", "created_at", "expires_at", "view_count", "last_viewed_at"},
		"id", su.ID, "status", su.Enum("active", "off"), "created_by_name", su.Nullable(su.Str), "created_at", su.TS,
		"expires_at", su.Nullable(su.TS), "view_count", su.Int, "last_viewed_at", su.Nullable(su.TS))
)

var b1Specs = []su.Spec{
	{Name: "page_link.get", Group: command.GroupRead, MinRole: command.Member,
		Desc: "The public link of a page, without the token (it cannot be shown again). Needs view access to the page. link is null when the page has no active link. allowed says whether a link could be created now and reason why not (the space has public links off, the space is archived, the page is not a plain page, or it or an ancestor has a view restriction). The same rules decide whether an existing link serves, so link.status is the truth the page's Public chip shows. Returns {link: {id, status, created_by_name, created_at, expires_at, view_count, last_viewed_at} | null, allowed, reason}.",
		In:   su.Obj([]string{"note_id"}, "note_id", su.ID),
		Out:  su.Obj([]string{"link", "allowed", "reason"}, "link", su.Nullable(sPageLink), "allowed", su.Bool, "reason", su.Nullable(su.Str))},
	{Name: "page_link.create", Group: command.GroupSendMessages, MinRole: command.Member, Confirm: true, Secret: true,
		Desc: "Make the public link of a page: anyone with the address can read the page (not its comments, sub-pages or other files) with no sign-in. Replaces the page's earlier link at once. Returns the address once: the token is not stored and cannot be shown again. Needs edit access to the page; refused for a typed page, in an archived space, when the space switched public links off, and when the page or an ancestor has a view restriction ('Remove the view restriction to share this page publicly'). expires_in_days is 1 to 365; omitted or null never expires. The link stops when its creator leaves the workspace or loses edit access. Emits share.created with purpose page. Returns {link, url}.",
		In:   su.Obj([]string{"note_id"}, "note_id", su.ID, "expires_in_days", su.Nullable(su.IntRange(1, 365))),
		Out:  su.Obj([]string{"link", "url"}, "link", sPageLink, "url", su.Str)},
	{Name: "page_link.revoke", Group: command.GroupWriteNotes, MinRole: command.Member,
		Desc: "Turn off a page's public link; it stops serving at once. Needs edit access to the page, or space admin. Emits share.revoked. Returns {revoked: n}.",
		In:   su.Obj([]string{"note_id"}, "note_id", su.ID), Out: su.Obj([]string{"revoked"}, "revoked", su.Int)},
	{Name: "page_link.list", Group: command.GroupRead, MinRole: command.Member,
		Desc: "The pages of a space that have an active public link, newest first, for the space settings page. Needs space admin. Only pages the caller can read are listed or counted; a page the admin cannot see is not there. Returns [{note_id, title, created_by_name, created_at, expires_at, view_count}].",
		In:   su.Obj([]string{"space_id"}, "space_id", su.ID),
		Out: su.Arr(su.Obj([]string{"note_id", "title", "created_by_name", "created_at", "expires_at", "view_count"},
			"note_id", su.ID, "title", su.Str, "created_by_name", su.Nullable(su.Str), "created_at", su.TS, "expires_at", su.Nullable(su.TS), "view_count", su.Int))},
	{Name: "page_link.revoke_space", Group: command.GroupWriteNotes, MinRole: command.Member, Internal: true,
		Desc: "Revoke every active page link of a space in the caller's transaction. Internal: called by space.update when public_links is turned off; no access check of its own. Returns {revoked: n}.",
		In:   su.Obj([]string{"space_id"}, "space_id", su.ID), Out: su.Obj([]string{"revoked"}, "revoked", su.Int)},
}

var b1Handlers = map[string]command.Handler{}

func b1Commands() []command.Command { return su.Commands(b1Specs, b1Handlers) }

// B1OutputSchema is the JSON Schema of a B1 command's result; apps/web/src/lib/docs.ts mirrors it.
func B1OutputSchema(name string) json.RawMessage { return su.OutputSchema(b1Specs, name) }
