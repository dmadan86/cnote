package projects

// Status from evidence (ADR-014), port of evidence.rs: signals from outside the app attach to items and fire the
// transitions whose `evidence` guard matches.

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// errMessage is CmdError.message for any error a handler can get back.
func errMessage(err error) string {
	var ce *command.Error
	if errors.As(command.DB(err), &ce) {
		return ce.Message
	}
	return err.Error()
}

func evidenceRecord(c *command.Ctx, input json.RawMessage) (any, error) {
	o, err := parseObj(input)
	if err != nil {
		return nil, err
	}
	source, err := o.reqStr("source")
	if err != nil {
		return nil, err
	}
	kindIn, err := o.reqStr("kind")
	if err != nil {
		return nil, err
	}
	keys, ok := o.strs("item_keys", true)
	if !ok {
		return nil, command.ErrFallback
	}
	url, err := o.optStr("url")
	if err != nil {
		return nil, err
	}

	kind := strings.TrimSpace(kindIn)
	valid := kind != ""
	for _, ch := range kind {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_') {
			valid = false
		}
	}
	if !valid {
		return nil, command.ErrInvalid("kind must be lowercase snake_case, e.g. pr_merged")
	}
	if strings.TrimSpace(source) == "" || len(keys) > 50 {
		return nil, command.ErrInvalid("source is required and at most 50 item keys are allowed")
	}
	recorded := 0
	transitions, skipped, unknown := []any{}, []any{}, []string{}
	for _, key := range keys {
		k := key
		item, err := LoadItem(c, ItemRef{Key: &k}, true)
		if err != nil {
			var ce *command.Error
			if errors.As(err, &ce) && (ce.Code == command.NotFound || ce.Code == command.Forbidden) {
				unknown = append(unknown, key)
				continue
			}
			return nil, err
		}
		if err := access.AssertSpaceAccess(c, item.SpaceID, true); err != nil {
			return nil, err
		}
		if _, err := c.Tx.Exec(c.Context,
			"insert into projects.evidence (workspace_id, note_id, source, kind, url) values ($1::uuid,$2::uuid,$3,$4,$5)",
			c.WorkspaceID(), item.ID, strings.TrimSpace(source), kind, url); err != nil {
			return nil, command.DB(err)
		}
		recorded++
		if _, err := c.Emit("evidence.recorded", &item.ID, map[string]any{"key": item.Key, "kind": kind, "source": source, "url": url}); err != nil {
			return nil, err
		}
		wf, err := SpaceWF(c, item.SpaceID)
		if err != nil {
			return nil, err
		}
		def, err := DefinitionFor(c, item.SpaceID, wf, item.WorkflowVersion)
		if err != nil {
			return nil, err
		}
		var why *string
		for _, t := range def.EvidenceDriven(item.Status, kind) {
			cur, err := LoadItem(c, idRef(item.ID), true)
			if err != nil {
				return nil, err
			}
			after, err := TransitionItem(c, cur, t.Name)
			if err != nil {
				m := errMessage(err)
				why = &m
				continue
			}
			transitions = append(transitions, map[string]any{"key": item.Key, "from": item.Status, "to": after.Status, "transition": t.Name})
			why = nil
			break
		}
		if why != nil {
			skipped = append(skipped, map[string]any{"key": item.Key, "reason": *why})
		}
	}
	return map[string]any{"recorded": recorded, "transitions": transitions, "skipped": skipped, "unknown": unknown}, nil
}
