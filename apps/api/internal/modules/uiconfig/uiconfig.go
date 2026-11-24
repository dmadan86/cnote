// Package uiconfig stores each person's screen settings (home widget layout, list columns) in core.ui_configs.
// It owns that table, imports no other module and no other module reads it, so it can move to its own service by
// routing the ui_config. prefix. The server does not know any screen's shape: it checks ownership and bounds only.
package uiconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
	"github.com/jackc/pgx/v5"
)

const (
	MaxBody     = 32 << 10 // request body, checked before decoding
	MaxConfig   = 16 << 10 // stored config, serialized
	MaxDepth    = 6
	MaxString   = 200
	MaxArray    = 100
	MaxScreens  = 200 // rows per person per workspace
	setBurst    = 30
	setPerSec   = 1
	quotaReason = "you have saved settings for 200 screens already; reset one first"
)

var screenRe = regexp.MustCompile(`^[a-z0-9_.:-]{1,64}$`)

func Commands() []command.Command {
	mk := func(name string, g command.Group, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = command.Guest // guests may arrange their own screens
		return c
	}
	return []command.Command{
		mk("ui_config.get", command.GroupRead, get),
		mk("ui_config.set", command.GroupWriteNotes, set),
		mk("ui_config.reset", command.GroupWriteNotes, reset),
	}
}

// owner is the only identity a config ever has: people, never API keys or agents (an agent carries its person's id, so the kind decides).
func owner(c *command.Ctx) (string, error) {
	if c.Principal.Kind != command.KindUser || c.Principal.UserID == nil {
		return "", command.ErrForbidden("screen settings belong to a person, not an API key or agent")
	}
	return *c.Principal.UserID, nil
}

func decode(input json.RawMessage, v any) error {
	if len(input) > MaxBody {
		return command.ErrInvalid("request is too large")
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	if err := json.Unmarshal(input, v); err != nil {
		return command.ErrInvalid("invalid input: " + err.Error())
	}
	return nil
}

func checkScreen(s string) error {
	if !screenRe.MatchString(s) {
		return command.ErrInvalid("screen is 1 to 64 characters from a-z, 0-9, _ . : -")
	}
	return nil
}

func get(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		Screen string `json:"screen"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	uid, err := owner(c)
	if err != nil {
		return nil, err
	}
	if err := checkScreen(in.Screen); err != nil {
		return nil, err
	}
	var cfg json.RawMessage
	var at time.Time
	err = c.Tx.QueryRow(c.Context, `select config, updated_at from core.ui_configs where workspace_id = $1::uuid and user_id = $2::uuid and screen = $3`,
		c.WorkspaceID(), uid, in.Screen).Scan(&cfg, &at)
	if err == pgx.ErrNoRows {
		return map[string]any{"config": nil, "updated_at": nil}, nil
	}
	if err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"config": cfg, "updated_at": at}, nil
}

func set(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		Screen string          `json:"screen"`
		Config json.RawMessage `json:"config"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	uid, err := owner(c)
	if err != nil {
		return nil, err
	}
	if err := checkScreen(in.Screen); err != nil {
		return nil, err
	}
	cfg, err := checkConfig(in.Config)
	if err != nil {
		return nil, err
	}
	if err := command.CheckRateWith("ui_config.set:"+c.WorkspaceID()+":"+uid, setBurst, setPerSec); err != nil {
		return nil, err
	}
	// The count and the insert must not interleave with another set by the same person: one lock per person and workspace.
	if _, err := c.Tx.Exec(c.Context, `select pg_advisory_xact_lock(hashtextextended('ui_config:' || $1 || ':' || $2, 0))`, c.WorkspaceID(), uid); err != nil {
		return nil, command.DB(err)
	}
	var at time.Time
	err = c.Tx.QueryRow(c.Context, `insert into core.ui_configs (workspace_id, user_id, screen, config)
		select $1::uuid, $2::uuid, $3, $4::jsonb
		where exists (select 1 from core.ui_configs where workspace_id = $1::uuid and user_id = $2::uuid and screen = $3)
		   or (select count(*) from core.ui_configs where workspace_id = $1::uuid and user_id = $2::uuid) < $5
		on conflict (workspace_id, user_id, screen) do update set config = excluded.config, updated_at = now()
		returning updated_at`, c.WorkspaceID(), uid, in.Screen, cfg, MaxScreens).Scan(&at)
	if err == pgx.ErrNoRows {
		return nil, command.ErrInvalid(quotaReason)
	}
	if err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{"updated_at": at}, nil
}

func reset(c *command.Ctx, input json.RawMessage) (any, error) {
	var in struct {
		Screen string `json:"screen"`
	}
	if err := decode(input, &in); err != nil {
		return nil, err
	}
	uid, err := owner(c)
	if err != nil {
		return nil, err
	}
	if err := checkScreen(in.Screen); err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, `delete from core.ui_configs where workspace_id = $1::uuid and user_id = $2::uuid and screen = $3`,
		c.WorkspaceID(), uid, in.Screen); err != nil {
		return nil, command.DB(err)
	}
	return map[string]any{}, nil
}

// checkConfig returns the config re-serialized from its parsed form (sorted keys, duplicates collapsed), which is what is
// stored and size-checked. It must be an object with an integer v >= 1 inside the depth, string and array limits.
func checkConfig(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return nil, command.ErrInvalid("config is required")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, command.ErrInvalid("config is not valid JSON")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, command.ErrInvalid("config is not valid JSON")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, command.ErrInvalid("config must be an object")
	}
	ver, ok := obj["v"].(json.Number)
	if n, err := ver.Int64(); !ok || err != nil || n < 1 {
		return nil, command.ErrInvalid("config.v must be an integer of 1 or more")
	}
	if err := walk(v, 1); err != nil {
		return nil, err
	}
	out, err := command.Marshal(v)
	if err != nil {
		return nil, command.ErrInvalid("config is not valid JSON")
	}
	if len(out) > MaxConfig {
		return nil, command.ErrInvalid(fmt.Sprintf("config is larger than %d KB", MaxConfig>>10))
	}
	return out, nil
}

func checkStr(s string) error {
	if utf8.RuneCountInString(s) > MaxString || strings.ContainsRune(s, 0) {
		return command.ErrInvalid(fmt.Sprintf("config text is at most %d characters", MaxString))
	}
	return nil
}

func walk(v any, depth int) error {
	deep := func() error {
		if depth > MaxDepth {
			return command.ErrInvalid(fmt.Sprintf("config is nested more than %d levels", MaxDepth))
		}
		return nil
	}
	switch x := v.(type) {
	case string:
		return checkStr(x)
	case []any:
		if err := deep(); err != nil {
			return err
		}
		if len(x) > MaxArray {
			return command.ErrInvalid(fmt.Sprintf("config lists hold at most %d items", MaxArray))
		}
		for _, e := range x {
			if err := walk(e, depth+1); err != nil {
				return err
			}
		}
	case map[string]any:
		if err := deep(); err != nil {
			return err
		}
		for k, e := range x {
			if err := checkStr(k); err != nil {
				return err
			}
			if err := walk(e, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
