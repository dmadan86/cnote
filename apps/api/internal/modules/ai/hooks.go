package ai

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// Outbound hooks (crates/cnote-ai/src/hooks.rs): the five Admin commands. Queued deliveries, retries and the
// schedule sweep stay in the Rust worker (hook.deliver job, hooks.schedule sweep); hook.test delivers inline.

const hookTimeout = 20 * time.Second

func hookCommands() []command.Command {
	mk := func(name string, secret bool, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupAdmin, h)
		c.MinRole, c.ReturnsSecret = command.Admin, secret
		return c
	}
	return []command.Command{
		mk("hook.list", false, hookList),
		mk("hook.create", true, hookCreate),
		mk("hook.set_enabled", false, hookSetEnabled),
		mk("hook.delete", false, hookDelete),
		mk("hook.test", false, hookTest),
	}
}

type hookOut struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Target       string          `json:"target"`
	URL          string          `json:"url"`
	Tool         *string         `json:"tool"`
	Arguments    json.RawMessage `json:"arguments"`
	Events       []string        `json:"events"`
	EveryMinutes *int32          `json:"every_minutes"`
	NextRunAt    *command.Ts     `json:"next_run_at"`
	Enabled      bool            `json:"enabled"`
	LastRunAt    *command.Ts     `json:"last_run_at"`
	LastStatus   *string         `json:"last_status"`
	LastError    *string         `json:"last_error"`
	CreatedAt    command.Ts      `json:"created_at"`
}

func hookList(c *command.Ctx, input json.RawMessage) (any, error) {
	if err := emptyObj(input); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context,
		`select id::text, name, target, url, tool, arguments, events, every_minutes, next_run_at, enabled,
		        last_run_at, last_status, last_error, created_at
		 from core.hooks order by created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []hookOut{}
	for rows.Next() {
		var h hookOut
		var args []byte
		var next, last *time.Time
		var created time.Time
		if err := rows.Scan(&h.ID, &h.Name, &h.Target, &h.URL, &h.Tool, &args, &h.Events, &h.EveryMinutes, &next, &h.Enabled,
			&last, &h.LastStatus, &h.LastError, &created); err != nil {
			return nil, err
		}
		if h.Arguments, err = command.Norm(args); err != nil {
			return nil, err
		}
		if h.Events == nil {
			h.Events = []string{}
		}
		h.NextRunAt, h.LastRunAt, h.CreatedAt = command.TsPtr(next), command.TsPtr(last), command.Ts(created)
		out = append(out, h)
	}
	return out, rows.Err()
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hookCreate(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	name, err := o.ReqStr("name")
	if err != nil {
		return nil, err
	}
	target, err := o.ReqStr("target")
	if err != nil {
		return nil, err
	}
	url, err := o.ReqStr("url")
	if err != nil {
		return nil, err
	}
	tool, err := o.OptStr("tool")
	if err != nil {
		return nil, err
	}
	args, err := o.OptValue("arguments")
	if err != nil {
		return nil, err
	}
	token, err := o.OptStr("token")
	if err != nil {
		return nil, err
	}
	events, err := o.Strings("events")
	if err != nil {
		return nil, err
	}
	every, err := o.OptInt32("every_minutes")
	if err != nil {
		return nil, err
	}
	startAt, err := optTime(o, "start_at")
	if err != nil {
		return nil, err
	}

	bad := func(m string) (any, error) { return nil, command.ErrInvalid(m) }
	if strings.TrimSpace(name) == "" || len(name) > 80 {
		return bad("name must be 1-80 characters")
	}
	if len(events) > 20 || anyOf(events, func(e string) bool {
		return e == "" || len(e) > 60 || strings.IndexFunc(e, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r == '.' || r == '_' || r == '*') }) >= 0
	}) {
		return bad("events must be up to 20 kind prefixes like 'item.created' or 'note.' ('*' for all)")
	}
	if every != nil && (*every < 5 || *every > 525_600) {
		return bad("every_minutes must be between 5 and 525600")
	}
	if len(events) == 0 && every == nil {
		return bad("give events, every_minutes, or both")
	}
	switch target {
	case "http":
		if tool != nil || token != nil || args != nil {
			return bad("tool, token and arguments are only for target 'mcp'")
		}
	case "mcp":
		if tool == nil || strings.TrimSpace(*tool) == "" {
			return bad("tool is required for target 'mcp'")
		}
		if args != nil && bytes.TrimSpace(args)[0] != '{' {
			return bad("arguments must be an object")
		}
	default:
		return bad("target must be 'http' or 'mcp'")
	}
	if err := CheckEndpoint(c.Context, url); err != nil {
		return nil, endpointErr(errors.New(strings.ReplaceAll(err.Error(), "base_url", "url")), command.ErrInvalid)
	}
	ws := c.Principal.WorkspaceID
	var signing *string
	if target == "http" {
		s := "whsec_" + randomToken(24)
		signing = &s
	}
	secret := signing
	if secret == nil && token != nil && *token != "" {
		secret = token
	}
	var secretEnc []byte
	if secret != nil {
		mk, err := MasterKey()
		if err != nil {
			return nil, err
		}
		if secretEnc, err = Encrypt(mk, ws, *secret); err != nil {
			return nil, err
		}
	}
	var next *time.Time
	if every != nil {
		t := time.Now().UTC().Add(time.Duration(*every) * time.Minute)
		if startAt != nil {
			t = *startAt
		}
		next = &t
	}
	if args == nil {
		args = json.RawMessage(`{}`)
	}
	var id string
	if err := c.Tx.QueryRow(c.Context,
		`insert into core.hooks (workspace_id, name, target, url, tool, arguments, secret_enc, events, every_minutes,
		                         next_run_at, created_by)
		 values ($1::uuid,$2,$3,$4,$5,$6,$7,$8::text[],$9,$10,$11::uuid) returning id::text`,
		ws, strings.TrimSpace(name), target, url, tool, args, secretEnc, events, every, next, c.Principal.UserID).Scan(&id); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "signing_secret": signing}, nil
}

func anyOf(s []string, f func(string) bool) bool {
	for _, x := range s {
		if f(x) {
			return true
		}
	}
	return false
}

// optTime is Option<DateTime<Utc>>; anything this strict RFC 3339 parser rejects goes to Rust.
func optTime(o notes.Obj, k string) (*time.Time, error) {
	s, err := o.OptStr(k)
	if err != nil || s == nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return nil, command.ErrFallback
	}
	t = t.UTC()
	return &t, nil
}

func hookSetEnabled(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("id")
	if err != nil {
		return nil, err
	}
	enabled, err := o.ReqBool("enabled")
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, "update core.hooks set enabled = $2 where id = $1::uuid", id, enabled)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, command.ErrNotFound("hook")
	}
	return map[string]any{"id": id, "enabled": enabled}, nil
}

func hookDelete(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("id")
	if err != nil {
		return nil, err
	}
	tag, err := c.Tx.Exec(c.Context, "delete from core.hooks where id = $1::uuid", id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": tag.RowsAffected() > 0}, nil
}

func hookTest(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := notes.ParseObj(input)
	if err != nil {
		return nil, err
	}
	id, err := o.ReqUUID("id")
	if err != nil {
		return nil, err
	}
	body := map[string]any{"type": "test", "hook_id": id, "workspace_id": c.Principal.WorkspaceID, "sent_at": command.Ts(time.Now().UTC())}
	derr, err := deliver(c, id, body)
	if err != nil {
		return nil, err
	}
	if derr != nil {
		return map[string]any{"ok": false, "error": derr.Error()}, nil
	}
	return map[string]any{"ok": true}, nil
}

// deliver: the outer error is database trouble; the inner one means the delivery failed (also recorded on the
// hook). A missing or paused hook is a no-op.
func deliver(c *command.Ctx, id string, body any) (delivery error, err error) {
	var target, url string
	var tool *string
	var args, secretEnc []byte
	e := c.Tx.QueryRow(c.Context, "select target, url, tool, arguments, secret_enc from core.hooks where id = $1::uuid and enabled", id).
		Scan(&target, &url, &tool, &args, &secretEnc)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	ws := c.Principal.WorkspaceID
	delivery = func() error {
		if err := CheckEndpoint(c.Context, url); err != nil { // DNS may have changed since the hook was saved
			if errors.Is(err, errUnparsable) {
				return errors.New("invalid url")
			}
			return errors.New(strings.ReplaceAll(err.Error(), "base_url", "url"))
		}
		var secret *string
		if secretEnc != nil {
			mk, err := MasterKey()
			if err == nil {
				var s string
				if s, err = Decrypt(mk, ws, secretEnc); err == nil {
					secret = &s
				}
			}
			if err != nil {
				return err
			}
		}
		ctx, cancel := context.WithTimeout(c.Context, hookTimeout)
		defer cancel()
		var err error
		if target == "mcp" {
			err = callMCP(ctx, url, deref(tool), args, secret, body)
		} else {
			err = postSigned(ctx, url, deref(secret), body)
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("timed out")
		}
		return err
	}()
	status, lastErr := "ok", (*string)(nil)
	if delivery != nil {
		m := delivery.Error()
		status, lastErr = "failed", &m
	}
	_, err = c.Tx.Exec(c.Context, "update core.hooks set last_run_at = now(), last_status = $2, last_error = $3 where id = $1::uuid", id, status, lastErr)
	return delivery, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Signature is X-CNote-Signature: sha256=hex(HMAC(secret, "{timestamp}.{body}")).
func Signature(secret string, ts int64, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	_, _ = m.Write([]byte(strconv.FormatInt(ts, 10) + "." + body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func postSigned(ctx context.Context, url, secret string, body any) error {
	raw, _ := command.Marshal(body)
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("request failed: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-cnote-timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("x-cnote-signature", Signature(secret, ts, string(raw)))
	res, err := httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("request failed: error sending request for url (%s)", url)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		return nil
	}
	return fmt.Errorf("HTTP %d", res.StatusCode)
}
