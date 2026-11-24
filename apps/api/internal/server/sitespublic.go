package server

// Public and preview endpoints of the website builder (docs/design/site-builder-phase1.md 17.4 to 17.7):
//
//	POST /api/sites/public_get            server token; the published page for a host (called by the web server)
//	GET  /api/sites/img/{site}/{image}    a photo the published version uses (no session, no cookie)
//	POST /api/sites/form/{site}           the contact form
//	GET  /api/w/{ws}/sites/{site}/img/{image}   any photo of the site, for members (builder preview)

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/leads"
	"github.com/cnote/api/internal/modules/sites"
)

var draftImgRe = regexp.MustCompile(`^/api/w/([0-9a-f-]{36})/sites/([0-9a-f-]{36})/img/([0-9a-f-]{36})$`)

func isDraftImage(r *http.Request) bool {
	return r.Method == http.MethodGet && draftImgRe.MatchString(r.URL.EscapedPath())
}

func (s *Server) siteFail(w http.ResponseWriter, err error) {
	var ce *command.Error
	if !errors.As(err, &ce) {
		slog.Error("sites public", "error", err)
		ce = command.NewError(command.Internal, "internal error")
	}
	body, _ := command.Marshal(map[string]any{"error": ce})
	writeJSON(w, ce.Code.Status(), body)
}

func (s *Server) sitesPublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	p := r.URL.Path
	switch {
	case p == "/api/sites/public_posts" && r.Method == http.MethodPost:
		s.sitePublicPosts(w, r, false)
	case p == "/api/sites/public_post" && r.Method == http.MethodPost:
		s.sitePublicPosts(w, r, true)
	case p == "/api/sites/public_get" && r.Method == http.MethodPost:
		s.sitePublicGet(w, r)
	case strings.HasPrefix(p, "/api/sites/img/") && (r.Method == http.MethodGet || r.Method == http.MethodHead):
		parts := strings.Split(strings.TrimPrefix(p, "/api/sites/img/"), "/")
		if len(parts) != 2 || !wsRe.MatchString(parts[0]) || !wsRe.MatchString(parts[1]) {
			notFound(w)
			return
		}
		s.sitePublicImage(w, r, parts[0], parts[1])
	case strings.HasPrefix(p, "/api/sites/form/") && r.Method == http.MethodPost:
		if id := strings.TrimPrefix(p, "/api/sites/form/"); wsRe.MatchString(id) {
			s.siteForm(w, r, id)
			return
		}
		notFound(w)
	default:
		notFound(w)
	}
}

func (s *Server) sitePublicGet(w http.ResponseWriter, r *http.Request) {
	want := os.Getenv("SITES_SERVER_TOKEN")
	a, b := sha256.Sum256([]byte(r.Header.Get("x-sites-token"))), sha256.Sum256([]byte(want))
	if want == "" || subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		notFound(w)
		return
	}
	var in struct {
		Host string `json:"host"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 2048)).Decode(&in) != nil {
		notFound(w)
		return
	}
	pub, err := sites.LookupHost(r.Context(), s.Pool, in.Host)
	if err != nil {
		notFound(w)
		return
	}
	body, _ := command.Marshal(sites.PublicJSON(pub))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) writeImage(w http.ResponseWriter, r *http.Request, out []byte, cache string) {
	var img struct {
		ContentType string `json:"content_type"`
		Data        string `json:"data_base64"`
	}
	if json.Unmarshal(out, &img) != nil {
		s.siteFail(w, errors.New("bad image"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		s.siteFail(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", img.ContentType)
	h.Set("Cache-Control", cache)
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *Server) sitePublicImage(w http.ResponseWriter, r *http.Request, site, image string) {
	n, err := db.Bump(r.Context(), s.Pool, "siteimage:ip:"+sites.IPKey(sites.ClientIP(r, sites.TrustedProxies())), false)
	if err != nil {
		s.siteFail(w, command.DB(err))
		return
	}
	if n > 600 {
		s.siteFail(w, command.NewError(command.RateLimited, "Too many image requests. Try again later."))
		return
	}
	ws, err := sites.PublicImage(r.Context(), s.Pool, site, image)
	if err != nil {
		notFound(w)
		return
	}
	var out []byte
	sys := command.Principal{Kind: command.KindSystem, WorkspaceID: ws, Role: command.Member}
	err = s.Registry.WithTx(r.Context(), s.Pool, sys, func(c *command.Ctx) error {
		out, err = c.Call("file.image_get", map[string]string{"site_id": site, "id": image})
		return err
	})
	if err != nil {
		notFound(w)
		return
	}
	s.writeImage(w, r, out, "public, max-age=60")
}

func (s *Server) siteDraftImage(w http.ResponseWriter, r *http.Request) {
	m := draftImgRe.FindStringSubmatch(r.URL.EscapedPath())
	p, err := s.Auth.PrincipalForRequest(r.Context(), r.Header, r.Host, m[1])
	var out []byte
	if err == nil {
		out, err = s.Registry.Execute(r.Context(), s.Pool, "site.image_get", json.RawMessage(`{"id":"`+m[2]+`","image_id":"`+m[3]+`"}`), p, command.ExecOptions{})
	}
	if err != nil {
		s.siteFail(w, err)
		return
	}
	s.writeImage(w, r, out, "private, max-age=60")
}

func (s *Server) siteForm(w http.ResponseWriter, r *http.Request, siteID string) {
	buf, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(buf) > 4096 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	var in struct {
		Name        string `json:"name"`
		Phone       string `json:"phone"`
		Email       string `json:"email"`
		Message     string `json:"message"`
		Consent     bool   `json:"consent"`
		ConsentText string `json:"consent_text"` // ignored: the server states the consent (17.8)
		ConsentID   string `json:"consent_id"`
		ItemID      string `json:"item_id"`
		Honeypot    string `json:"company_website"`
	}
	pub, lerr := sites.LookupID(r.Context(), s.Pool, siteID)
	switch {
	case lerr != nil:
		s.siteFail(w, command.ErrNotFound("site"))
		return
	case json.Unmarshal(buf, &in) != nil:
		s.siteFail(w, command.ErrInvalid("invalid request"))
		return
	case in.Honeypot != "": // a bot gets the same answer as a person and nothing is stored
		writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
		return
	case !in.Consent:
		s.siteFail(w, command.ErrInvalid("please tick the box to agree before sending"))
		return
	case utf8.RuneCountInString(in.Message) > 1000 || len(in.ConsentID) > 40 || len(in.ItemID) > 40:
		s.siteFail(w, command.ErrInvalid("the message is too long"))
		return
	}
	consent, _, okConsent := sites.ConsentFor(in.ConsentID, pub.Name)
	if !okConsent {
		s.siteFail(w, command.ErrInvalid("unknown consent text"))
		return
	}
	q, _, err := leads.Clean(leads.Inquiry{Name: in.Name, Phone: in.Phone, Email: in.Email, Message: in.Message})
	if err == nil {
		err = sites.FormAllowed(r.Context(), s.Pool, siteID, sites.ClientIP(r, sites.TrustedProxies()))
	}
	if err == nil {
		err = sites.Submit(r.Context(), s.Pool, s.Registry, pub, sites.Submission{Name: q.Name, Phone: in.Phone, Email: q.Email, Message: q.Message, ConsentText: consent, ItemID: in.ItemID})
	}
	if err != nil {
		s.siteFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}
