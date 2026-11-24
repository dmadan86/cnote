package server

// POST /api/sites/report/{site_id}: a visitor reports a published site (design 18.13). No session, no cookie. Body
// {reason: scam|phishing|illegal|impersonation|other, details, url, company_website}; company_website is a honeypot.
// Limits: 3 reports per address per site per day, 500 per hour for the whole platform.

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/sites"
)

const (
	reportPerIPSiteDay = 3
	reportPerHour      = 500
)

var reportReasons = map[string]bool{"scam": true, "phishing": true, "illegal": true, "impersonation": true, "other": true}

func (s *Server) siteReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	siteID := strings.TrimPrefix(r.URL.Path, "/api/sites/report/")
	if !sites.AbuseOn() || !wsRe.MatchString(siteID) {
		notFound(w)
		return
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(buf) > 4096 {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	var in struct {
		Reason   string `json:"reason"`
		Details  string `json:"details"`
		URL      string `json:"url"`
		Honeypot string `json:"company_website"`
	}
	if json.Unmarshal(buf, &in) != nil || !reportReasons[in.Reason] {
		s.siteFail(w, command.ErrInvalid("choose what is wrong with this site"))
		return
	}
	if in.Honeypot != "" { // a bot gets the same answer as a person and nothing is stored
		writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
		return
	}
	clean := func(v string, max int) string {
		v = strings.TrimSpace(strings.Map(func(c rune) rune {
			if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
				return ' '
			}
			return c
		}, v))
		if utf8.RuneCountInString(v) > max {
			v = string([]rune(v)[:max])
		}
		return v
	}
	text := in.Reason
	if d := clean(in.Details, 300); d != "" {
		text += ": " + d
	}
	if u := clean(in.URL, 150); u != "" {
		text += " [" + u + "]"
	}
	if _, err := sites.LookupID(r.Context(), s.Pool, siteID); err != nil {
		notFound(w)
		return
	}
	busy := command.NewError(command.RateLimited, "Too many reports right now. Please try again later.")
	ip := sites.IPKey(sites.ClientIP(r, sites.TrustedProxies()))
	var result string
	err = s.Pool.QueryRow(r.Context(), "select sites.abuse_report_add($1::uuid, $2, $3)", siteID, ip, text).Scan(&result)
	if err != nil {
		s.siteFail(w, command.DB(err))
		return
	}
	if result == "rate_limited" {
		s.siteFail(w, busy)
		return
	}
	if result != "ok" {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, []byte(`{"ok":true}`))
}
