package timelog

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

var sacRe = regexp.MustCompile(`^[0-9]{4,8}$`)

// Per-space billing profile: core.spaces.config.billing = {contact_id, hourly_rate_minor, hsn_sac, gst_rate_bp}.
type profile struct {
	ContactID string `json:"contact_id"`
	Rate      int64  `json:"hourly_rate_minor"`
	HSNSAC    string `json:"hsn_sac"`
	GSTRateBP int    `json:"gst_rate_bp"`
}

func loadProfile(c *command.Ctx, space string) (profile, error) {
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, "select coalesce(config->'billing','{}') from core.spaces where id = $1::uuid", space).Scan(&raw); err != nil {
		return profile{}, command.DB(err)
	}
	p := profile{GSTRateBP: 1800}
	_ = json.Unmarshal(raw, &p)
	return p, nil
}

func billingGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err := need("space_id", v.SpaceID); err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, v.SpaceID, false); err != nil {
		return nil, err
	}
	return loadProfile(c, v.SpaceID)
}

func billingSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err := need("space_id", v.SpaceID); err != nil {
		return nil, err
	}
	if err := access.AssertSpaceAccess(c, v.SpaceID, true); err != nil {
		return nil, err
	}
	patch := map[string]any{}
	if v.ContactID != nil {
		var ok bool
		if err := c.Tx.QueryRow(c.Context, "select exists(select 1 from crm.contacts where note_id = $1::uuid)", *v.ContactID).Scan(&ok); err != nil {
			return nil, command.DB(err)
		}
		if !ok {
			return nil, command.ErrNotFound("contact")
		}
		patch["contact_id"] = *v.ContactID
	}
	if v.HourlyRate != nil {
		if *v.HourlyRate < 0 {
			return nil, command.ErrInvalid("hourly_rate_minor cannot be negative")
		}
		patch["hourly_rate_minor"] = *v.HourlyRate
	}
	if v.HSNSAC != nil {
		if !sacRe.MatchString(*v.HSNSAC) {
			return nil, command.ErrInvalid("hsn_sac must be 4 to 8 digits")
		}
		patch["hsn_sac"] = *v.HSNSAC
	}
	if v.GSTRateBP != nil {
		if *v.GSTRateBP < 0 || *v.GSTRateBP > 2800 {
			return nil, command.ErrInvalid("gst_rate_bp must be between 0 and 2800")
		}
		patch["gst_rate_bp"] = *v.GSTRateBP
	}
	b, _ := json.Marshal(patch)
	if _, err := c.Tx.Exec(c.Context,
		"update core.spaces set config = jsonb_set(config, '{billing}', coalesce(config->'billing','{}'::jsonb) || $2::jsonb) where id = $1::uuid", v.SpaceID, string(b)); err != nil {
		return nil, command.DB(err)
	}
	fields := make([]string, 0, len(patch))
	for k := range patch {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	if _, err := c.Emit("project.billing_set", nil, map[string]any{"space_id": v.SpaceID, "fields": fields}); err != nil {
		return nil, err
	}
	return loadProfile(c, v.SpaceID)
}

// draft raises a draft invoice through invoice.create_draft and returns the full invoice and its id.
func draft(c *command.Ctx, space, contact string, date, place *string, lines []map[string]any) (json.RawMessage, string, error) {
	body := map[string]any{"space_id": space, "contact_id": contact, "lines": lines}
	if date != nil {
		body["invoice_date"] = *date
	}
	if place != nil {
		body["place_of_supply"] = *place
	}
	out, err := c.Call("invoice.create_draft", body)
	if err != nil {
		return nil, "", err
	}
	var inv struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(out, &inv) != nil || inv.ID == "" {
		return nil, "", command.NewError(command.Internal, "invoice.create_draft returned no id")
	}
	return out, inv.ID, nil
}

func hours(min int) string { return fmt.Sprintf("%d h %02d min", min/60, min%60) }

// invoice bills the unbilled billable time of a space up to a date. The entries are locked and marked in the same
// transaction as the draft, so a second call (or a concurrent one) finds nothing left to bill.
func invoice(c *command.Ctx, raw json.RawMessage) (any, error) {
	v, err := decode(raw)
	if err != nil {
		return nil, err
	}
	if err := need("space_id", v.SpaceID); err != nil {
		return nil, err
	}
	if v.ToDate == nil {
		return nil, command.ErrInvalid("to_date is required")
	}
	group := "none"
	if v.GroupBy != nil {
		group = *v.GroupBy
	}
	if group != "none" && group != "item" && group != "person" {
		return nil, command.ErrInvalid("group_by must be item, person or none")
	}
	if err := access.AssertSpaceAccess(c, v.SpaceID, true); err != nil {
		return nil, err
	}
	p, err := loadProfile(c, v.SpaceID)
	if err != nil {
		return nil, err
	}
	contact := p.ContactID
	if v.ContactID != nil {
		contact = *v.ContactID
	}
	if contact == "" || p.Rate <= 0 || p.HSNSAC == "" {
		return nil, command.ErrInvalid("set the customer, hourly rate and SAC for this project first (project.billing_set)")
	}
	rows, err := c.Tx.Query(c.Context,
		`select e.id::text, e.minutes, pi.key, n.title, u.id::text, u.name from projects.time_entries e
		 join core.notes n on n.id = e.item_id join projects.items pi on pi.note_id = e.item_id join core.users u on u.id = e.user_id
		 where n.space_id = $1::uuid and e.billable and e.invoice_id is null and e.worked_on <= $2::date
		 order by e.worked_on, e.created_at for update of e`, v.SpaceID, *v.ToDate)
	if err != nil {
		return nil, command.DB(err)
	}
	type bucket struct {
		label string
		min   int
	}
	var order []string
	buckets := map[string]*bucket{}
	var ids []string
	total := 0
	for rows.Next() {
		var id, key, title, uid, user string
		var min int
		if err := rows.Scan(&id, &min, &key, &title, &uid, &user); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		k, label := "all", "Professional services"
		switch group {
		case "item":
			k, label = key, key+" "+title
		case "person":
			k, label = uid, "Services by "+user
		}
		if buckets[k] == nil {
			buckets[k] = &bucket{label: label}
			order = append(order, k)
		}
		buckets[k].min += min
		ids, total = append(ids, id), total+min
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	if len(ids) == 0 {
		return nil, command.ErrConflict("there is no unbilled billable time up to that date")
	}
	lines := []map[string]any{}
	for _, k := range order {
		b := buckets[k]
		// shortcut: price = rate x minutes / 60 rounded to the paisa on one unit, not hours as quantity; upgrade when invoices must print hours as quantity.
		lines = append(lines, map[string]any{"description": fmt.Sprintf("%s: %s at %d.%02d per hour", b.label, hours(b.min), p.Rate/100, p.Rate%100),
			"hsn_sac": p.HSNSAC, "quantity": 1, "unit_price_minor": (p.Rate*int64(b.min) + 30) / 60, "gst_rate_bp": p.GSTRateBP})
	}
	out, invID, err := draft(c, v.SpaceID, contact, v.InvoiceDate, nil, lines)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "update projects.time_entries set invoice_id = $2::uuid where id = any($1::text[]::uuid[])", ids, invID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("time.invoiced", &invID, map[string]any{"space_id": v.SpaceID, "entries": len(ids), "minutes": total}); err != nil {
		return nil, err
	}
	return map[string]any{"invoice": json.RawMessage(out), "entries_billed": len(ids), "minutes": total}, nil
}
