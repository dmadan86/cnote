package server

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/sites"
	"github.com/cnote/api/internal/modules/tables"
	"github.com/cnote/api/internal/secretbox"
)

// The public form endpoint (design 18.2 as revised by 18.8): GET and POST /api/public/table-form/<token>. The page that
// calls it is /tf/<token>. The token is the whole credential: it is never logged, echoed or put in an error.
const tablesFormPrefix = "/api/public/table-form/"

// Hourly limits (shared across replicas through core.rate_buckets, counted before any work).
const (
	formIPHour   = 120  // per address, every request, valid token or not
	formAllHour  = 6000 // the platform
	formMissHour = 20   // per address, unknown or malformed tokens
	formTokHour  = 120  // per form
	formSubHour  = 10   // per address per form, POSTs
	maxPayload   = 64 << 10
)

const (
	formNotFound = `{"error":{"code":"not_found","message":"This form is not available."}}`
	formLimited  = `{"error":{"code":"rate_limited","message":"Too many responses right now. Please try again later."}}`
	formDown     = `{"error":{"code":"unavailable","message":"This form is temporarily unavailable."}}`
	formInternal = `{"error":{"code":"internal","message":"internal error"}}`
)

var formWarnOnce sync.Once

// warnTablesForm logs, once at startup, the two settings the public form limits depend on.
func warnTablesForm() {
	formWarnOnce.Do(func() {
		if len(sites.TrustedProxies()) == 0 {
			slog.Warn("PUBLIC_TRUSTED_PROXIES is empty: every visitor of a public table form behind the Next rewrite shares one address and one hourly limit")
		}
		if _, err := secretbox.MasterKey(); err != nil {
			slog.Warn("CNOTE_MASTER_KEY is not set: public table forms answer 503 (the address key must be shared by all replicas)")
		}
	})
}

// formIPKey is the address key of the public table forms (see publicIPKey).
func formIPKey(ip string) (string, error) { return publicIPKey("table-form-ip", ip) }

func formReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func (s *Server) tablesForm(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w, "GET, POST")
		return
	}
	ctx := r.Context()
	ipk, err := formIPKey(sites.ClientIP(r, sites.TrustedProxies()))
	if err != nil {
		formReply(w, http.StatusServiceUnavailable, formDown)
		return
	}
	fail := func(err error) { // the request line holds the token, so only the error is logged
		slog.Error("table form", "error", err)
		formReply(w, http.StatusInternalServerError, formInternal)
	}
	over := func(key string, max int, day bool) (bool, error) {
		n, err := db.Bump(ctx, s.Pool, key, day)
		return n > max, err
	}
	// 1. The address limits come first and count every request, including malformed tokens.
	for _, l := range []struct {
		key string
		max int
	}{{"tf:ip:" + ipk, formIPHour}, {"tf:all", formAllHour}} {
		limited, err := over(l.key, l.max, false)
		if err != nil {
			fail(err)
			return
		}
		if limited {
			formReply(w, http.StatusTooManyRequests, formLimited)
			return
		}
	}
	miss := func() { // a miss costs the prober; a valid token never touches this bucket
		if _, err := db.Bump(ctx, s.Pool, "tf:miss:"+ipk, false); err != nil {
			slog.Error("table form", "error", err)
		}
		formReply(w, http.StatusNotFound, formNotFound)
	}
	if n, err := db.Peek(ctx, s.Pool, "tf:miss:"+ipk, false); err != nil {
		fail(err)
		return
	} else if n >= formMissHour {
		formReply(w, http.StatusTooManyRequests, formLimited)
		return
	}
	// 2. The token.
	token := strings.TrimPrefix(r.URL.Path, tablesFormPrefix)
	if !tables.ValidFormToken(token) {
		miss()
		return
	}
	hash := tables.HashFormToken(token)
	form, err := tables.ResolveForm(ctx, s.Pool, hash)
	if errors.Is(err, tables.ErrNoForm) {
		miss()
		return
	}
	if err != nil {
		fail(err)
		return
	}
	if r.Method == http.MethodGet {
		body, err := tables.PublicForm(ctx, s.Pool, s.Registry, hash, form)
		if errors.Is(err, tables.ErrNoForm) {
			formReply(w, http.StatusNotFound, formNotFound)
			return
		}
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	// 3. POST: per-form limits, still before the body is read.
	for _, l := range []struct {
		key string
		max int
		day bool
	}{{"tf:tok:" + form.ID, formTokHour, false}, {"tf:sub:" + form.ID + ":" + ipk, formSubHour, false}, {"tf:day:" + form.ID, tables.FormDailyCap(form.Plan), true}} {
		limited, err := over(l.key, l.max, l.day)
		if err != nil {
			fail(err)
			return
		}
		if limited {
			formReply(w, http.StatusTooManyRequests, formLimited)
			return
		}
	}
	payload, err := readFormPayload(w, r)
	if err != nil {
		formReply(w, http.StatusBadRequest, `{"error":{"code":"invalid","message":"invalid request"}}`)
		return
	}
	switch err := tables.SubmitForm(ctx, s.Pool, s.Registry, hash, form, payload); {
	case err == nil:
		formReply(w, http.StatusOK, `{"ok":true}`)
	case errors.Is(err, tables.ErrNoForm):
		formReply(w, http.StatusNotFound, formNotFound)
	default:
		var ce *command.Error
		if !errors.As(err, &ce) || ce.Code == command.Internal {
			fail(err)
			return
		}
		body, _ := command.Marshal(map[string]any{"error": ce})
		writeJSON(w, ce.Code.Status(), body)
	}
}

// readFormPayload reads the JSON body, at most 64 KB. Forms take no files, so there is no multipart route.
func readFormPayload(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPayload)
	return io.ReadAll(r.Body)
}
