package crm

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/cnote/api/internal/command"
)

// Lifecycle is Go-only (no Rust twin): contact.lifecycle composes the existing list commands through c.Call, so
// every section obeys exactly the same gate and space/note access as calling that list directly. A section the
// caller may not read comes back null instead of failing the whole read.
func lifecycle(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &in) != nil || in.ID == "" {
		return nil, command.ErrInvalid("id is required")
	}
	if _, err := c.Call("contact.get", map[string]any{"id": in.ID}); err != nil {
		return nil, err
	}
	// section runs one list command; forbidden means "not yours to see" (nil), anything else is a real error.
	section := func(name string, extra map[string]any) ([]map[string]any, error) {
		extra["contact_id"], extra["limit"] = in.ID, 200
		out, err := c.Call(name, extra)
		var ce *command.Error
		if errors.As(err, &ce) && ce.Code == command.Forbidden {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		var rows []map[string]any
		return rows, json.Unmarshal(out, &rows)
	}
	deals, err := section("deal.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	quotes, err := section("quote.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	invoices, err := section("invoice.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	activities, err := section("activity.list", map[string]any{}) // the contact's own and its deals' calls, visits, messages
	if err != nil {
		return nil, err
	}
	followUps, err := section("followup.list", map[string]any{}) // open deals with their next step and date
	if err != nil {
		return nil, err
	}
	for _, rows := range [][]map[string]any{deals, quotes} {
		if err := addCreated(c, rows); err != nil {
			return nil, err
		}
	}
	res := map[string]any{"deals": deals, "activities": activities, "follow_ups": followUps, "quotes": quotes, "invoices": invoices, "projects": nil, "payments": nil, "outstanding_minor": nil}

	// Projects: only items created from this contact's visible quotes (items carry no contact of their own).
	if quotes != nil {
		projects := []map[string]any{}
		for _, q := range quotes {
			id, _ := q["converted_item_id"].(string)
			if id == "" {
				continue
			}
			out, err := c.Call("item.get", map[string]any{"id": id})
			if err != nil {
				continue // item hidden or deleted: the caller does not see it
			}
			var it map[string]any
			if json.Unmarshal(out, &it) != nil {
				continue
			}
			p := map[string]any{"quote_id": q["id"]}
			for _, k := range []string{"id", "space_id", "key", "title", "status", "status_category", "created_at"} {
				p[k] = it[k]
			}
			projects = append(projects, p)
		}
		res["projects"] = projects
	}

	// Payments and the balance come only from invoices the caller can already list.
	if invoices != nil {
		ids, outstanding := []string{}, int64(0)
		number := map[string]any{}
		for _, i := range invoices {
			id, _ := i["id"].(string)
			ids, number[id] = append(ids, id), i["number"]
			if i["kind"] == "invoice" && i["status"] != "draft" && i["status"] != "cancelled" {
				outstanding += num(i["total_minor"]) - num(i["paid_minor"]) - num(i["credited_minor"]) - num(i["written_off_minor"])
			}
		}
		rows, err := c.Tx.Query(c.Context, `select id::text, invoice_id::text, amount_minor, method, reference, received_at
			from billing.payments where invoice_id = any($1::text[]::uuid[]) order by received_at desc`, ids)
		if err != nil {
			return nil, command.DB(err)
		}
		defer rows.Close()
		pays := []map[string]any{}
		for rows.Next() {
			var id, inv, method string
			var amt int64
			var ref *string
			var at time.Time
			if err := rows.Scan(&id, &inv, &amt, &method, &ref, &at); err != nil {
				return nil, command.DB(err)
			}
			pays = append(pays, map[string]any{"id": id, "invoice_id": inv, "invoice_number": number[inv],
				"amount_minor": amt, "method": method, "reference": ref, "received_at": command.Ts(at)})
		}
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
		res["payments"], res["outstanding_minor"] = pays, outstanding
	}
	return res, nil
}

func num(v any) int64 { f, _ := v.(float64); return int64(f) }

// addCreated stamps each already-authorized row with its note's creation time (deal and quote rows carry none).
func addCreated(c *command.Ctx, rows []map[string]any) error {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		id, _ := r["id"].(string)
		ids = append(ids, id)
	}
	rs, err := c.Tx.Query(c.Context, `select id::text, created_at from core.notes where id = any($1::text[]::uuid[])`, ids)
	if err != nil {
		return command.DB(err)
	}
	defer rs.Close()
	at := map[string]command.Ts{}
	for rs.Next() {
		var id string
		var t time.Time
		if err := rs.Scan(&id, &t); err != nil {
			return command.DB(err)
		}
		at[id] = command.Ts(t)
	}
	for _, r := range rows {
		r["created_at"] = at[r["id"].(string)]
	}
	return command.DB(rs.Err())
}
