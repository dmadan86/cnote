package sites

// Blog (design section 3, revised by 17.2 and 17.3): posts are rows outside the page document. A post body is TipTap
// JSON checked against the grammar below on every write and again before it is served. A published post's edits wait in
// a pending copy until "Update post".

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cnote/api/internal/command"
)

// Plan limits for posts (design section 0): published / all.
const (
	postsPublishedIncluded = 10
	postsPublishedPro      = 200
	postsAllIncluded       = 30
	postsAllPro            = 300
)

func postLimits(pro bool) (published, all int) {
	if pro {
		return postsPublishedPro, postsAllPro
	}
	return postsPublishedIncluded, postsAllIncluded
}

// Post grammar limits (17.2). The body column is below 102400 bytes of jsonb text, which is a little longer than
// compact JSON, so the compact limit sits under it.
const (
	postMaxDepth  = 8
	postMaxNodes  = 2000
	postMaxText   = 40000
	postMaxImages = 20
	postMaxBytes  = 80000
	postAltMax    = 120
)

// postParents lists, per node type, the child types it may hold. A type absent from the map may not exist at all.
var postParents = map[string][]string{
	"doc":         {"paragraph", "heading", "bulletList", "orderedList", "blockquote", "horizontalRule", "image", "siteImage"},
	"paragraph":   {"text", "hardBreak"},
	"heading":     {"text", "hardBreak"},
	"bulletList":  {"listItem"},
	"orderedList": {"listItem"},
	"listItem":    {"paragraph", "bulletList", "orderedList"},
	"blockquote":  {"paragraph", "bulletList", "orderedList"},
	"image":       nil, "siteImage": nil, // both names are accepted; attrs are {image_id, alt} only
	"horizontalRule": nil, "hardBreak": nil, "text": nil,
}

var postAttrs = map[string][]string{
	"heading": {"level"}, "orderedList": {"start"}, "image": {"image_id", "alt"}, "siteImage": {"image_id", "alt"},
}

var postMarks = map[string]bool{"bold": true, "italic": true, "link": true}

// ValidatePost checks a TipTap body against the grammar. images is the set of image ids of this site; a nil map skips
// the ownership check (use it only for syntax checks of what is already stored).
func ValidatePost(body json.RawMessage, images map[string]bool) error {
	_, err := NormalizePost(body, images)
	return err
}

// NormalizePost validates a body and returns it with control characters stripped from text and alt.
func NormalizePost(body json.RawMessage, images map[string]bool) (json.RawMessage, error) {
	if len(body) > postMaxBytes {
		return nil, errors.New("the post is too large")
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, errors.New("the post body is not valid")
	}
	v := &postCheck{images: images}
	if root["type"] != "doc" {
		return nil, errors.New("the post body must be one doc")
	}
	if err := v.node(root, "doc", 1); err != nil {
		return nil, err
	}
	out, err := json.Marshal(root)
	if err != nil || len(out) > postMaxBytes {
		return nil, errors.New("the post is too large")
	}
	return out, nil
}

type postCheck struct {
	images             map[string]bool
	nodes, text, image int
}

func (v *postCheck) node(n map[string]any, want string, depth int) error {
	typ, _ := n["type"].(string)
	kids, known := postParents[typ]
	if !known || typ != want {
		return fmt.Errorf("the post has a part that is not allowed (%q)", typ)
	}
	if depth > postMaxDepth {
		return errors.New("the post is nested too deeply")
	}
	if v.nodes++; v.nodes > postMaxNodes {
		return errors.New("the post has too many parts")
	}
	for k := range n {
		if k != "type" && k != "attrs" && k != "content" && k != "text" && k != "marks" {
			return fmt.Errorf("the post has an unknown field %q", k)
		}
	}
	if err := v.attrs(typ, n); err != nil {
		return err
	}
	content, hasContent := n["content"]
	if typ == "text" {
		return v.leafText(n, hasContent)
	}
	if _, has := n["text"]; has || n["marks"] != nil {
		return fmt.Errorf("%s cannot hold text or marks", typ)
	}
	if !hasContent {
		return nil
	}
	list, ok := content.([]any)
	if !ok || len(kids) == 0 {
		return fmt.Errorf("%s cannot hold content", typ)
	}
	for _, c := range list {
		m, ok := c.(map[string]any)
		if !ok {
			return errors.New("the post body is not valid")
		}
		ct, _ := m["type"].(string)
		if !contains(kids, ct) {
			return fmt.Errorf("%s cannot hold %q", typ, ct)
		}
		if ct == "image" || ct == "siteImage" {
			if v.image++; v.image > postMaxImages {
				return fmt.Errorf("a post can hold at most %d images", postMaxImages)
			}
		}
		if err := v.node(m, ct, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// attrs allows only the named attributes, each with a checked value.
func (v *postCheck) attrs(typ string, n map[string]any) error {
	raw, has := n["attrs"]
	if !has || raw == nil {
		if typ == "heading" || typ == "image" || typ == "siteImage" {
			return fmt.Errorf("%s needs its attributes", typ)
		}
		delete(n, "attrs")
		return nil
	}
	a, ok := raw.(map[string]any)
	if !ok {
		return errors.New("the post body is not valid")
	}
	for k := range a {
		if !contains(postAttrs[typ], k) {
			return fmt.Errorf("%s has an attribute that is not allowed (%q)", typ, k)
		}
	}
	switch typ {
	case "heading":
		if l, _ := a["level"].(float64); l != 2 && l != 3 {
			return errors.New("headings may be level 2 or 3")
		}
	case "orderedList":
		if s, ok := a["start"]; ok {
			if f, isNum := s.(float64); !isNum || f < 1 || f > 9999 || f != float64(int(f)) {
				return errors.New("the list start is not valid")
			}
		}
	case "image", "siteImage":
		id, _ := a["image_id"].(string)
		if !imageIDPattern.MatchString(id) {
			return errors.New("an image in the post is not valid")
		}
		if v.images != nil && !v.images[id] {
			return errors.New("the post uses a photo that is not on this website")
		}
		alt, _ := a["alt"].(string)
		if alt = stripControl(alt); utf8.RuneCountInString(alt) > postAltMax {
			return fmt.Errorf("an image description may be at most %d characters", postAltMax)
		}
		a["alt"] = alt
	}
	return nil
}

func (v *postCheck) leafText(n map[string]any, hasContent bool) error {
	s, ok := n["text"].(string)
	if !ok || hasContent {
		return errors.New("a text part needs text and nothing else")
	}
	s = stripControl(s)
	if s == "" {
		return errors.New("a text part is empty")
	}
	if v.text += utf8.RuneCountInString(s); v.text > postMaxText {
		return fmt.Errorf("a post can hold at most %d characters", postMaxText)
	}
	n["text"] = s
	marks, has := n["marks"]
	if !has || marks == nil {
		delete(n, "marks")
		return nil
	}
	list, ok := marks.([]any)
	if !ok {
		return errors.New("the post body is not valid")
	}
	seen := map[string]bool{}
	for _, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return errors.New("the post body is not valid")
		}
		typ, _ := m["type"].(string)
		if !postMarks[typ] {
			return fmt.Errorf("the post has a style that is not allowed (%q)", typ)
		}
		if seen[typ] {
			return fmt.Errorf("the post repeats the %s style", typ)
		}
		seen[typ] = true
		for k := range m {
			if k != "type" && k != "attrs" {
				return fmt.Errorf("the post has an unknown field %q", k)
			}
		}
		if typ == "link" {
			if err := postLink(m["attrs"]); err != nil {
				return err
			}
		} else if m["attrs"] != nil {
			return fmt.Errorf("%s takes no attributes", typ)
		}
	}
	return nil
}

// postLink applies the Phase 0 link rule: http(s), mailto, tel, or an address on this site ("/" or "/<slug>").
func postLink(raw any) error {
	a, ok := raw.(map[string]any)
	if !ok || len(a) != 1 {
		return errors.New("a link needs an address and nothing else")
	}
	href, _ := a["href"].(string)
	if href == "" {
		return errors.New("a link needs an address")
	}
	slugs := map[string]bool{}
	if m := internalLink.FindStringSubmatch(href); m != nil {
		slugs[m[1]] = true // any well-formed page address; a page that is gone is a 404, not an unsafe link
	}
	if strings.HasPrefix(href, "#") {
		return errors.New("a link may not point inside the page")
	}
	if err := validateFieldIn("link", href, field{Kind: "link", Max: 300}, slugs); err != nil {
		return errors.New("a link in the post is not valid")
	}
	return nil
}

func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// postText is the plain text of a body, for descriptions.
func postText(body json.RawMessage) string {
	var b strings.Builder
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if s, ok := n["text"].(string); ok {
			b.WriteString(s)
			b.WriteByte(' ')
		}
		if l, ok := n["content"].([]any); ok {
			for _, c := range l {
				if m, ok := c.(map[string]any); ok {
					walk(m)
				}
			}
		}
	}
	var root map[string]any
	if json.Unmarshal(body, &root) == nil {
		walk(root)
	}
	return strings.TrimSpace(b.String())
}

// ---------- storage ----------

type postOut struct {
	ID           string          `json:"id"`
	SiteID       string          `json:"site_id"`
	WorkspaceID  string          `json:"workspace_id"`
	Slug         string          `json:"slug"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	CoverImageID *string         `json:"cover_image_id"`
	OGImageID    *string         `json:"og_image_id"`
	Body         json.RawMessage `json:"body,omitempty"`
	Pending      json.RawMessage `json:"pending"`
	Status       string          `json:"status"`
	PublishedAt  *time.Time      `json:"published_at"`
	Rev          int             `json:"rev"`
	CreatedBy    string          `json:"created_by"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	HasPending   bool            `json:"has_pending"`
	AI           bool            `json:"ai"`
}

// postFields is what a save writes: the live fields of a draft, or the pending copy of a published post.
type postFields struct {
	Slug         string          `json:"slug"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	CoverImageID *string         `json:"cover_image_id"`
	Body         json.RawMessage `json:"body"`
}

const postCols = `id::text, site_id::text, workspace_id::text, slug, title, description, cover_image_id::text, og_image_id::text, body, pending,
	status, published_at, rev, created_by::text, created_at, updated_at, ai`

func scanPost(r pgx.Row, withBody bool) (postOut, error) {
	var p postOut
	var body, pending []byte
	err := r.Scan(&p.ID, &p.SiteID, &p.WorkspaceID, &p.Slug, &p.Title, &p.Description, &p.CoverImageID, &p.OGImageID, &body, &pending,
		&p.Status, &p.PublishedAt, &p.Rev, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt, &p.AI)
	if err != nil {
		return p, err
	}
	if withBody {
		p.Body = body
	}
	if len(pending) > 0 && string(pending) != "null" {
		p.Pending, p.HasPending = pending, true
		var f postFields
		if json.Unmarshal(pending, &f) == nil { // the editor sees the newest text; pending says it is not live
			p.Slug, p.Title, p.Description, p.CoverImageID = f.Slug, f.Title, f.Description, f.CoverImageID
			if withBody {
				p.Body = f.Body
			}
		}
	}
	return p, nil
}

func loadPost(c *command.Ctx, siteID, postID string, lock bool) (postOut, error) {
	if err := uid("post_id", postID); err != nil {
		return postOut{}, err
	}
	q := "select " + postCols + " from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid"
	if lock {
		q += " for update"
	}
	p, err := scanPost(c.Tx.QueryRow(c.Context, q, c.WorkspaceID(), siteID, postID), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, command.ErrNotFound("post")
	}
	return p, command.DB(err)
}

var postSlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func checkPostFields(c *command.Ctx, siteID string, f *postFields) error {
	f.Title = strings.TrimSpace(f.Title)
	if n := utf8.RuneCountInString(f.Title); n < 1 || n > 120 {
		return command.ErrInvalid("the title must be 1 to 120 characters")
	}
	f.Description = strings.TrimSpace(f.Description)
	if utf8.RuneCountInString(f.Description) > 160 {
		return command.ErrInvalid("the description may be at most 160 characters")
	}
	if f.Slug = strings.ToLower(strings.TrimSpace(f.Slug)); f.Slug == "" {
		f.Slug = slugifyPost(f.Title)
	}
	if len(f.Slug) > 60 || !postSlugRe.MatchString(f.Slug) || f.Slug == "page" {
		return command.ErrInvalid("the address may use only a-z, 0-9 and hyphens, up to 60 characters")
	}
	owned, err := siteImages(c, siteID)
	if err != nil {
		return err
	}
	if f.CoverImageID != nil {
		id := strings.ToLower(*f.CoverImageID)
		if id == "" {
			f.CoverImageID = nil
		} else if !owned[id] {
			return command.ErrInvalid("the cover photo is not on this website")
		} else {
			f.CoverImageID = &id
		}
	}
	if len(f.Body) == 0 {
		f.Body = json.RawMessage(`{"type":"doc","content":[{"type":"paragraph"}]}`)
	}
	body, err := NormalizePost(f.Body, owned)
	if err != nil {
		return command.ErrInvalid(err.Error())
	}
	f.Body = body
	return nil
}

func slugifyPost(title string) string {
	s := slugify(title)
	if len(s) > 60 {
		s = strings.TrimRight(s[:60], "-")
	}
	if s == "" {
		s = "post"
	}
	return s
}

// freeSlug returns base, or base-2, base-3 ... when another post holds it.
func freeSlug(c *command.Ctx, siteID, base string) (string, error) {
	for n := 1; ; n++ {
		s := base
		if n > 1 {
			suffix := fmt.Sprintf("-%d", n)
			if len(s)+len(suffix) > 60 {
				s = strings.TrimRight(s[:60-len(suffix)], "-")
			}
			s += suffix
		}
		var taken bool
		if err := c.Tx.QueryRow(c.Context, "select exists (select 1 from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and slug = $3 and slug <> 'page')", c.WorkspaceID(), siteID, s).Scan(&taken); err != nil {
			return "", command.DB(err)
		}
		if !taken && s != "page" {
			return s, nil
		}
	}
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// savePostDraft writes a post: a new draft, a draft updated in place, or the pending copy of a published post. The
// caller has locked the site row. expectedRev is required for an existing post and checked under the post row lock.
func savePostDraft(c *command.Ctx, site siteOut, postID string, expectedRev *int, f postFields) (postOut, error) {
	user, err := userID(c)
	if err != nil {
		return postOut{}, err
	}
	explicitSlug := strings.TrimSpace(f.Slug) != ""
	var cur postOut
	if postID != "" {
		if expectedRev == nil {
			return postOut{}, command.ErrInvalid("expected_rev is required")
		}
		if cur, err = loadPost(c, site.ID, postID, true); err != nil {
			return postOut{}, err
		}
		if cur.Rev != *expectedRev {
			return postOut{}, command.ErrConflict("The post changed. Reload to see the latest.")
		}
		if !explicitSlug {
			f.Slug = cur.Slug // an edit never renames the address by itself
		}
	}
	if err := checkPostFields(c, site.ID, &f); err != nil {
		return postOut{}, err
	}
	if postID == "" {
		_, all := postLimits(site.Pro)
		var n int
		if err := c.Tx.QueryRow(c.Context, "select count(*) from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid", c.WorkspaceID(), site.ID).Scan(&n); err != nil {
			return postOut{}, command.DB(err)
		}
		if n >= all {
			return postOut{}, command.NewError(command.PlanLimit, fmt.Sprintf("Your plan allows %d posts. Delete one to add another.", all))
		}
		if !explicitSlug {
			if f.Slug, err = freeSlug(c, site.ID, f.Slug); err != nil {
				return postOut{}, err
			}
		}
		p, err := scanPost(c.Tx.QueryRow(c.Context, `insert into sites.posts (workspace_id, site_id, slug, title, description, cover_image_id, body, created_by)
			values ($1::uuid, $2::uuid, $3, $4, $5, $6::uuid, $7, $8::uuid) returning `+postCols,
			c.WorkspaceID(), site.ID, f.Slug, f.Title, f.Description, f.CoverImageID, []byte(f.Body), user), true)
		if isUniqueViolation(err) {
			return postOut{}, command.ErrConflict("Another post already uses that address.")
		}
		return p, command.DB(err)
	}
	var row pgx.Row
	if cur.Status == "published" {
		pend, _ := json.Marshal(f)
		row = c.Tx.QueryRow(c.Context, `update sites.posts set pending = $4, rev = rev + 1, updated_at = now()
			where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid and rev = $5 returning `+postCols, c.WorkspaceID(), site.ID, postID, pend, *expectedRev)
	} else {
		row = c.Tx.QueryRow(c.Context, `update sites.posts set slug = $4, title = $5, description = $6, cover_image_id = $7::uuid, body = $8, pending = null, rev = rev + 1, updated_at = now()
			where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid and rev = $9 returning `+postCols,
			c.WorkspaceID(), site.ID, postID, f.Slug, f.Title, f.Description, f.CoverImageID, []byte(f.Body), *expectedRev)
	}
	p, err := scanPost(row, true)
	if isUniqueViolation(err) {
		return postOut{}, command.ErrConflict("Another post already uses that address.")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return postOut{}, command.ErrConflict("The post changed. Reload to see the latest.")
	}
	if err == nil && cur.Status == "published" {
		var taken bool // a courtesy check now; promotion checks again
		if e := c.Tx.QueryRow(c.Context, "select exists (select 1 from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and slug = $3 and id <> $4::uuid)", c.WorkspaceID(), site.ID, f.Slug, postID).Scan(&taken); e == nil && taken {
			return postOut{}, command.ErrConflict("Another post already uses that address.")
		}
	}
	return p, command.DB(err)
}

// ---------- commands ----------

func BlogCommands() []command.Command {
	R, W := command.GroupRead, command.GroupWriteItems
	id := str("site id")
	post := str("post id")
	rev := props{"type": "integer", "description": "the revision the person is looking at; a stale one is a conflict"}
	body := props{"type": "object", "description": "TipTap JSON: doc, paragraph, heading 2-3, bulletList, orderedList, listItem, blockquote, hardBreak, horizontalRule, image {image_id, alt}, text with bold, italic, link {href}"}
	return []command.Command{
		mk("site.post_list", R, command.Member, false, "List the posts of the website, newest first, without bodies.",
			obj([]string{"id"}, props{"id": id, "status": str("draft or published"), "before": str("an updated_at from the previous page"), "limit": props{"type": "integer"}}), postList),
		mk("site.post_get", R, command.Member, false, "One post with its body. A published post with changes waiting shows those changes and has_pending set.",
			obj([]string{"id", "post_id"}, props{"id": id, "post_id": post}), postGet),
		mk("site.post_save", W, command.Member, false, "Create a post, or save one. A draft is updated in place; a published post's change waits as a pending copy until it is published again.",
			obj([]string{"id", "title", "body"}, props{"id": id, "post_id": post, "expected_rev": rev, "title": str("1 to 120 characters"), "slug": str("a-z, 0-9, hyphens"), "description": str("up to 160 characters"), "cover_image_id": str("a photo of this website"), "body": body}), postSave),
		mk("site.post_publish", W, command.Admin, true, "Publish a post, put it live with its pending changes, or move it back to drafts.",
			obj([]string{"id", "post_id", "expected_rev", "publish"}, props{"id": id, "post_id": post, "expected_rev": rev, "publish": props{"type": "boolean"}, "published_at": str("RFC 3339; now or in the past")}), postPublish),
		mk("site.post_delete", W, command.Admin, false, "Delete a post. A published post leaves the website at once.",
			obj([]string{"id", "post_id", "expected_rev"}, props{"id": id, "post_id": post, "expected_rev": rev}), postDelete),
	}
}

func postList(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID, Status, Before string
		Limit              int
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	if in.Status != "" && in.Status != "draft" && in.Status != "published" {
		return nil, command.ErrInvalid("status must be draft or published")
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 50
	}
	var before *time.Time
	if in.Before != "" {
		t, err := time.Parse(time.RFC3339Nano, in.Before)
		if err != nil {
			return nil, command.ErrInvalid("before must be a time")
		}
		before = &t
	}
	rows, err := c.Tx.Query(c.Context, "select "+postCols+` from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid
		and ($3 = '' or status = $3) and ($4::timestamptz is null or updated_at < $4) order by updated_at desc, id limit $5`, c.WorkspaceID(), site.ID, in.Status, before, in.Limit)
	if err != nil {
		return nil, command.DB(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (postOut, error) { return scanPost(r, false) })
	if out == nil {
		out = []postOut{}
	}
	return out, command.DB(err)
}

func postGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID     string
		PostID string `json:"post_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	return loadPost(c, site.ID, in.PostID, false)
}

func postSave(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID, Title, Slug, Description string
		PostID                       string          `json:"post_id"`
		ExpectedRev                  *int            `json:"expected_rev"`
		CoverImageID                 *string         `json:"cover_image_id"`
		Body                         json.RawMessage `json:"body"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, true) // the site row lock orders the post count against other saves
	if err != nil {
		return nil, err
	}
	return savePostDraft(c, site, in.PostID, in.ExpectedRev, postFields{Slug: in.Slug, Title: in.Title, Description: in.Description, CoverImageID: in.CoverImageID, Body: in.Body})
}

func postPublish(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID          string
		PostID      string `json:"post_id"`
		ExpectedRev *int   `json:"expected_rev"`
		Publish     bool
		PublishedAt string `json:"published_at"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.ExpectedRev == nil {
		return nil, command.ErrInvalid("expected_rev is required")
	}
	site, data, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	cur, err := loadPost(c, site.ID, in.PostID, true)
	if err != nil {
		return nil, err
	}
	if cur.Rev != *in.ExpectedRev {
		return nil, command.ErrConflict("The post changed. Reload to see the latest.")
	}
	var row pgx.Row
	if !in.Publish {
		// back to drafts: the newest text (pending over live) becomes the draft
		f := postFields{Slug: cur.Slug, Title: cur.Title, Description: cur.Description, CoverImageID: cur.CoverImageID, Body: cur.Body}
		row = c.Tx.QueryRow(c.Context, `update sites.posts set status = 'draft', slug = $4, title = $5, description = $6, cover_image_id = $7::uuid, body = $8, pending = null,
			og_image_id = null, rev = rev + 1, updated_at = now() where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid and rev = $9 returning `+postCols,
			c.WorkspaceID(), site.ID, cur.ID, f.Slug, f.Title, f.Description, f.CoverImageID, []byte(f.Body), cur.Rev)
	} else {
		if site.TakenDown != nil {
			return nil, command.ErrForbidden(site.TakenDown.Reason)
		}
		if site.PublishedVersionID == nil {
			return nil, command.ErrConflict("Publish your website first, then publish posts.")
		}
		if err := checkNoBlogPage(c, site, data); err != nil {
			return nil, err
		}
		if cur.Status != "published" {
			pubLimit, _ := postLimits(site.Pro)
			var n int
			if err := c.Tx.QueryRow(c.Context, "select count(*) from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and status = 'published'", c.WorkspaceID(), site.ID).Scan(&n); err != nil {
				return nil, command.DB(err)
			}
			if n >= pubLimit {
				return nil, command.NewError(command.PlanLimit, fmt.Sprintf("Your plan allows %d published posts. Move one back to drafts first.", pubLimit))
			}
		}
		var at *time.Time
		if in.PublishedAt != "" {
			t, err := time.Parse(time.RFC3339, in.PublishedAt)
			if err != nil || t.After(time.Now()) {
				return nil, command.ErrInvalid("published_at must be now or in the past")
			}
			at = &t
		}
		// promotion: the newest text goes live; the slug is checked again by the unique constraint
		owned, err := siteImages(c, site.ID)
		if err != nil {
			return nil, err
		}
		body, err := NormalizePost(cur.Body, owned)
		if err != nil {
			return nil, command.ErrInvalid(err.Error())
		}
		if cur.CoverImageID != nil && !owned[*cur.CoverImageID] {
			return nil, command.ErrInvalid("the cover photo is not on this website")
		}
		row = c.Tx.QueryRow(c.Context, `update sites.posts set status = 'published', slug = $4, title = $5, description = $6, cover_image_id = $7::uuid, body = $8, pending = null,
			published_at = coalesce($9::timestamptz, published_at, now()), rev = rev + 1, updated_at = now()
			where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid and rev = $10 returning `+postCols,
			c.WorkspaceID(), site.ID, cur.ID, cur.Slug, cur.Title, cur.Description, cur.CoverImageID, []byte(body), at, cur.Rev)
	}
	p, err := scanPost(row, true)
	if isUniqueViolation(err) {
		return nil, command.ErrConflict("Another post already uses that address. Change the address and publish again.")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("The post changed. Reload to see the latest.")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if in.Publish {
		if err := enqueueOG(c, site.ID, "", p.ID); err != nil {
			return nil, err
		}
	} else if err := pruneGenerated(c, site.ID); err != nil {
		return nil, err
	}
	if _, err := c.Emit("site.post_published", nil, props{"site_id": site.ID, "post_id": p.ID, "publish": in.Publish}); err != nil {
		return nil, err
	}
	return p, nil
}

func postDelete(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID          string
		PostID      string `json:"post_id"`
		ExpectedRev *int   `json:"expected_rev"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.ExpectedRev == nil {
		return nil, command.ErrInvalid("expected_rev is required")
	}
	site, _, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	cur, err := loadPost(c, site.ID, in.PostID, true)
	if err != nil {
		return nil, err
	}
	if cur.Rev != *in.ExpectedRev {
		return nil, command.ErrConflict("The post changed. Reload to see the latest.")
	}
	if _, err := c.Tx.Exec(c.Context, "delete from sites.posts where workspace_id = $1::uuid and site_id = $2::uuid and id = $3::uuid and rev = $4", c.WorkspaceID(), site.ID, cur.ID, cur.Rev); err != nil {
		return nil, command.DB(err)
	}
	if err := pruneGenerated(c, site.ID); err != nil {
		return nil, err
	}
	return props{"deleted": true}, nil
}

// checkNoBlogPage refuses the blog while the site has a page called blog, in the draft or the published version (17.4).
func checkNoBlogPage(c *command.Ctx, site siteOut, draft json.RawMessage) error {
	has := func(data json.RawMessage) bool {
		var d map[string]any
		return json.Unmarshal(data, &d) == nil && findPage(d, "blog") != nil
	}
	busy := has(draft)
	if !busy && site.PublishedVersionID != nil {
		if data, err := versionData(c, site.ID, *site.PublishedVersionID); err == nil {
			busy = has(data)
		}
	}
	if busy {
		return command.ErrConflict("Your website has a page with the address \"blog\". Rename that page before using the blog.")
	}
	return nil
}
