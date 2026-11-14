// Package hooks is the outbound side of crates/cnote-ai/src/hooks.rs: event fan-out, the hook.deliver
// job (signed HTTP POST or MCP tool call), retries and the schedule sweep. The hook.* commands stay in Rust.
package hooks

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/jobs"
	"github.com/cnote/api/internal/netguard"
	"github.com/cnote/api/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxAttempts = 5
	timeout     = 20 * time.Second
)

func enqueue(ctx context.Context, tx pgx.Tx, ws, kind string, payload any, runAt *time.Time) error {
	b, err := command.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,$3::jsonb,coalesce($4::timestamptz, now()))",
		ws, kind, string(b), runAt)
	return command.DB(err)
}

// OnEvent queues one delivery per enabled hook whose prefixes match. Bodies are built now, so retries resend the same bytes.
func OnEvent(c *command.Ctx, e jobs.Event) error {
	rows, err := c.Tx.Query(c.Context, `select id::text from core.hooks
		where enabled and exists (select 1 from unnest(events) p where p = '*' or starts_with($1, p))`, e.Kind)
	if err != nil {
		return command.DB(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return command.DB(err)
	}
	if len(ids) == 0 {
		return nil
	}
	payload := e.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	payload, err = command.Norm(payload)
	if err != nil {
		return err
	}
	ev := map[string]any{
		"id": e.ID, "workspace_id": e.WorkspaceID, "note_id": e.NoteID, "kind": e.Kind, "actor_type": e.ActorType,
		"actor_id": e.ActorID, "on_behalf_of": e.OnBehalfOf, "payload": payload, "created_at": command.Ts(e.CreatedAt),
	}
	for _, id := range ids {
		body := map[string]any{"type": "event", "hook_id": id, "workspace_id": e.WorkspaceID, "event": ev}
		if err := enqueue(c.Context, c.Tx, e.WorkspaceID, "hook.deliver", map[string]any{"hook_id": id, "body": body, "attempt": 1}, nil); err != nil {
			return err
		}
	}
	return nil
}

// Sweep advances every due schedule past now (skipping missed runs) and queues one delivery for it.
func Sweep(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, "select core.all_workspace_ids()::text")
	if err != nil {
		return err
	}
	wss, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, ws := range wss {
		if err := sweepOne(ctx, pool, ws); err != nil {
			return err
		}
	}
	return nil
}

func sweepOne(ctx context.Context, pool *pgxpool.Pool, ws string) error {
	tx, err := db.BeginTenant(ctx, pool, ws)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := sweepTx(ctx, tx, ws); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sweepTx(ctx context.Context, tx pgx.Tx, ws string) error {
	rows, err := tx.Query(ctx, `with due as (select id, next_run_at from core.hooks
			where enabled and every_minutes is not null and next_run_at <= now() for update skip locked)
		update core.hooks h
		   set next_run_at = due.next_run_at + make_interval(mins => h.every_minutes
		         * (floor(extract(epoch from now() - due.next_run_at) / 60 / h.every_minutes)::int + 1))
		from due where h.id = due.id
		returning h.id::text, h.name, due.next_run_at`)
	if err != nil {
		return err
	}
	type dueRow struct {
		ID, Name string
		At       time.Time
	}
	due, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (d dueRow, err error) {
		err = r.Scan(&d.ID, &d.Name, &d.At)
		return
	})
	if err != nil {
		return err
	}
	for _, d := range due {
		body := map[string]any{"type": "schedule", "hook_id": d.ID, "workspace_id": ws, "name": d.Name, "scheduled_for": command.Ts(d.At)}
		if err := enqueue(ctx, tx, ws, "hook.deliver", map[string]any{"hook_id": d.ID, "body": body, "attempt": 1}, nil); err != nil {
			return err
		}
	}
	return nil
}

// DeliverJob is the hook.deliver job handler.
func DeliverJob(c *command.Ctx, payload json.RawMessage) error {
	var p struct {
		HookID  string          `json:"hook_id"`
		Body    json.RawMessage `json:"body"`
		Attempt json.Number     `json:"attempt"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || !validUUID(p.HookID) {
		return command.ErrInvalid("hook_id")
	}
	attempt, err := p.Attempt.Int64()
	if err != nil {
		attempt = 1
	}
	body, err := command.Norm(orNull(p.Body))
	if err != nil {
		return command.ErrInvalid("body")
	}
	derr, err := deliver(c, p.HookID, body)
	if err != nil {
		return err
	}
	if derr != "" && attempt < maxAttempts {
		at := time.Now().Add(time.Duration(int64(1)<<attempt) * time.Minute)
		retry := map[string]any{"hook_id": p.HookID, "body": body, "attempt": attempt + 1}
		return enqueue(c.Context, c.Tx, c.Principal.WorkspaceID, "hook.deliver", retry, &at)
	}
	return nil
}

func orNull(b json.RawMessage) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	return b
}

func validUUID(s string) bool {
	var u [16]byte
	h := strings.ReplaceAll(s, "-", "")
	_, err := hex.Decode(u[:], []byte(h))
	return err == nil && len(h) == 32 && len(s) == 36
}

// errText is CmdError's Display: "{code:?}: {message}".
func errText(err error) string {
	var ce *command.Error
	if errors.As(err, &ce) {
		return strings.ToUpper(string(ce.Code[:1])) + string(ce.Code[1:]) + ": " + ce.Message
	}
	return err.Error()
}

type target struct {
	Target, URL string
	Tool        *string
	Arguments   json.RawMessage
	SecretEnc   []byte
}

// deliver returns (delivery error text or "", database error). A missing or paused hook is a no-op.
func deliver(c *command.Ctx, id string, body json.RawMessage) (string, error) {
	var t target
	err := c.Tx.QueryRow(c.Context, "select target, url, tool, arguments, secret_enc from core.hooks where id = $1::uuid and enabled", id).
		Scan(&t.Target, &t.URL, &t.Tool, &t.Arguments, &t.SecretEnc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", command.DB(err)
	}
	ferr := run(c, t, body)
	status, lastErr := "ok", (*string)(nil)
	if ferr != "" {
		status, lastErr = "failed", &ferr
	}
	_, err = c.Tx.Exec(c.Context, "update core.hooks set last_run_at = now(), last_status = $2, last_error = $3 where id = $1::uuid", id, status, lastErr)
	return ferr, command.DB(err)
}

func run(c *command.Ctx, t target, body json.RawMessage) string {
	// DNS may have changed since the hook was saved.
	if err := netguard.CheckEndpoint(c.Context, t.URL); err != nil {
		return strings.ReplaceAll(err.Error(), "base_url", "url")
	}
	var secret *string
	if t.SecretEnc != nil {
		key, err := secretbox.MasterKey()
		if err == nil {
			var s string
			if s, err = secretbox.Decrypt(key, c.Principal.WorkspaceID, t.SecretEnc); err == nil {
				secret = &s
			}
		}
		if err != nil {
			return errText(err)
		}
	}
	ctx, cancel := context.WithTimeout(c.Context, timeout)
	defer cancel()
	var err error
	if t.Target == "mcp" {
		err = callMCP(ctx, t, secret, body)
	} else {
		s := ""
		if secret != nil {
			s = *secret
		}
		err = postSigned(ctx, t.URL, s, body)
	}
	switch {
	case err == nil:
		return ""
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed out"
	}
	return err.Error()
}

// Signature is "sha256=" + hex(HMAC-SHA256(secret, "{ts}.{body}")).
func Signature(secret string, ts int64, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10) + "." + body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func postSigned(ctx context.Context, url, secret string, body json.RawMessage) error {
	raw := string(body)
	ts := time.Now().Unix()
	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(raw))
	if err != nil {
		return fmt.Errorf("request failed: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-cnote-timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("x-cnote-signature", Signature(secret, ts, raw))
	res, err := netguard.Client().Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %v", err)
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return nil
}
