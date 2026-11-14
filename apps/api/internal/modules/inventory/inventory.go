// Package inventory (WP13): items, locations and an append-only stock ledger for a trading SMB. Go-only.
//
// Stock on hand is always the sum of inventory.movements, never a stored number. Purchase bills post receipts and
// invoices post issues inside the same transaction (PostLines); cancelling either appends exact reversals (Reverse).
// Valuation is weighted average cost, the only method: each movement stores its signed value at cost, and an issue is
// valued at the average cost of the item (all locations) at that moment. The general ledger is not touched.
package inventory

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

func Commands() []command.Command {
	mi, rd := command.GroupManageInvoices, command.GroupRead
	mk := func(name string, g command.Group, min command.Role, h command.Handler) command.Command {
		c := command.NewCommand(name, g, h)
		c.MinRole = min
		return c
	}
	adjust := mk("stock.adjust", mi, command.Admin, adjust)
	adjust.Confirm = true // writing stock off or on changes the books: a machine caller needs a person to confirm
	return []command.Command{
		mk("stock_item.create", mi, command.Member, itemCreate),
		mk("stock_item.update", mi, command.Member, itemUpdate),
		mk("stock_item.list", rd, command.Member, itemList),
		mk("stock_item.get", rd, command.Member, itemGet),
		mk("stock_location.list", rd, command.Member, locationList),
		mk("stock_location.create", mi, command.Admin, locationCreate),
		mk("stock_location.update", mi, command.Admin, locationUpdate),
		adjust,
		mk("stock.transfer", mi, command.Member, transfer),
		mk("stock.movement_list", rd, command.Member, movementList),
		mk("report.stock_valuation", rd, command.Member, valuation),
	}
}

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	qtyRe  = regexp.MustCompile(`^\d{1,12}(\.\d{1,3})?$`)
)

type obj map[string]json.RawMessage

func decode(raw json.RawMessage) (obj, error) {
	var m obj
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return nil, command.ErrInvalid("input must be a JSON object")
	}
	return m, nil
}

func (o obj) has(k string) bool { b, ok := o[k]; return ok && string(b) != "null" }

func (o obj) str(k string, max int) (string, error) {
	if !o.has(k) {
		return "", nil
	}
	var v string
	if json.Unmarshal(o[k], &v) != nil {
		return "", command.ErrInvalid(k + " must be a string")
	}
	if v = strings.TrimSpace(v); len([]rune(v)) > max {
		return "", command.ErrInvalid(k + " is too long")
	}
	return v, nil
}

func (o obj) id(k string, required bool) (string, error) {
	v, err := o.str(k, 36)
	if err != nil {
		return "", err
	}
	v = strings.ToLower(v)
	if v == "" && required {
		return "", command.ErrInvalid(k + " is required")
	}
	if v != "" && !uuidRe.MatchString(v) {
		return "", command.ErrInvalid(k + " is not a valid id")
	}
	return v, nil
}

func (o obj) int(k string, min, max int64) (int64, bool, error) {
	if !o.has(k) {
		return 0, false, nil
	}
	var v int64
	if json.Unmarshal(o[k], &v) != nil || v < min || v > max {
		return 0, true, command.ErrInvalid(fmt.Sprintf("%s must be a whole number from %d to %d", k, min, max))
	}
	return v, true, nil
}

func (o obj) boolean(k string) (bool, bool, error) {
	if !o.has(k) {
		return false, false, nil
	}
	var v bool
	if json.Unmarshal(o[k], &v) != nil {
		return false, true, command.ErrInvalid(k + " must be true or false")
	}
	return v, true, nil
}

// qty reads a decimal quantity (number or string) with at most 3 decimals. signed allows a negative value.
func (o obj) qty(k string, signed, zeroOK bool) (string, bool, error) {
	if !o.has(k) {
		return "", false, nil
	}
	t := strings.TrimSpace(string(o[k]))
	t = strings.Trim(t, `"`)
	neg := strings.HasPrefix(t, "-")
	if neg {
		if !signed {
			return "", true, command.ErrInvalid(k + " must be positive")
		}
		t = t[1:]
	}
	if !qtyRe.MatchString(t) || (!zeroOK && strings.Trim(t, "0.") == "") {
		return "", true, command.ErrInvalid(k + " must be a non-zero number with at most 3 decimals")
	}
	if neg {
		t = "-" + t
	}
	return t, true, nil
}

func validHSN(h string) bool {
	if n := len(h); n != 4 && n != 6 && n != 8 {
		return false
	}
	for _, r := range h {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ---------- items ----------

type itemOut struct {
	ID           string     `json:"id"`
	SKU          string     `json:"sku"`
	Name         string     `json:"name"`
	HSNSAC       *string    `json:"hsn_sac"`
	Unit         string     `json:"unit"`
	GSTRateBP    int32      `json:"gst_rate_bp"`
	PriceMinor   int64      `json:"price_minor"`
	ReorderLevel string     `json:"reorder_level"`
	Active       bool       `json:"active"`
	OnHand       string     `json:"on_hand"`
	Low          bool       `json:"low"`
	CreatedAt    command.Ts `json:"created_at"`
}

// itemSelect: on hand is derived from the ledger (RLS scopes the movements to the workspace).
const itemSelect = `select i.id::text, i.sku, i.name, i.hsn_sac, i.unit, i.gst_rate_bp, i.price_minor, i.reorder_level::text, i.active,
	coalesce(s.q, 0)::text, i.active and i.reorder_level > 0 and coalesce(s.q, 0) <= i.reorder_level, i.created_at
	from inventory.items i left join (select item_id, sum(qty) q from inventory.movements group by item_id) s on s.item_id = i.id `

func scanItem(r pgx.Row) (itemOut, error) {
	var o itemOut
	var at time.Time
	err := r.Scan(&o.ID, &o.SKU, &o.Name, &o.HSNSAC, &o.Unit, &o.GSTRateBP, &o.PriceMinor, &o.ReorderLevel, &o.Active, &o.OnHand, &o.Low, &at)
	o.CreatedAt = command.Ts(at)
	return o, err
}

func loadItem(c *command.Ctx, id string) (itemOut, error) {
	o, err := scanItem(c.Tx.QueryRow(c.Context, itemSelect+"where i.id = $1::uuid", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return o, command.ErrNotFound("item")
	}
	return o, command.DB(err)
}

// itemFields validates the editable columns; only the keys present are returned.
func itemFields(o obj, create bool) (map[string]any, error) {
	f := map[string]any{}
	for _, k := range []struct {
		key  string
		max  int
		need bool
	}{{"sku", 40, true}, {"name", 120, true}, {"unit", 12, false}} {
		if o.has(k.key) || (create && k.need) {
			v, err := o.str(k.key, k.max)
			if err != nil {
				return nil, err
			}
			if v == "" && (k.need || o.has(k.key)) {
				return nil, command.ErrInvalid(k.key + " is required")
			}
			f[k.key] = v
		}
	}
	if o.has("hsn_sac") {
		h, err := o.str("hsn_sac", 8)
		if err != nil {
			return nil, err
		}
		if h != "" && !validHSN(h) {
			return nil, command.ErrInvalid("hsn_sac must be 4, 6 or 8 digits")
		}
		if h == "" {
			f["hsn_sac"] = nil
		} else {
			f["hsn_sac"] = h
		}
	}
	if v, ok, err := o.int("gst_rate_bp", 0, 2800); err != nil {
		return nil, err
	} else if ok {
		f["gst_rate_bp"] = v
	}
	if v, ok, err := o.int("price_minor", 0, 1_000_000_000_000); err != nil {
		return nil, err
	} else if ok {
		f["price_minor"] = v
	}
	if v, ok, err := o.qty("reorder_level", false, true); err != nil {
		return nil, err
	} else if ok {
		f["reorder_level"] = v
	}
	if v, ok, err := o.boolean("active"); err != nil {
		return nil, err
	} else if ok && !create {
		f["active"] = v
	}
	return f, nil
}

func itemCreate(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	f, err := itemFields(o, true)
	if err != nil {
		return nil, err
	}
	var id string
	err = c.Tx.QueryRow(c.Context,
		`insert into inventory.items (workspace_id, sku, name, hsn_sac, unit, gst_rate_bp, price_minor, reorder_level)
		 values ($1::uuid, $2, $3, $4, coalesce(nullif($5, ''), 'pcs'), coalesce($6::int, 1800), coalesce($7::bigint, 0), coalesce($8::numeric, 0))
		 on conflict do nothing returning id::text`,
		c.WorkspaceID(), f["sku"], f["name"], f["hsn_sac"], f["unit"], f["gst_rate_bp"], f["price_minor"], f["reorder_level"]).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("an item with this SKU already exists")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("stock_item.created", nil, map[string]any{"item_id": id, "sku": f["sku"]}); err != nil {
		return nil, err
	}
	return loadItem(c, id)
}

func itemUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, err := o.id("id", true)
	if err != nil {
		return nil, err
	}
	f, err := itemFields(o, false)
	if err != nil {
		return nil, err
	}
	if len(f) == 0 {
		return loadItem(c, id)
	}
	var found string
	if err := c.Tx.QueryRow(c.Context, "select id::text from inventory.items where id = $1::uuid for no key update", id).Scan(&found); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("item")
	} else if err != nil {
		return nil, command.DB(err)
	}
	sets, args := []string{}, []any{id}
	cast := map[string]string{"gst_rate_bp": "::int", "price_minor": "::bigint", "reorder_level": "::numeric", "active": "::boolean"}
	for _, k := range []string{"sku", "name", "hsn_sac", "unit", "gst_rate_bp", "price_minor", "reorder_level", "active"} {
		if v, ok := f[k]; ok {
			args = append(args, v)
			sets = append(sets, fmt.Sprintf("%s = $%d%s", k, len(args), cast[k]))
		}
	}
	_, err = c.Tx.Exec(c.Context, "update inventory.items set "+strings.Join(sets, ", ")+" where id = $1::uuid", args...)
	if err != nil {
		if strings.Contains(err.Error(), "items_sku") {
			return nil, command.ErrConflict("an item with this SKU already exists")
		}
		return nil, command.DB(err)
	}
	if _, err := c.Emit("stock_item.updated", nil, map[string]any{"item_id": id}); err != nil {
		return nil, err
	}
	return loadItem(c, id)
}

func itemList(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	low, _, err := o.boolean("low")
	if err != nil {
		return nil, err
	}
	archived, _, err := o.boolean("include_archived")
	if err != nil {
		return nil, err
	}
	q, err := o.str("q", 80)
	if err != nil {
		return nil, err
	}
	limit, ok, err := o.int("limit", 1, 500)
	if err != nil {
		return nil, err
	}
	if !ok {
		limit = 200
	}
	rows, err := c.Tx.Query(c.Context, itemSelect+`where ($1::bool or i.active) and ($2 = '' or i.sku ilike '%'||$2||'%' or i.name ilike '%'||$2||'%')
		and (not $3::bool or (i.active and i.reorder_level > 0 and coalesce(s.q, 0) <= i.reorder_level))
		order by (not i.active), lower(i.name), i.id limit $4`, archived, q, low, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []itemOut{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, command.DB(err)
		}
		out = append(out, it)
	}
	return out, command.DB(rows.Err())
}

type locQty struct {
	LocationID string `json:"location_id"`
	Name       string `json:"name"`
	OnHand     string `json:"on_hand"`
}

func itemGet(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, err := o.id("id", true)
	if err != nil {
		return nil, err
	}
	it, err := loadItem(c, id)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select l.id::text, l.name, coalesce(sum(m.qty), 0)::text
		from inventory.locations l left join inventory.movements m on m.location_id = l.id and m.item_id = $1::uuid
		where l.active or exists (select 1 from inventory.movements x where x.location_id = l.id and x.item_id = $1::uuid)
		group by l.id, l.name order by l.is_default desc, l.name`, id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	locs := []locQty{}
	for rows.Next() {
		var l locQty
		if err := rows.Scan(&l.LocationID, &l.Name, &l.OnHand); err != nil {
			return nil, command.DB(err)
		}
		locs = append(locs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, command.DB(err)
	}
	rows.Close()
	mv, err := movements(c, id, "", 50)
	if err != nil {
		return nil, err
	}
	return map[string]any{"item": it, "locations": locs, "movements": mv}, nil
}

// ---------- locations ----------

type locOut struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	Active    bool   `json:"active"`
}

func locationRows(c *command.Ctx, only string) ([]locOut, error) {
	rows, err := c.Tx.Query(c.Context, `select id::text, name, is_default, active from inventory.locations
		where ($1 = '' or id = $1::uuid) order by (not active), is_default desc, name`, only)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []locOut{}
	for rows.Next() {
		var l locOut
		if err := rows.Scan(&l.ID, &l.Name, &l.IsDefault, &l.Active); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, l)
	}
	return out, command.DB(rows.Err())
}

func locationList(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	if _, err := defaultLocation(c); err != nil {
		return nil, err
	}
	return locationRows(c, "")
}

func locationCreate(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	name, err := o.str("name", 80)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, command.ErrInvalid("name is required")
	}
	if _, err := defaultLocation(c); err != nil { // the first location of a workspace is the default one
		return nil, err
	}
	var id string
	err = c.Tx.QueryRow(c.Context, "insert into inventory.locations (workspace_id, name) values ($1::uuid, $2) on conflict do nothing returning id::text", c.WorkspaceID(), name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrConflict("a location with this name already exists")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("stock_location.created", nil, map[string]any{"location_id": id, "name": name}); err != nil {
		return nil, err
	}
	r, err := locationRows(c, id)
	if err != nil {
		return nil, err
	}
	return r[0], nil
}

func locationUpdate(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	id, err := o.id("id", true)
	if err != nil {
		return nil, err
	}
	name, err := o.str("name", 80)
	if err != nil {
		return nil, err
	}
	def, _, err := o.boolean("is_default")
	if err != nil {
		return nil, err
	}
	active, haveActive, err := o.boolean("active")
	if err != nil {
		return nil, err
	}
	var cur locOut
	if err := c.Tx.QueryRow(c.Context, "select id::text, name, is_default, active from inventory.locations where id = $1::uuid for update", id).
		Scan(&cur.ID, &cur.Name, &cur.IsDefault, &cur.Active); errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("location")
	} else if err != nil {
		return nil, command.DB(err)
	}
	if haveActive && !active {
		if cur.IsDefault {
			return nil, command.ErrConflict("make another location the default before archiving this one")
		}
		var holds bool
		if err := c.Tx.QueryRow(c.Context, "select exists (select 1 from inventory.movements where location_id = $1::uuid group by item_id having sum(qty) <> 0)", id).Scan(&holds); err != nil {
			return nil, command.DB(err)
		}
		if holds {
			return nil, command.ErrConflict("move the stock out of this location before archiving it")
		}
	}
	if def {
		if haveActive && !active {
			return nil, command.ErrInvalid("the default location cannot be archived")
		}
		if _, err := c.Tx.Exec(c.Context, "update inventory.locations set is_default = false where is_default and id <> $1::uuid", id); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Tx.Exec(c.Context, `update inventory.locations set name = coalesce(nullif($2, ''), name), active = coalesce($3::boolean, active),
		is_default = is_default or $4::boolean where id = $1::uuid`, id, name, nilBool(haveActive, active), def); err != nil {
		if strings.Contains(err.Error(), "locations_workspace_id_name_key") {
			return nil, command.ErrConflict("a location with this name already exists")
		}
		return nil, command.DB(err)
	}
	if _, err := c.Emit("stock_location.updated", nil, map[string]any{"location_id": id}); err != nil {
		return nil, err
	}
	r, err := locationRows(c, id)
	if err != nil {
		return nil, err
	}
	return r[0], nil
}

func nilBool(have, v bool) any {
	if !have {
		return nil
	}
	return v
}

// defaultLocation is where documents post. A workspace gets "Main" on first use; the advisory lock makes two first
// uses in parallel agree on one row.
func defaultLocation(c *command.Ctx) (string, error) {
	const q = "select id::text from inventory.locations where is_default"
	var id string
	err := c.Tx.QueryRow(c.Context, q).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "select pg_advisory_xact_lock(hashtext('inventory-default:' || $1))", c.WorkspaceID()); err != nil {
		return "", command.DB(err)
	}
	if err := c.Tx.QueryRow(c.Context, q).Scan(&id); err == nil {
		return id, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", command.DB(err)
	}
	err = c.Tx.QueryRow(c.Context, `update inventory.locations set is_default = true where id = (select id from inventory.locations where active order by created_at, id limit 1) returning id::text`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = c.Tx.QueryRow(c.Context, "insert into inventory.locations (workspace_id, name, is_default) values ($1::uuid, 'Main', true) returning id::text", c.WorkspaceID()).Scan(&id)
	}
	return id, command.DB(err)
}

// ---------- the ledger ----------

type posting struct {
	itemID, locID, kind string
	qty, value          string // signed qty text; value in paise as text for the numeric cast
	reason              string
	srcType, srcID      string
	transferID          string
	reverses            string
}

func insertMovement(c *command.Ctx, p posting) (string, error) {
	var id string
	err := c.Tx.QueryRow(c.Context,
		`insert into inventory.movements (workspace_id, item_id, location_id, kind, qty, value_minor, reason, source_type, source_id, transfer_id, reverses, created_by)
		 values ($1::uuid, $2::uuid, $3::uuid, $4, $5::numeric, $6::bigint, nullif($7, ''), nullif($8, ''), nullif($9, '')::uuid, nullif($10, '')::uuid, nullif($11, '')::uuid, $12::uuid)
		 returning id::text`,
		c.WorkspaceID(), p.itemID, p.locID, p.kind, p.qty, p.value, p.reason, p.srcType, p.srcID, p.transferID, p.reverses, c.Principal.UserID).Scan(&id)
	return id, command.DB(err)
}

// lockItem serialises everything that moves one item: two issues racing for the last unit queue here, so the second
// sees the first's movement. NO KEY UPDATE, not UPDATE: the invoice lines being written hold KEY SHARE on the item (foreign
// key), and FOR UPDATE would deadlock two issues of the same item. Callers lock several items in id order to avoid deadlocks.
func lockItem(c *command.Ctx, id string) (sku string, err error) {
	err = c.Tx.QueryRow(c.Context, "select sku from inventory.items where id = $1::uuid for no key update", id).Scan(&sku)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", command.ErrNotFound("item")
	}
	return sku, command.DB(err)
}

func allowNegative(c *command.Ctx) (bool, error) {
	var ok bool
	err := c.Tx.QueryRow(c.Context, "select coalesce(settings->>'allow_negative_stock', '') = 'true' from core.workspaces where id = $1::uuid", c.WorkspaceID()).Scan(&ok)
	return ok, command.DB(err)
}

// checkStock refuses to take qty (positive text) out of a location that holds less, unless the workspace allows
// negative stock (settings.allow_negative_stock).
func checkStock(c *command.Ctx, itemID, sku, locID, qty string) error {
	var have string
	var enough bool
	if err := c.Tx.QueryRow(c.Context, `select coalesce(sum(qty), 0)::text, coalesce(sum(qty), 0) >= $3::numeric
		from inventory.movements where item_id = $1::uuid and location_id = $2::uuid`, itemID, locID, qty).Scan(&have, &enough); err != nil {
		return command.DB(err)
	}
	if enough {
		return nil
	}
	if ok, err := allowNegative(c); err != nil || ok {
		return err
	}
	return command.ErrConflict(fmt.Sprintf("not enough stock of %s: %s on hand, %s needed", sku, have, qty))
}

// avgValue is the cost (paise) of qty units at the item's weighted average: all of the value when qty covers the
// stock, nothing when there is no stock (negative stock carries no cost until stock arrives), else the pro rata share.
func avgValue(c *command.Ctx, itemID, qty string) (string, error) {
	var v string
	err := c.Tx.QueryRow(c.Context, `select case when q <= 0 then 0 when $2::numeric >= q then v else round($2::numeric * v / q) end::bigint::text
		from (select coalesce(sum(qty), 0) q, coalesce(sum(value_minor), 0) v from inventory.movements where item_id = $1::uuid) s`, itemID, qty).Scan(&v)
	return v, command.DB(err)
}

func neg(s string) string {
	if strings.HasPrefix(s, "-") {
		return s[1:]
	}
	if s == "0" {
		return s
	}
	return "-" + s
}

// outflow takes qty (positive text) out of a location at weighted average cost.
func outflow(c *command.Ctx, p posting, sku, qty string) (string, error) {
	if err := checkStock(c, p.itemID, sku, p.locID, qty); err != nil {
		return "", err
	}
	v, err := avgValue(c, p.itemID, qty)
	if err != nil {
		return "", err
	}
	p.qty, p.value = neg(qty), neg(v)
	_, err = insertMovement(c, p)
	return v, err
}

// PostLines posts the stock of a document's lines at the default location: kind "receipt" (a purchase bill, valued at
// the line taxable amounts, input GST excluded) or "issue" (an invoice, valued at average cost). table and col name
// the line table and its document column (constants). Lines without an item are ignored. Safe to call once per
// document: movements_once rejects a second posting.
func PostLines(c *command.Ctx, kind, srcType, srcID, table, col string) error {
	rows, err := c.Tx.Query(c.Context, `select stock_item_id::text, sum(quantity)::text, sum(taxable_minor)::bigint
		from `+table+` where `+col+` = $1::uuid and stock_item_id is not null group by 1 order by 1`, srcID)
	if err != nil {
		return command.DB(err)
	}
	type line struct {
		item, qty string
		taxable   int64
	}
	var ls []line
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.item, &l.qty, &l.taxable); err != nil {
			rows.Close()
			return command.DB(err)
		}
		ls = append(ls, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return command.DB(err)
	}
	if len(ls) == 0 {
		return nil
	}
	loc, err := defaultLocation(c)
	if err != nil {
		return err
	}
	for _, l := range ls {
		sku, err := lockItem(c, l.item)
		if err != nil {
			return err
		}
		var positive bool
		if err := c.Tx.QueryRow(c.Context, "select $1::numeric > 0", l.qty).Scan(&positive); err != nil {
			return command.DB(err)
		}
		if !positive || l.taxable < 0 {
			return command.ErrInvalid("lines with a stock item need a positive quantity and amount (" + sku + ")")
		}
		p := posting{itemID: l.item, locID: loc, kind: kind, srcType: srcType, srcID: srcID}
		if kind == "receipt" {
			p.qty, p.value = l.qty, fmt.Sprint(l.taxable)
			_, err = insertMovement(c, p)
		} else {
			_, err = outflow(c, p, sku, l.qty)
		}
		if err != nil {
			return err
		}
	}
	_, err = c.Emit("stock."+kind+"_posted", &srcID, map[string]any{"source_type": srcType, "items": len(ls)})
	return err
}

// Reverse appends the exact opposite of every movement the document posted that is not reversed yet (same quantity
// and cost). Taking back a receipt can leave stock negative: refused unless the workspace allows it.
func Reverse(c *command.Ctx, srcType, srcID string) error {
	rows, err := c.Tx.Query(c.Context, `select m.id::text, m.item_id::text, m.location_id::text, m.qty::text, m.value_minor::text
		from inventory.movements m where m.source_type = $1 and m.source_id = $2::uuid and m.kind in ('receipt','issue')
		  and not exists (select 1 from inventory.movements r where r.reverses = m.id) order by m.item_id`, srcType, srcID)
	if err != nil {
		return command.DB(err)
	}
	type mv struct{ id, item, loc, qty, value string }
	var ms []mv
	for rows.Next() {
		var m mv
		if err := rows.Scan(&m.id, &m.item, &m.loc, &m.qty, &m.value); err != nil {
			rows.Close()
			return command.DB(err)
		}
		ms = append(ms, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return command.DB(err)
	}
	for _, m := range ms {
		sku, err := lockItem(c, m.item)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(m.qty, "-") { // undoing a receipt takes stock out
			if err := checkStock(c, m.item, sku, m.loc, m.qty); err != nil {
				return err
			}
		}
		if _, err := insertMovement(c, posting{itemID: m.item, locID: m.loc, kind: "reversal", qty: neg(m.qty), value: neg(m.value),
			srcType: srcType, srcID: srcID, reverses: m.id}); err != nil {
			return err
		}
	}
	if len(ms) > 0 {
		_, err = c.Emit("stock.reversed", &srcID, map[string]any{"source_type": srcType, "items": len(ms)})
	}
	return err
}

// ---------- manual movements ----------

func adjust(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	item, err := o.id("item_id", true)
	if err != nil {
		return nil, err
	}
	qty, ok, err := o.qty("qty", true, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, command.ErrInvalid("qty is required: positive adds stock, negative removes it")
	}
	reason, err := o.str("reason", 300)
	if err != nil {
		return nil, err
	}
	if reason == "" {
		return nil, command.ErrInvalid("reason is required for a stock adjustment")
	}
	loc, err := o.id("location_id", false)
	if err != nil {
		return nil, err
	}
	cost, haveCost, err := o.int("unit_cost_minor", 0, 1_000_000_000_000)
	if err != nil {
		return nil, err
	}
	sku, err := lockItem(c, item)
	if err != nil {
		return nil, err
	}
	if loc == "" {
		if loc, err = defaultLocation(c); err != nil {
			return nil, err
		}
	} else if err := locationOK(c, loc); err != nil {
		return nil, err
	}
	p := posting{itemID: item, locID: loc, kind: "adjustment", reason: reason}
	if strings.HasPrefix(qty, "-") {
		if _, err = outflow(c, p, sku, qty[1:]); err != nil {
			return nil, err
		}
		// outflow wrote the negative movement already
	} else {
		var v string
		if haveCost { // stock added at a stated cost (opening stock, a found item)
			err = c.Tx.QueryRow(c.Context, "select round($1::numeric * $2::bigint)::bigint::text", qty, cost).Scan(&v)
		} else {
			if v, err = avgValue(c, item, qty); err == nil && v == "0" {
				var any bool
				if err = c.Tx.QueryRow(c.Context, "select coalesce(sum(qty), 0) > 0 from inventory.movements where item_id = $1::uuid", item).Scan(&any); err == nil && !any {
					return nil, command.ErrInvalid("unit_cost_minor is required when the item has no stock yet")
				}
			}
		}
		if err != nil {
			return nil, command.DB(err)
		}
		p.qty, p.value = qty, v
		if _, err = insertMovement(c, p); err != nil {
			return nil, err
		}
	}
	if _, err := c.Emit("stock.adjusted", nil, map[string]any{"item_id": item, "location_id": loc, "qty": qty, "reason": reason}); err != nil {
		return nil, err
	}
	return itemGet(c, mustJSON(map[string]any{"id": item}))
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func locationOK(c *command.Ctx, id string) error {
	var active bool
	err := c.Tx.QueryRow(c.Context, "select active from inventory.locations where id = $1::uuid", id).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return command.ErrNotFound("location")
	}
	if err != nil {
		return command.DB(err)
	}
	if !active {
		return command.ErrConflict("location is archived")
	}
	return nil
}

func transfer(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	item, err := o.id("item_id", true)
	if err != nil {
		return nil, err
	}
	from, err := o.id("from_location_id", true)
	if err != nil {
		return nil, err
	}
	to, err := o.id("to_location_id", true)
	if err != nil {
		return nil, err
	}
	if from == to {
		return nil, command.ErrInvalid("choose two different locations")
	}
	qty, ok, err := o.qty("qty", false, false)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, command.ErrInvalid("qty is required")
	}
	reason, err := o.str("reason", 300)
	if err != nil {
		return nil, err
	}
	sku, err := lockItem(c, item)
	if err != nil {
		return nil, err
	}
	for _, l := range []string{from, to} {
		if err := locationOK(c, l); err != nil {
			return nil, err
		}
	}
	var tid string
	if err := c.Tx.QueryRow(c.Context, "select gen_random_uuid()::text").Scan(&tid); err != nil {
		return nil, command.DB(err)
	}
	v, err := outflow(c, posting{itemID: item, locID: from, kind: "transfer_out", transferID: tid, reason: reason}, sku, qty)
	if err != nil {
		return nil, err
	}
	if _, err := insertMovement(c, posting{itemID: item, locID: to, kind: "transfer_in", qty: qty, value: v, transferID: tid, reason: reason}); err != nil {
		return nil, err
	}
	if _, err := c.Emit("stock.transferred", nil, map[string]any{"item_id": item, "from": from, "to": to, "qty": qty}); err != nil {
		return nil, err
	}
	return itemGet(c, mustJSON(map[string]any{"id": item}))
}

// ---------- reads ----------

type movementOut struct {
	ID         string     `json:"id"`
	ItemID     string     `json:"item_id"`
	SKU        string     `json:"sku"`
	ItemName   string     `json:"item_name"`
	LocationID string     `json:"location_id"`
	Location   string     `json:"location"`
	Kind       string     `json:"kind"`
	Qty        string     `json:"qty"`
	Reason     *string    `json:"reason"`
	SourceType *string    `json:"source_type"`
	SourceID   *string    `json:"source_id"`
	TransferID *string    `json:"transfer_id"`
	Reverses   *string    `json:"reverses"`
	CreatedAt  command.Ts `json:"created_at"`
}

// movements lists newest first. The ledger shows quantities only: cost is in report.stock_valuation (finance access).
func movements(c *command.Ctx, item, loc string, limit int64) ([]movementOut, error) {
	rows, err := c.Tx.Query(c.Context, `select m.id::text, m.item_id::text, i.sku, i.name, m.location_id::text, l.name, m.kind, m.qty::text,
		m.reason, m.source_type, m.source_id::text, m.transfer_id::text, m.reverses::text, m.created_at
		from inventory.movements m join inventory.items i on i.id = m.item_id join inventory.locations l on l.id = m.location_id
		where ($1 = '' or m.item_id = $1::uuid) and ($2 = '' or m.location_id = $2::uuid)
		order by m.created_at desc, m.id desc limit $3`, item, loc, limit)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []movementOut{}
	for rows.Next() {
		var m movementOut
		var at time.Time
		if err := rows.Scan(&m.ID, &m.ItemID, &m.SKU, &m.ItemName, &m.LocationID, &m.Location, &m.Kind, &m.Qty, &m.Reason, &m.SourceType, &m.SourceID, &m.TransferID, &m.Reverses, &at); err != nil {
			return nil, command.DB(err)
		}
		m.CreatedAt = command.Ts(at)
		out = append(out, m)
	}
	return out, command.DB(rows.Err())
}

func movementList(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	item, err := o.id("item_id", false)
	if err != nil {
		return nil, err
	}
	loc, err := o.id("location_id", false)
	if err != nil {
		return nil, err
	}
	limit, ok, err := o.int("limit", 1, 500)
	if err != nil {
		return nil, err
	}
	if !ok {
		limit = 100
	}
	return movements(c, item, loc, limit)
}

// valuation: stock value at weighted average cost, per item. Finance-gated by name (markFinance).
func valuation(c *command.Ctx, b json.RawMessage) (any, error) {
	if _, err := decode(b); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select i.id::text, i.sku, i.name, i.unit, coalesce(s.q, 0)::text, coalesce(s.v, 0)::bigint,
		case when coalesce(s.q, 0) > 0 then round(s.v / s.q)::bigint else 0 end
		from inventory.items i left join (select item_id, sum(qty) q, sum(value_minor) v from inventory.movements group by item_id) s on s.item_id = i.id
		where i.active or coalesce(s.q, 0) <> 0 order by lower(i.name), i.id`)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type row struct {
		ItemID       string `json:"item_id"`
		SKU          string `json:"sku"`
		Name         string `json:"name"`
		Unit         string `json:"unit"`
		OnHand       string `json:"on_hand"`
		ValueMinor   int64  `json:"value_minor"`
		AvgCostMinor int64  `json:"avg_cost_minor"`
	}
	items, total := []row{}, int64(0)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.ItemID, &r.SKU, &r.Name, &r.Unit, &r.OnHand, &r.ValueMinor, &r.AvgCostMinor); err != nil {
			return nil, command.DB(err)
		}
		total += r.ValueMinor
		items = append(items, r)
	}
	return map[string]any{"method": "weighted_average", "items": items, "total_minor": total}, command.DB(rows.Err())
}

// LowStock is the home tile: active items at or under their reorder level (a level of 0 means no alert), the number of
// active items, and the three lowest. Reports nothing (total 0) for a workspace that has no items.
type LowItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	OnHand       string `json:"on_hand"`
	ReorderLevel string `json:"reorder_level"`
}

func LowStock(c *command.Ctx) (count, total int64, sample []LowItem, err error) {
	sample = []LowItem{}
	if err = c.Tx.QueryRow(c.Context, "select count(*) from inventory.items where active").Scan(&total); err != nil || total == 0 {
		return 0, total, sample, command.DB(err)
	}
	it, err := itemList(c, mustJSON(map[string]any{"low": true, "limit": 500}))
	if err != nil {
		return 0, total, sample, err
	}
	for _, i := range it.([]itemOut) {
		count++
		if len(sample) < 3 {
			sample = append(sample, LowItem{i.ID, i.Name, i.OnHand, i.ReorderLevel})
		}
	}
	return count, total, sample, nil
}
