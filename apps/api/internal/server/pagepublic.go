package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/files"
	"github.com/cnote/api/internal/modules/publiclinks"
	"github.com/cnote/api/internal/modules/sites"
	"github.com/cnote/api/internal/secretbox"
)

// The public page endpoints (docs-parity-b 4.2 and 9.1): GET /api/public/page/<token> and
// GET /api/public/page/<token>/files/<id>. The page that calls them is /pg/<token>. The token is the whole credential: it is
// never logged, echoed or put in an event or an error.
const pagePublicPrefix = "/api/public/page/"

// Hourly limits, shared by every replica through core.rate_buckets and counted before any token work.
const (
	pgIPHour    = 300   // per address, page requests
	pgIPFHour   = 1500  // per address, file requests
	pgMissHour  = 20    // per address, unknown or malformed tokens
	pgTokHour   = 2000  // per link, page requests
	pgTokFHour  = 20000 // per link, file requests
	pgAllHour   = 20000 // the platform, page requests
	pgNotFound  = `{"error":{"code":"not_found","message":"This page is not available."}}`
	pgLimited   = `{"error":{"code":"rate_limited","message":"Too many requests right now. Please try again later."}}`
	pgDown      = `{"error":{"code":"unavailable","message":"This page is temporarily unavailable."}}`
	pgInternal  = `{"error":{"code":"internal","message":"internal error"}}`
	pgUUID      = `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
	pgTokenSize = 43
)

var errNoPage = errors.New("page not available")

var pgWarnOnce sync.Once

// warnPagePublic logs, once, the two settings the public page limits depend on.
func warnPagePublic() {
	pgWarnOnce.Do(func() {
		if len(sites.TrustedProxies()) == 0 {
			slog.Warn("PUBLIC_TRUSTED_PROXIES is empty: every reader of a public page behind the Next rewrite shares one address and one hourly limit")
		}
		if _, err := secretbox.MasterKey(); err != nil {
			slog.Warn("CNOTE_MASTER_KEY is not set: public pages answer 503 (the address key must be shared by all replicas)")
		}
	})
}

func pgReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func validPageToken(t string) bool {
	if len(t) != pgTokenSize {
		return false
	}
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// PublicPage is the ONLY shape the page endpoint returns; a golden test pins its key set.
type PublicPage struct {
	Title         string  `json:"title"`
	Icon          *string `json:"icon"`
	Wide          bool    `json:"wide"`
	BodyMD        string  `json:"body_md"`
	UpdatedOn     string  `json:"updated_on"`
	WorkspaceName string  `json:"workspace_name"`
}

type pageFile struct {
	Name, Type string
	Data       []byte
}

type pageResult struct {
	page PublicPage
	file *pageFile
	link string
	ws   string
	note string
	by   string
}

func (s *Server) pagePublic(w http.ResponseWriter, r *http.Request) {
	warnPagePublic()
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Robots-Tag", "noindex, nofollow")
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	ctx := r.Context()
	ipk, err := publicIPKey("page-ip", sites.ClientIP(r, sites.TrustedProxies()))
	if err != nil {
		pgReply(w, http.StatusServiceUnavailable, pgDown)
		return
	}
	fail := func(err error) { // the request line holds the token, so only the error is logged
		slog.Error("public page", "error", err)
		pgReply(w, http.StatusInternalServerError, pgInternal)
	}
	token, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, pagePublicPrefix), "/")
	fileID, isFile := strings.CutPrefix(rest, "files/")
	over := func(key string, max int) (bool, error) {
		n, err := db.Bump(ctx, s.Pool, key, false)
		return n > max, err
	}
	// 1. The address limits come first and count every request, malformed tokens included.
	lims := []struct {
		key string
		max int
	}{{"pg:ip:" + ipk, pgIPHour}, {"pg:all", pgAllHour}}
	if isFile {
		lims = lims[:1]
		lims[0].key, lims[0].max = "pg:ipf:"+ipk, pgIPFHour
	}
	for _, l := range lims {
		limited, err := over(l.key, l.max)
		if err != nil {
			fail(err)
			return
		}
		if limited {
			pgReply(w, http.StatusTooManyRequests, pgLimited)
			return
		}
	}
	miss := func() { // a miss costs the prober; a valid token never touches this bucket
		if _, err := db.Bump(ctx, s.Pool, "pg:miss:"+ipk, false); err != nil {
			slog.Error("public page", "error", err)
		}
		pgReply(w, http.StatusNotFound, pgNotFound)
	}
	if n, err := db.Peek(ctx, s.Pool, "pg:miss:"+ipk, false); err != nil {
		fail(err)
		return
	} else if n >= pgMissHour {
		pgReply(w, http.StatusTooManyRequests, pgLimited)
		return
	}
	// 2. The token. PUBLIC_PAGES=0 turns every page link off.
	if !validPageToken(token) || os.Getenv("PUBLIC_PAGES") == "0" || (rest != "" && (!isFile || !pgUUIDRe.MatchString(fileID))) {
		miss()
		return
	}
	hash := publiclinks.HashToken(token)
	var linkID, ws, note, creator string
	err = s.Pool.QueryRow(ctx, `select id::text, workspace_id::text, note_id::text, space_id::text, created_by::text from core.resolve_page_link($1)`, hash).
		Scan(&linkID, &ws, &note, new(string), &creator)
	if errors.Is(err, pgx.ErrNoRows) {
		miss()
		return
	}
	if err != nil {
		fail(err)
		return
	}
	// 3. The per-link limit, then the transaction.
	key, max := "pg:tok:"+linkID, pgTokHour
	if isFile {
		key, max = "pg:tokf:"+linkID, pgTokFHour
	}
	if limited, err := over(key, max); err != nil {
		fail(err)
		return
	} else if limited {
		pgReply(w, http.StatusTooManyRequests, pgLimited)
		return
	}
	res, err := s.servePage(r, token, hash, linkID, ws, note, creator, fileID)
	if errors.Is(err, errNoPage) {
		miss()
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if res.file != nil {
		files.RecordPolicy.Headers(h, res.file.Name, res.file.Type, res.file.Data)
		h.Set("Cache-Control", "no-store")
		_, _ = w.Write(res.file.Data)
		return
	}
	body, _ := json.Marshal(res.page)
	writeJSON(w, http.StatusOK, body)
	s.countPageView(r, res)
}

var pgUUIDRe = regexp.MustCompile(pgUUID)

// servePage authorizes and reads in ONE transaction that holds the link row FOR SHARE (13.2.4). Revoke and replace take that
// row FOR UPDATE, so a request authorized before the revoke commits may finish, and one that starts after it fails here with
// errNoPage. Everything below the lock is re-checked: the resolver ran outside this transaction and may be stale.
func (s *Server) servePage(r *http.Request, token, hash, linkID, ws, note, creator, fileID string) (pageResult, error) {
	ctx := r.Context()
	res := pageResult{link: linkID, ws: ws, note: note, by: creator}
	p := command.Principal{Kind: command.KindSystem, WorkspaceID: ws, UserID: &creator, Role: command.Member}
	err := s.Registry.WithTx(ctx, s.Pool, p, func(c *command.Ctx) error {
		var by string
		err := c.Tx.QueryRow(ctx, `select created_by::text from core.public_links
			where id = $1::uuid and token_hash = $2 and purpose = 'page' and revoked_at is null
			  and (expires_at is null or expires_at > now()) for share`, linkID, hash).Scan(&by)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNoPage
		}
		if err != nil {
			return err
		}
		var again string
		err = c.Tx.QueryRow(ctx, `select id::text from core.resolve_page_link($1)`, hash).Scan(&again)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && again != linkID {
			return errNoPage
		}
		if err != nil {
			return err
		}
		c.Principal.UserID = &by // the creator now on record, not the one the resolver saw
		ok, err := publiclinks.PageServes(c, note, by)
		if err != nil {
			return err
		}
		if !ok {
			return errNoPage
		}
		var pg PublicPage
		var md string
		if err := c.Tx.QueryRow(ctx, `select n.title, n.icon, n.wide, n.content_md, (n.updated_at at time zone 'UTC')::date::text, w.name
			from core.notes n join core.workspaces w on w.id = n.workspace_id where n.id = $1::uuid`, note).
			Scan(&pg.Title, &pg.Icon, &pg.Wide, &md, &pg.UpdatedOn, &pg.WorkspaceName); err != nil {
			return err
		}
		rows, err := c.Tx.Query(ctx, `select id::text from core.attachments where note_id = $1::uuid`, note)
		if err != nil {
			return err
		}
		own, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		scope := publiclinks.BodyScope{WorkspaceID: ws, NoteID: note, Token: token, OwnFiles: map[string]bool{}}
		for _, id := range own {
			scope.OwnFiles[strings.ToLower(id)] = true
		}
		if pg.BodyMD, err = publiclinks.PublicBody(md, scope); err != nil {
			return err
		}
		res.page = pg
		if fileID == "" {
			return nil
		}
		// A file is served only when it is attached to this page AND the body still shows it.
		fileID = strings.ToLower(fileID)
		if !scope.OwnFiles[fileID] || !strings.Contains(pg.BodyMD, "/api/public/page/"+scope.Token+"/files/"+fileID) {
			return errNoPage
		}
		out, err := c.Call("file.download", map[string]string{"id": fileID})
		if err != nil {
			var ce *command.Error
			if errors.As(err, &ce) && (ce.Code == command.NotFound || ce.Code == command.Forbidden) {
				return errNoPage
			}
			return err
		}
		var f struct {
			Filename    string `json:"filename"`
			ContentType string `json:"content_type"`
			Data        string `json:"data_base64"`
		}
		if err := json.Unmarshal(out, &f); err != nil {
			return err
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return err
		}
		res.file = &pageFile{Name: f.Filename, Type: f.ContentType, Data: data}
		return nil
	})
	return res, err
}

// countPageView counts a page view after the read has committed. It is a separate transaction on purpose: an UPDATE of the
// link row inside the reading transaction would upgrade its FOR SHARE lock and could deadlock with a waiting revoke. Failure
// is logged and does not change the answer. The event is once per link per day, as for billing links.
func (s *Server) countPageView(r *http.Request, res pageResult) {
	p := command.Principal{Kind: command.KindSystem, WorkspaceID: res.ws, UserID: &res.by, Role: command.Member}
	err := s.Registry.WithTx(r.Context(), s.Pool, p, func(c *command.Ctx) error {
		var first bool
		err := c.Tx.QueryRow(c.Context, `select last_viewed_at is null or (last_viewed_at at time zone 'UTC')::date < (now() at time zone 'UTC')::date
			from core.public_links where id = $1::uuid and revoked_at is null for update`, res.link).Scan(&first)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := c.Tx.Exec(c.Context, "update core.public_links set view_count = view_count + 1, last_viewed_at = now() where id = $1::uuid", res.link); err != nil {
			return err
		}
		if first {
			_, err = c.Emit("share.viewed", &res.note, map[string]any{"link_id": res.link, "purpose": "page"})
		}
		return err
	})
	if err != nil {
		slog.Error("public page view count", "error", err)
	}
}
