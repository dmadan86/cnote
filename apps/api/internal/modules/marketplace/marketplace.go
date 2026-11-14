// Package marketplace ports the marketplace command surface; OAuth exchange remains in Rust.
package marketplace

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

func Commands() []command.Command {
	names := []string{"market.list", "market.get", "market.install", "market.uninstall", "market.installed", "dev.apps", "dev.app_save", "dev.app_submit", "dev.app_rotate_secret", "dev.app_delete", "market.review_queue", "market.review"}
	out := []command.Command{}
	for _, name := range names {
		group := command.GroupAdmin
		if slices.Contains([]string{"market.list", "market.get", "market.installed"}, name) {
			group = command.GroupRead
		}
		n := name
		cmd := command.NewCommand(n, group, func(c *command.Ctx, b json.RawMessage) (any, error) { return run(c, n, b) })
		if group == command.GroupAdmin {
			cmd.MinRole = command.Admin
		}
		cmd.ReturnsSecret = n == "dev.app_save" || n == "dev.app_rotate_secret"
		out = append(out, cmd)
	}
	return out
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type input map[string]json.RawMessage

func (i input) str(k string, optional, def bool) (*string, error) {
	b, ok := i[k]
	if !ok {
		if optional {
			return nil, nil
		}
		if def {
			s := ""
			return &s, nil
		}
		return nil, command.ErrFallback
	}
	if string(b) == "null" && optional {
		return nil, nil
	}
	var s string
	if string(b) == "null" || json.Unmarshal(b, &s) != nil || strings.ContainsRune(s, '�') {
		return nil, command.ErrFallback
	}
	return &s, nil
}
func (i input) id(k string, opt bool) (*string, error) {
	s, e := i.str(k, opt, false)
	if e != nil || s == nil {
		return s, e
	}
	if !uuidRE.MatchString(*s) {
		return nil, command.ErrFallback
	}
	v := strings.ToLower(*s)
	return &v, nil
}
func person(c *command.Ctx) error {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return command.ErrForbidden("only people can manage apps")
	}
	return nil
}
func platform(c *command.Ctx) error {
	if e := person(c); e != nil {
		return e
	}
	var email string
	if e := c.Tx.QueryRow(c.Context, platformEmailSQL, c.Principal.UserID).Scan(&email); e != nil {
		return e
	}
	for _, v := range strings.Split(os.Getenv("CNOTE_PLATFORM_ADMINS"), ",") {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" && (v == email || (strings.HasPrefix(v, "@") && strings.HasSuffix(email, v))) {
			return nil
		}
	}
	return command.ErrForbidden("platform admins only")
}
func token(prefix string, n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}
func hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func many(c *command.Ctx, q string, args ...any) (any, error) {
	rows, e := c.Tx.Query(c.Context, q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
func one(c *command.Ctx, q string, args ...any) (map[string]any, error) {
	var b []byte
	e := c.Tx.QueryRow(c.Context, q, args...).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("app")
	}
	if e != nil {
		return nil, e
	}
	var m map[string]any
	e = json.Unmarshal(b, &m)
	return m, e
}
func run(c *command.Ctx, name string, b json.RawMessage) (any, error) {
	var i input
	if !utf8.Valid(b) || json.Unmarshal(b, &i) != nil || i == nil {
		return nil, command.ErrFallback
	}
	// sqlx uses UTC sessions; jsonb timestamp rendering depends on the session timezone.
	if _, err := c.Tx.Exec(c.Context, "set local timezone = 'UTC'"); err != nil {
		return nil, err
	}
	ws := c.WorkspaceID()
	switch name {
	case "market.list":
		q, e := i.str("q", true, false)
		if e != nil {
			return nil, e
		}
		v := ""
		if q != nil {
			v = "%" + strings.TrimSpace(*q) + "%"
		}
		return many(c, listSQL, v)
	case "market.get":
		slug, e := i.str("slug", false, false)
		if e != nil {
			return nil, e
		}
		a, e := one(c, getSQL, slug, ws)
		if e != nil {
			return nil, e
		}
		var settings map[string]any
		var raw []byte
		if e = c.Tx.QueryRow(c.Context, settingsSQL, ws).Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &settings); e != nil {
			return nil, e
		}
		enabled := []string{"read", "write_notes", "write_items", "manage_crm"}
		if arr, ok := settings["agent_tool_groups"].([]any); ok {
			enabled = []string{}
			for _, v := range arr {
				if s, ok := v.(string); ok {
					enabled = append(enabled, s)
				}
			}
		}
		blocked := []string{}
		for _, v := range a["scopes"].([]any) {
			if !slices.Contains(enabled, v.(string)) {
				blocked = append(blocked, v.(string))
			}
		}
		a["blocked_scopes"] = blocked
		return a, nil
	case "market.installed":
		return many(c, installedSQL)
	case "dev.apps":
		if e := person(c); e != nil {
			return nil, e
		}
		return many(c, fullAppSQL+"from core.market_apps a where publisher_workspace_id = $1 order by created_at desc", ws)
	case "market.review_queue":
		if e := platform(c); e != nil {
			return nil, e
		}
		return many(c, fullAppSQL+"from core.market_apps a where status = 'review' order by updated_at")
	case "dev.app_save":
		return save(c, i)
	}
	key := "id"
	if name == "market.install" || name == "market.uninstall" {
		key = "app_id"
	}
	id, e := i.id(key, false)
	if e != nil {
		return nil, e
	}
	var approve bool
	var note *string
	if name == "market.review" {
		v, ok := i["approve"]
		if !ok || string(v) == "null" || json.Unmarshal(v, &approve) != nil {
			return nil, command.ErrFallback
		}
		note, e = i.str("note", true, false)
		if e != nil {
			return nil, e
		}
		e = platform(c)
	} else {
		e = person(c)
	}
	if e != nil {
		return nil, e
	}
	switch name {
	case "market.install":
		var appName, redirect string
		var scopes []string
		e = c.Tx.QueryRow(c.Context, installAppSQL, id, ws).Scan(&appName, &redirect, &scopes)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("app")
		}
		if e != nil {
			return nil, e
		}
		var exists bool
		if e = c.Tx.QueryRow(c.Context, installedExistsSQL, id).Scan(&exists); e != nil {
			return nil, e
		}
		if exists {
			return nil, command.ErrConflict("app is already installed")
		}
		var agent, install string
		if e = c.Tx.QueryRow(c.Context, insertAgentSQL, ws, "App: "+appName, c.Principal.UserID, scopes).Scan(&agent); e != nil {
			return nil, e
		}
		if e = c.Tx.QueryRow(c.Context, insertInstallSQL, ws, id, agent, c.Principal.UserID, scopes).Scan(&install); e != nil {
			return nil, e
		}
		code, e := token("cnic_", 24)
		if e != nil {
			return nil, e
		}
		if _, e = c.Tx.Exec(c.Context, insertCodeSQL, hash(code), install, ws, int32(10)); e != nil {
			return nil, e
		}
		if _, e = c.Emit("market.installed", nil, map[string]any{"app_id": *id, "install_id": install}); e != nil {
			return nil, e
		}
		sep := "?"
		if strings.Contains(redirect, "?") {
			sep = "&"
		}
		return map[string]any{"install_id": install, "redirect_to": redirect + sep + "code=" + code + "&workspace_id=" + ws}, nil
	case "market.uninstall":
		tag, e := c.Tx.Exec(c.Context, uninstallSQL, id)
		if e != nil {
			return nil, e
		}
		ok := tag.RowsAffected() == 1
		if ok {
			if _, e = c.Emit("market.uninstalled", nil, map[string]any{"app_id": *id}); e != nil {
				return nil, e
			}
		}
		return map[string]any{"uninstalled": ok}, nil
	case "dev.app_submit":
		tag, e := c.Tx.Exec(c.Context, submitSQL, id, ws)
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() == 0 {
			return nil, command.ErrConflict("only your draft or rejected apps can be submitted")
		}
		return map[string]any{"status": "review"}, nil
	case "dev.app_rotate_secret":
		secret, e := token("cnas_", 32)
		if e != nil {
			return nil, e
		}
		tag, e := c.Tx.Exec(c.Context, rotateSecretSQL, id, ws, hash(secret))
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() == 0 {
			return nil, command.ErrNotFound("app")
		}
		return map[string]any{"client_secret": secret}, nil
	case "dev.app_delete":
		var n int64
		e := c.Tx.QueryRow(c.Context, installCountSQL, id, ws).Scan(&n)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, command.ErrNotFound("app")
		}
		if e != nil {
			return nil, e
		}
		if n > 0 {
			return nil, command.ErrConflict("app still has installs")
		}
		_, e = c.Tx.Exec(c.Context, deleteAppSQL, id)
		return map[string]any{"deleted": true}, e
	case "market.review":
		status := "rejected"
		if approve {
			status = "published"
		}
		tag, e := c.Tx.Exec(c.Context, reviewSQL, id, status, note)
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() == 0 {
			return nil, command.ErrNotFound("app in review")
		}
		return map[string]any{"status": status}, nil
	}
	return nil, command.ErrFallback
}
func save(c *command.Ctx, i input) (any, error) {
	id, e := i.id("id", true)
	if e != nil {
		return nil, e
	}
	v := map[string]*string{}
	for _, k := range []string{"slug", "name", "tagline", "description", "category", "icon_url", "homepage_url", "redirect_url"} {
		v[k], e = i.str(k, k == "icon_url" || k == "homepage_url", k == "tagline" || k == "description")
		if e != nil {
			return nil, e
		}
	}
	var scopes []string
	raw, ok := i["scopes"]
	if !ok || string(raw) == "null" || json.Unmarshal(raw, &scopes) != nil {
		return nil, command.ErrFallback
	}
	var elems []json.RawMessage
	_ = json.Unmarshal(raw, &elems)
	for _, x := range elems {
		if string(x) == "null" {
			return nil, command.ErrFallback
		}
	}
	if e = person(c); e != nil {
		return nil, e
	}
	slug := *v["slug"]
	if !slugRE.MatchString(slug) {
		return nil, command.ErrInvalid("slug must be 3-40 chars of a-z, 0-9 and '-'")
	}
	name := strings.TrimSpace(*v["name"])
	if n := utf8.RuneCountInString(name); n < 1 || n > 60 {
		return nil, command.ErrInvalid("name must be 1-60 characters")
	}
	if utf8.RuneCountInString(*v["tagline"]) > 120 || utf8.RuneCountInString(*v["description"]) > 4000 {
		return nil, command.ErrInvalid("tagline max 120 and description max 4000 characters")
	}
	if !slices.Contains([]string{"productivity", "sales", "finance", "developer", "communication", "other"}, *v["category"]) {
		return nil, command.ErrInvalid("category must be one of productivity, sales, finance, developer, communication, other")
	}
	for _, k := range []string{"icon_url", "homepage_url"} {
		if u := v[k]; u != nil && !https(*u) {
			return nil, command.ErrInvalid(k + " must be an https URL")
		}
	}
	redirect := *v["redirect_url"]
	valid := https(redirect)
	if strings.HasPrefix(redirect, "http://") && strings.IndexFunc(redirect, unicode.IsSpace) < 0 {
		h := strings.TrimPrefix(redirect, "http://")
		if n := strings.IndexAny(h, "/?#"); n >= 0 {
			h = h[:n]
		}
		if n := strings.LastIndex(h, ":"); n >= 0 {
			h = h[:n]
		}
		valid = h == "localhost" || h == "127.0.0.1"
	}
	if !valid {
		return nil, command.ErrInvalid("redirect_url must be https (http only for localhost)")
	}
	if !slices.Contains(scopes, "read") {
		return nil, command.ErrInvalid("scopes must include read")
	}
	for _, s := range scopes {
		if strings.ContainsRune(s, '�') {
			return nil, command.ErrFallback
		}
		if !slices.Contains([]string{"read", "write_notes", "write_items", "manage_crm", "manage_invoices", "send_messages"}, s) {
			return nil, command.ErrInvalid("scope " + s + " cannot be granted to apps")
		}
	}
	var taken bool
	if e = c.Tx.QueryRow(c.Context, slugTakenSQL, slug, id).Scan(&taken); e != nil {
		return nil, e
	}
	if taken {
		return nil, command.ErrConflict("slug is already taken")
	}
	ws := c.WorkspaceID()
	if id != nil {
		tag, e := c.Tx.Exec(c.Context, updateAppSQL, id, ws, slug, name, v["tagline"], v["description"], v["category"], v["icon_url"], v["homepage_url"], redirect, scopes)
		if e != nil {
			return nil, e
		}
		if tag.RowsAffected() == 0 {
			return nil, command.ErrNotFound("app")
		}
		return map[string]any{"app_id": *id}, nil
	}
	var publisher string
	if e = c.Tx.QueryRow(c.Context, publisherSQL, ws).Scan(&publisher); e != nil {
		return nil, e
	}
	secret, e := token("cnas_", 32)
	if e != nil {
		return nil, e
	}
	var appID string
	e = c.Tx.QueryRow(c.Context, insertAppSQL, slug, name, v["tagline"], v["description"], v["category"], v["icon_url"], v["homepage_url"], redirect, scopes, ws, publisher, hash(secret)).Scan(&appID)
	return map[string]any{"app_id": appID, "client_secret": secret}, e
}
func https(s string) bool {
	return strings.HasPrefix(s, "https://") && len(s) > 8 && strings.IndexFunc(s, unicode.IsSpace) < 0
}
