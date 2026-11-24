package sites

// Editing without the AI (design 3, 6, 7 and 17.11, 17.12): site.edit, restore, version history and its pruning.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

const (
	coalesceWindow = "5 minutes"
	keepVersions   = 200
	versionsPage   = 50
)

func EditorCommands() []command.Command {
	return []command.Command{
		mk("site.versions", command.GroupRead, command.Member, false, "List a website's versions, newest first: who, when, source and a summary. Page with before_seq (the seq of the last row you have).",
			obj([]string{"id"}, props{"id": str("site id"), "before_seq": props{"type": "integer"}, "limit": props{"type": "integer", "description": "at most 50"}}), versions),
	}
}

func versions(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID        string
		BeforeSeq *int `json:"before_seq"`
		Limit     int
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	if in.Limit < 1 || in.Limit > versionsPage {
		in.Limit = versionsPage
	}
	rows, err := c.Tx.Query(c.Context, `select v.id::text, v.seq, v.source, v.summary, v.created_by::text, coalesce(u.name, ''), v.created_at,
			v.id = s.draft_version_id, v.id is not distinct from s.published_version_id
		from sites.site_versions v join sites.sites s on s.workspace_id = v.workspace_id and s.id = v.site_id
		left join core.users u on u.id = v.created_by
		where v.workspace_id = $1::uuid and v.site_id = $2::uuid and ($3::int is null or v.seq < $3) order by v.seq desc limit $4`,
		c.WorkspaceID(), site.ID, in.BeforeSeq, in.Limit)
	if err != nil {
		return nil, command.DB(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (props, error) {
		var id, source, summary, by, name string
		var seq int
		var at time.Time
		var draft, published bool
		if err := r.Scan(&id, &seq, &source, &summary, &by, &name, &at, &draft, &published); err != nil {
			return nil, err
		}
		return props{"id": id, "seq": seq, "source": source, "summary": summary, "created_by": props{"id": by, "name": name},
			"created_at": at, "is_draft": draft, "is_published": published}, nil
	})
	if out == nil {
		out = []props{}
	}
	return out, command.DB(err)
}

// ---------- edit ----------

var editVerb = map[string]string{"update_block_props": "Updated", "add_block": "Added", "remove_block": "Deleted", "move_block": "Moved", "duplicate_block": "Duplicated", "set_theme": "Changed the theme", "set_page": "Changed the page title and description"}

// opSummary is the history line of one toolbar operation.
func opSummary(tool string, input json.RawMessage) string {
	var a struct{ ID, Type, Title, Page, Slug string }
	_ = json.Unmarshal(input, &a)
	switch tool {
	case "add_page":
		return "Added page " + firstNonEmpty(a.Title, a.Slug)
	case "remove_page":
		return "Removed page " + a.Page
	case "set_page":
		if a.Page != "" {
			return "Changed page " + a.Page
		}
	case "set_content":
		return "Updated page " + a.Page
	}
	label := firstNonEmpty(a.Type, strings.SplitN(a.ID, "-", 2)[0])
	return strings.TrimSpace(editVerb[tool] + " " + label)
}

func edit(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		ExpectedRev       *int   `json:"expected_rev"`
		Source            string
		Ops               []struct {
			Tool  string
			Input json.RawMessage
		}
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	source := firstNonEmpty(in.Source, "edit")
	if source != "edit" && source != "editor" {
		return nil, command.ErrInvalid("source must be edit or editor")
	}
	if len(in.Ops) == 0 || len(in.Ops) > maxEditOps {
		return nil, command.ErrInvalid("send between 1 and 20 operations")
	}
	site, data, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if err := checkDraft(c, site, in.ExpectedVersionID); err != nil {
		return nil, err
	}
	needsRev := source == "editor"
	for _, op := range in.Ops {
		if op.Tool == "set_content" {
			needsRev = true
		}
	}
	if needsRev && in.ExpectedRev == nil {
		return nil, command.ErrInvalid("expected_rev is required")
	}
	if in.ExpectedRev != nil && *in.ExpectedRev != site.Rev {
		return nil, command.ErrConflict(conflictMsg)
	}
	var slots map[string]string
	working := data
	var summaries []string
	for _, op := range in.Ops {
		tool, input := op.Tool, op.Input
		if tool == "duplicate_block" {
			if tool, input, err = duplicateOp(working, input); err != nil {
				return nil, command.ErrInvalid(err.Error())
			}
		}
		if tool == "add_page" && slots == nil {
			b, err := business(c)
			if err != nil {
				return nil, err
			}
			slots = templateSlots(b, deref(site.URL))
		}
		next, err := applyWith(working, tool, input, slots)
		if err != nil {
			return nil, command.ErrInvalid(op.Tool + ": " + err.Error())
		}
		working = next
		summaries = append(summaries, opSummary(op.Tool, op.Input))
	}
	if sameJSON(data, working) {
		return props{"site": site, "data": data}, nil
	}
	summary := strings.Join(summaries, ", ")
	if source == "editor" {
		if parent, ok, err := coalescible(c, site); err != nil {
			return nil, err
		} else if ok {
			if err := replaceDraft(c, site, parent, working); err != nil {
				return nil, err
			}
			site, working, err = loadSite(c, site.ID, false)
			return props{"site": site, "data": working}, err
		}
		summary = describeDoc(data, working)
	}
	if _, err := addVersion(c, site.ID, &site.DraftVersionID, nil, source, summary, working); err != nil {
		return nil, err
	}
	site, working, err = loadSite(c, site.ID, false)
	return props{"site": site, "data": working}, err
}

// coalescible says whether the draft is the same person's editor save from the last five minutes that nothing else
// depends on: not published (now or ever), no turn based on or producing it, no child (design 17.12). parent is the
// version it was saved on top of, or nil.
func coalescible(c *command.Ctx, site siteOut) (parent json.RawMessage, ok bool, err error) {
	user, err := userID(c)
	if err != nil {
		return nil, false, err
	}
	var parentID *string
	err = c.Tx.QueryRow(c.Context, `select v.parent_id::text from sites.site_versions v
		where v.workspace_id = $1::uuid and v.site_id = $2::uuid and v.id = $3::uuid and v.source = 'editor' and v.created_by = $4::uuid
		  and v.updated_at > now() - $5::interval
		  and v.id is distinct from (select published_version_id from sites.sites where workspace_id = $1::uuid and id = $2::uuid)
		  and not exists (select 1 from sites.site_turns t where t.workspace_id = $1::uuid and t.site_id = $2::uuid and (t.base_version_id = v.id or t.version_id = v.id))
		  and not exists (select 1 from sites.site_versions k where k.workspace_id = $1::uuid and k.site_id = $2::uuid and k.parent_id = v.id)
		  and not exists (select 1 from core.events e where e.workspace_id = $1::uuid and e.kind = 'site.published' and e.payload->>'version_id' = v.id::text)`,
		c.WorkspaceID(), site.ID, site.DraftVersionID, user, coalesceWindow).Scan(&parentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, command.DB(err)
	}
	if parentID != nil {
		if parent, err = versionData(c, site.ID, *parentID); err != nil {
			return nil, false, err
		}
	}
	return parent, true, nil
}

// replaceDraft overwrites the draft version's document (a coalesced editor save); parent is what it sat on top of.
func replaceDraft(c *command.Ctx, site siteOut, parent, data json.RawMessage) error {
	if err := Validate(data); err != nil {
		return command.ErrInvalid(err.Error())
	}
	if err := checkImages(c, site.ID, data); err != nil {
		return err
	}
	if err := checkVersionPages(c, site.ID, &site.DraftVersionID, data); err != nil {
		return err
	}
	base := parent
	if base == nil {
		base = data
	}
	if _, err := c.Tx.Exec(c.Context, `update sites.site_versions set data = $3, summary = $4, updated_at = now(), rev = rev + 1
		where workspace_id = $1::uuid and site_id = $2::uuid and id = $5::uuid`, c.WorkspaceID(), site.ID, []byte(data), describeDoc(base, data), site.DraftVersionID); err != nil {
		return command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update sites.sites set updated_at = now() where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), site.ID); err != nil {
		return command.DB(err)
	}
	_, err := c.Emit("site.version_updated", nil, props{"site_id": site.ID, "version_id": site.DraftVersionID, "source": "editor"})
	return err
}

// checkVersionPages applies the plan's page limit to a new document on top of parent.
func checkVersionPages(c *command.Ctx, siteID string, parent *string, data json.RawMessage) error {
	after := pageCount(data)
	var pro bool
	if after > 0 || parent != nil {
		if err := c.Tx.QueryRow(c.Context, "select coalesce((select pro from sites.site_platform where site_id = $1::uuid), false)", siteID).Scan(&pro); err != nil {
			return command.DB(err)
		}
	}
	if after <= pageLimit(pro) {
		return nil
	}
	before := 0
	if parent != nil {
		old, err := versionData(c, siteID, *parent)
		if err != nil {
			return err
		}
		before = pageCount(old)
	}
	return checkPageGrowth(pro, before, after)
}

// guardPublish refuses to publish a draft with more pages than the plan allows (extra pages stay editable).
func guardPublish(inner command.Handler) command.Handler {
	return func(c *command.Ctx, raw json.RawMessage) (any, error) {
		var in struct{ ID string }
		if decode(raw, &in) == nil {
			if site, data, err := loadSite(c, in.ID, true); err == nil {
				if n, lim := pageCount(data), pageLimit(site.Pro); n > lim {
					return nil, command.NewError(command.PlanLimit, fmt.Sprintf("This site has %d extra pages but your plan allows %d. Remove pages to publish.", n, lim))
				}
			}
		}
		return inner(c, raw)
	}
}

// checkDraft refuses a stale view of the draft and any change while a chat turn is in flight.
func checkDraft(c *command.Ctx, site siteOut, expected string) error {
	if expected != site.DraftVersionID {
		return command.ErrConflict(conflictMsg)
	}
	if busy, err := activeTurn(c, site.ID); err != nil {
		return err
	} else if busy {
		return command.ErrConflict(busyMsg)
	}
	return nil
}

// duplicateOp turns duplicate_block {id} into an add_block just after the original, with a fresh id.
func duplicateOp(data, input json.RawMessage) (string, json.RawMessage, error) {
	var in struct{ ID string }
	var d map[string]any
	if json.Unmarshal(input, &in) != nil || json.Unmarshal(data, &d) != nil {
		return "", nil, errors.New("invalid input")
	}
	slug, i, ok := locate(d, in.ID)
	if !ok {
		return "", nil, errors.New("unknown block id")
	}
	b := asMap(contentOf(d, slug)[i])
	p := props{}
	for k, v := range asMap(b["props"]) {
		if k != "id" {
			p[k] = v
		}
	}
	out, _ := json.Marshal(props{"type": b["type"], "props": p, "index": i + 1, "page": slug})
	return "add_block", out, nil
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// describeDoc is the history line of an editor save: what differs between two documents.
func describeDoc(before, after json.RawMessage) string {
	var b, a map[string]any
	if json.Unmarshal(before, &b) != nil || json.Unmarshal(after, &a) != nil {
		return "Saved"
	}
	var out []string
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	titles := func(d map[string]any) map[string]string {
		m := map[string]string{}
		for _, p := range pageMaps(d) {
			m[fmt.Sprint(p["slug"])] = fmt.Sprint(p["title"])
		}
		return m
	}
	bt, at := titles(b), titles(a)
	for slug, t := range at {
		if _, ok := bt[slug]; !ok {
			add("Added page " + t)
		}
	}
	for slug, t := range bt {
		if _, ok := at[slug]; !ok {
			add("Removed page " + t)
		}
	}
	ids := func(content []any) (order []string, byID map[string]map[string]any) {
		byID = map[string]map[string]any{}
		for _, x := range content {
			m := asMap(x)
			id := fmt.Sprint(asMap(m["props"])["id"])
			order = append(order, id)
			byID[id] = m
		}
		return
	}
	for _, slug := range slugsOf(a) {
		if slug != "" {
			if _, ok := bt[slug]; !ok {
				continue
			}
			if findPage(a, slug)["title"] != findPage(b, slug)["title"] || findPage(a, slug)["description"] != findPage(b, slug)["description"] {
				add("Changed page " + fmt.Sprint(findPage(a, slug)["title"]))
			}
		}
		ao, am := ids(contentOf(a, slug))
		bo, bm := ids(contentOf(b, slug))
		var common []string
		for _, id := range ao {
			if _, ok := bm[id]; !ok {
				add("Added " + fmt.Sprint(am[id]["type"]))
			} else {
				common = append(common, id)
				if !reflect.DeepEqual(am[id], bm[id]) {
					add("Updated " + fmt.Sprint(am[id]["type"]))
				}
			}
		}
		var bcommon []string
		for _, id := range bo {
			if _, ok := am[id]; !ok {
				add("Deleted " + fmt.Sprint(bm[id]["type"]))
			} else {
				bcommon = append(bcommon, id)
			}
		}
		if !slices.Equal(common, bcommon) {
			add("Reordered sections")
		}
	}
	ar, br := asMap(asMap(a["root"])["props"]), asMap(asMap(b["root"])["props"])
	if ar["title"] != br["title"] || ar["description"] != br["description"] {
		add("Changed the page title and description")
	}
	if !reflect.DeepEqual(ar["theme"], br["theme"]) {
		add("Changed the theme")
	}
	if len(out) == 0 {
		return "Saved"
	}
	if len(out) > 3 {
		out = append(out[:3], fmt.Sprintf("and %d more", len(out)-3))
	}
	return strings.Join(out, ", ")
}

// ---------- restore ----------

func restore(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID                string
		ExpectedVersionID string `json:"expected_version_id"`
		VersionID         string `json:"version_id"`
		AsNew             bool   `json:"as_new"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, data, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if err := checkDraft(c, site, in.ExpectedVersionID); err != nil {
		return nil, err
	}
	if in.VersionID == site.DraftVersionID {
		return props{"site": site, "data": data}, nil
	}
	target, err := versionData(c, site.ID, in.VersionID)
	if err != nil {
		return nil, err
	}
	if in.AsNew {
		var seq int
		if err := c.Tx.QueryRow(c.Context, "select seq from sites.site_versions where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid", c.WorkspaceID(), site.ID, in.VersionID).Scan(&seq); err != nil {
			return nil, command.DB(err)
		}
		if _, err := addVersion(c, site.ID, &site.DraftVersionID, nil, "restore", fmt.Sprintf("Restored version %d", seq), target); err != nil {
			return nil, err
		}
	} else {
		if err := checkPageGrowth(site.Pro, pageCount(data), pageCount(target)); err != nil {
			return nil, err
		}
		if err := moveDraft(c, site.ID, &site.DraftVersionID, in.VersionID); err != nil {
			return nil, err
		}
	}
	site, _, err = loadSite(c, site.ID, false)
	return props{"site": site, "data": target}, err
}

// ---------- pruning ----------

// PruneVersions deletes versions beyond each site's newest 200, except the draft, the published one, any version that
// was ever published (site.published events), and any a chat turn points at. Children are re-parented first.
func PruneVersions(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, ws := range wss {
		if err := pruneWorkspace(ctx, pool, ws); err != nil {
			return fmt.Errorf("sites prune versions: %w", err)
		}
	}
	return nil
}

func pruneWorkspace(ctx context.Context, pool *pgxpool.Pool, ws string) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `select v.id::text from sites.site_versions v join sites.sites s on s.workspace_id = v.workspace_id and s.id = v.site_id
		where v.seq <= (select max(x.seq) from sites.site_versions x where x.site_id = v.site_id) - $1
		  and v.id <> s.draft_version_id and v.id is distinct from s.published_version_id
		  and not exists (select 1 from sites.site_turns t where t.site_id = v.site_id and (t.base_version_id = v.id or t.version_id = v.id))
		  and not exists (select 1 from core.events e where e.kind = 'site.published' and e.payload->>'version_id' = v.id::text)
		order by v.seq`, keepVersions)
	if err != nil {
		return err
	}
	doomed, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, id := range doomed { // oldest first: a child is re-parented to the grandparent before the parent goes
		if _, err := tx.Exec(ctx, "update sites.site_versions set parent_id = (select parent_id from sites.site_versions where id = $1::uuid) where parent_id = $1::uuid", id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "delete from sites.site_versions where id = $1::uuid", id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
