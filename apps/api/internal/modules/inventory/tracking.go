package inventory

// WP21: batch and serial tracking. An item is tracked "none", "batch" or "serial" (inventory.items.tracking). Tracked
// movements carry batch_id or serial; stock per batch or serial is still the sum of the ledger. Valuation stays
// weighted average per item: a tracked issue is valued exactly like an untracked one, leg by leg.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// Tracking is what a document line (or a manual movement) says about batches and serials.
//
//	receipt: {"batch":"B-17","mfg_date":"2026-01-01","expiry_date":"2027-01-01"} or {"serials":["S1","S2"]}
//	issue:   nothing (batch items pick first-expiry-first-out), or {"picks":[{"batch_id":"..","qty":"3"}]},
//	         or {"serials":["S1"]}; admins may add override_expired_reason to issue expired stock.
type Tracking struct {
	Batch           string   `json:"batch"`
	MfgDate         string   `json:"mfg_date"`
	ExpiryDate      string   `json:"expiry_date"`
	Serials         []string `json:"serials"`
	Picks           []Pick   `json:"picks"`
	OverrideExpired string   `json:"override_expired_reason"`
}

type Pick struct {
	BatchID string      `json:"batch_id"`
	Qty     json.Number `json:"qty"`
}

type leg struct {
	batch, serial, qty string // qty positive text
	expired            bool
}

const istToday = "(now() at time zone 'Asia/Kolkata')::date"

func parseTracking(raw json.RawMessage) (Tracking, error) {
	var t Tracking
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return t, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&t) != nil {
		return t, command.ErrInvalid("tracking must be an object with batch, mfg_date, expiry_date, serials, picks or override_expired_reason")
	}
	t.Batch, t.OverrideExpired = strings.TrimSpace(t.Batch), strings.TrimSpace(t.OverrideExpired)
	return t, nil
}

func (t Tracking) empty() bool {
	return t.Batch == "" && t.MfgDate == "" && t.ExpiryDate == "" && len(t.Serials) == 0 && len(t.Picks) == 0 && t.OverrideExpired == ""
}

// milli reads a non-negative decimal quantity as thousandths (quantities carry at most 3 decimals).
func milli(s string) (int64, bool) {
	i, f, _ := strings.Cut(strings.TrimSpace(s), ".")
	f = strings.TrimRight(f, "0")
	if len(f) > 3 || i == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(i+f+strings.Repeat("0", 3-len(f)), 10, 64)
	return n, err == nil && n >= 0
}

func unmilli(n int64) string {
	s := strings.TrimRight(fmt.Sprintf("%d.%03d", n/1000, n%1000), "0")
	return strings.TrimSuffix(s, ".")
}

func itemMode(c *command.Ctx, id string) (string, error) {
	var m string
	err := c.Tx.QueryRow(c.Context, "select tracking from inventory.items where id = $1::uuid", id).Scan(&m)
	return m, command.DB(err)
}

func dateOrNil(label, s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil, command.ErrInvalid(label + " must be a date, YYYY-MM-DD")
	}
	return &d, nil
}

// serialList trims and checks serials: 1 to 60 characters, none repeated.
func serialList(sku string, in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s == "" || len([]rune(s)) > 60 {
			return nil, command.ErrInvalid("a serial must be 1 to 60 characters (" + sku + ")")
		}
		if seen[s] {
			return nil, command.ErrInvalid("serial " + s + " is listed twice")
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}

func serialCount(sku, qty string, serials []string) error {
	n, ok := milli(qty)
	if !ok || n%1000 != 0 || n/1000 != int64(len(serials)) {
		return command.ErrInvalid(fmt.Sprintf("%s is tracked by serial: give exactly one serial per unit (%s units, %d serials)", sku, qty, len(serials)))
	}
	return nil
}

// receiveLegs turns a receipt of qty into legs: one batch leg (the batch is created on first sight), or one leg per
// serial. A serial that is in stock anywhere cannot be received again. The caller holds the item lock, so two
// receipts of the same serial queue and the second sees the first.
func receiveLegs(c *command.Ctx, item, sku, mode, qty string, t Tracking) ([]leg, error) {
	switch mode {
	case "batch":
		if t.Batch == "" {
			return nil, command.ErrInvalid("batch number is required for " + sku)
		}
		if len(t.Serials) > 0 || len(t.Picks) > 0 || t.OverrideExpired != "" {
			return nil, command.ErrInvalid("a receipt of " + sku + " takes batch, mfg_date and expiry_date only")
		}
		if len([]rune(t.Batch)) > 40 {
			return nil, command.ErrInvalid("batch number is too long")
		}
		mfg, err := dateOrNil("mfg_date", t.MfgDate)
		if err != nil {
			return nil, err
		}
		exp, err := dateOrNil("expiry_date", t.ExpiryDate)
		if err != nil {
			return nil, err
		}
		if mfg != nil && exp != nil && exp.Before(*mfg) {
			return nil, command.ErrInvalid("expiry_date is before mfg_date")
		}
		var id string
		err = c.Tx.QueryRow(c.Context, `insert into inventory.batches (workspace_id, item_id, number, mfg_date, expiry_date)
			values ($1::uuid, $2::uuid, $3, nullif($4, '')::date, nullif($5, '')::date)
			on conflict (workspace_id, item_id, lower(number)) do nothing returning id::text`, c.WorkspaceID(), item, t.Batch, t.MfgDate, t.ExpiryDate).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) { // the batch exists: a new receipt may not contradict its dates
			var m, e *string
			if err = c.Tx.QueryRow(c.Context, "select id::text, mfg_date::text, expiry_date::text from inventory.batches where item_id = $1::uuid and lower(number) = lower($2)",
				item, t.Batch).Scan(&id, &m, &e); err != nil {
				return nil, command.DB(err)
			}
			if (t.MfgDate != "" && (m == nil || *m != t.MfgDate)) || (t.ExpiryDate != "" && (e == nil || *e != t.ExpiryDate)) {
				return nil, command.ErrConflict("batch " + t.Batch + " of " + sku + " already exists with different dates")
			}
			err = nil
		}
		if err != nil {
			return nil, command.DB(err)
		}
		return []leg{{batch: id, qty: qty}}, nil
	case "serial":
		if t.Batch != "" || t.MfgDate != "" || t.ExpiryDate != "" || len(t.Picks) > 0 || t.OverrideExpired != "" {
			return nil, command.ErrInvalid("a receipt of " + sku + " takes serials only")
		}
		ss, err := serialList(sku, t.Serials)
		if err != nil {
			return nil, err
		}
		if err := serialCount(sku, qty, ss); err != nil {
			return nil, err
		}
		legs := []leg{}
		for _, s := range ss {
			var have int64
			if err := c.Tx.QueryRow(c.Context, "select coalesce(sum(qty), 0)::bigint from inventory.movements where item_id = $1::uuid and serial = $2", item, s).Scan(&have); err != nil {
				return nil, command.DB(err)
			}
			if have > 0 {
				return nil, command.ErrConflict("serial " + s + " of " + sku + " is already in stock")
			}
			legs = append(legs, leg{serial: s, qty: "1"})
		}
		return legs, nil
	}
	if !t.empty() {
		return nil, command.ErrInvalid(sku + " is not tracked by batch or serial: remove the tracking details")
	}
	return nil, nil
}

// issueLegs picks what leaves loc. Batch items: the given picks, else first-expiry-first-out (batches without an
// expiry go last). With guardExpired (invoices), expired batches are refused unless an admin gives
// override_expired_reason; FEFO skips expired batches on its own. Serial items: the serials must be in stock at loc.
func issueLegs(c *command.Ctx, item, sku, mode, loc, qty string, t Tracking, guardExpired bool) ([]leg, error) {
	switch mode {
	case "batch":
		if t.Batch != "" || t.MfgDate != "" || t.ExpiryDate != "" || len(t.Serials) > 0 {
			return nil, command.ErrInvalid("an issue of " + sku + " takes picks only")
		}
		allowExpired := !guardExpired
		if guardExpired && t.OverrideExpired != "" {
			if c.Principal.Role < command.Admin {
				return nil, command.ErrForbidden("only an admin can issue expired stock")
			}
			if len([]rune(t.OverrideExpired)) > 200 {
				return nil, command.ErrInvalid("override_expired_reason is too long")
			}
			allowExpired = true
		}
		want, ok := milli(qty)
		if !ok || want <= 0 {
			return nil, command.ErrInvalid("quantity must be positive")
		}
		rows, err := c.Tx.Query(c.Context, `select b.id::text, b.number, coalesce(b.expiry_date::text, ''),
			b.expiry_date is not null and b.expiry_date < `+istToday+`, sum(m.qty)::text
			from inventory.movements m join inventory.batches b on b.id = m.batch_id
			where m.item_id = $1::uuid and m.location_id = $2::uuid group by b.id having sum(m.qty) > 0
			order by b.expiry_date nulls last, b.created_at, b.number`, item, loc)
		if err != nil {
			return nil, command.DB(err)
		}
		type avail struct {
			id, number, expiry string
			expired            bool
			qty                int64
		}
		var av []avail
		for rows.Next() {
			var a avail
			var q string
			if err := rows.Scan(&a.id, &a.number, &a.expiry, &a.expired, &q); err != nil {
				rows.Close()
				return nil, command.DB(err)
			}
			a.qty, _ = milli(q)
			av = append(av, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, command.DB(err)
		}
		var legs []leg
		if len(t.Picks) == 0 {
			var usable, expired int64
			for _, a := range av {
				if a.expired && !allowExpired {
					expired += a.qty
					continue
				}
				usable += a.qty
				if take := min(a.qty, want); take > 0 {
					legs = append(legs, leg{batch: a.id, qty: unmilli(take), expired: a.expired && guardExpired})
					want -= take
				}
				if want == 0 {
					break
				}
			}
			if want > 0 {
				msg := fmt.Sprintf("not enough batch stock of %s: %s available", sku, unmilli(usable))
				if expired > 0 {
					msg += fmt.Sprintf(" (%s more is expired and cannot be issued)", unmilli(expired))
				}
				return nil, command.ErrConflict(msg)
			}
			return legs, nil
		}
		byID := map[string]*avail{}
		for i := range av {
			byID[av[i].id] = &av[i]
		}
		for _, p := range t.Picks {
			a, pq := byID[strings.ToLower(strings.TrimSpace(p.BatchID))], int64(0)
			if pq, ok = milli(p.Qty.String()); !ok || pq <= 0 {
				return nil, command.ErrInvalid("each pick needs a positive qty")
			}
			if a == nil {
				return nil, command.ErrConflict("a picked batch of " + sku + " has no stock here")
			}
			if a.expired && !allowExpired {
				return nil, command.ErrConflict("batch " + a.number + " of " + sku + " expired on " + a.expiry + " and cannot be issued")
			}
			if pq > a.qty {
				return nil, command.ErrConflict(fmt.Sprintf("not enough stock of %s batch %s: %s on hand, %s picked", sku, a.number, unmilli(a.qty), unmilli(pq)))
			}
			a.qty -= pq // a batch picked twice cannot exceed what it holds
			want -= pq
			legs = append(legs, leg{batch: a.id, qty: unmilli(pq), expired: a.expired && guardExpired})
		}
		if want != 0 {
			return nil, command.ErrInvalid("the picks must add up to the quantity of " + sku)
		}
		return legs, nil
	case "serial":
		if t.Batch != "" || t.MfgDate != "" || t.ExpiryDate != "" || len(t.Picks) > 0 || t.OverrideExpired != "" {
			return nil, command.ErrInvalid("an issue of " + sku + " takes serials only")
		}
		ss, err := serialList(sku, t.Serials)
		if err != nil {
			return nil, err
		}
		if err := serialCount(sku, qty, ss); err != nil {
			return nil, err
		}
		legs := []leg{}
		for _, s := range ss {
			var have int64
			if err := c.Tx.QueryRow(c.Context, "select coalesce(sum(qty), 0)::bigint from inventory.movements where item_id = $1::uuid and serial = $2 and location_id = $3::uuid",
				item, s, loc).Scan(&have); err != nil {
				return nil, command.DB(err)
			}
			if have < 1 {
				return nil, command.ErrConflict("serial " + s + " of " + sku + " is not in stock here")
			}
			legs = append(legs, leg{serial: s, qty: "1"})
		}
		return legs, nil
	}
	if !t.empty() {
		return nil, command.ErrInvalid(sku + " is not tracked by batch or serial: remove the tracking details")
	}
	return nil, nil
}

// postIn books receipt-like legs (receipt, adjustment, transfer_in): the total value is split over the legs, the
// odd paise on the first.
func postIn(c *command.Ctx, p posting, legs []leg, total int64) error {
	n := int64(len(legs))
	for i, l := range legs {
		v := total / n
		if i == 0 {
			v += total % n
		}
		p.batchID, p.serial, p.qty, p.value = l.batch, l.serial, l.qty, fmt.Sprint(v)
		if _, err := insertMovement(c, p); err != nil {
			return err
		}
	}
	return nil
}

// postOut books issue-like legs at the item's weighted average cost and returns each leg's value.
func postOut(c *command.Ctx, p posting, sku string, legs []leg) ([]string, error) {
	vals := make([]string, 0, len(legs))
	for _, l := range legs {
		q := p
		q.batchID, q.serial = l.batch, l.serial
		if l.expired {
			q.reason = "expired stock issued: " + q.reason
		}
		v, err := outflow(c, q, sku, l.qty)
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
	}
	return vals, nil
}

// ---------- reads ----------

type batchOut struct {
	ID         string  `json:"id"`
	ItemID     string  `json:"item_id"`
	SKU        string  `json:"sku"`
	ItemName   string  `json:"item_name"`
	Unit       string  `json:"unit"`
	Number     string  `json:"number"`
	MfgDate    *string `json:"mfg_date"`
	ExpiryDate *string `json:"expiry_date"`
	OnHand     string  `json:"on_hand"`
	DaysLeft   *int32  `json:"days_left"` // negative once expired
	Expired    bool    `json:"expired"`
}

// batchRows: batches with their on-hand quantity from the ledger, first-expiry-first. within >= 0 keeps batches that
// hold stock and expire within that many days (expired ones included).
func batchRows(c *command.Ctx, item, loc string, includeEmpty bool, within int64) ([]batchOut, error) {
	rows, err := c.Tx.Query(c.Context, `select b.id::text, b.item_id::text, i.sku, i.name, i.unit, b.number, b.mfg_date::text, b.expiry_date::text,
		coalesce(s.q, 0)::text, (b.expiry_date - `+istToday+`)::int
		from inventory.batches b join inventory.items i on i.id = b.item_id
		left join (select batch_id, sum(qty) q from inventory.movements where batch_id is not null and ($2 = '' or location_id = $2::uuid) group by batch_id) s on s.batch_id = b.id
		where ($1 = '' or b.item_id = $1::uuid) and ($3::bool or coalesce(s.q, 0) > 0)
		  and ($4 < 0 or (b.expiry_date is not null and b.expiry_date <= `+istToday+` + $4::int))
		order by b.expiry_date nulls last, lower(i.name), b.number limit 500`, item, loc, includeEmpty, within)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []batchOut{}
	for rows.Next() {
		var b batchOut
		if err := rows.Scan(&b.ID, &b.ItemID, &b.SKU, &b.ItemName, &b.Unit, &b.Number, &b.MfgDate, &b.ExpiryDate, &b.OnHand, &b.DaysLeft); err != nil {
			return nil, command.DB(err)
		}
		b.Expired = b.DaysLeft != nil && *b.DaysLeft < 0
		out = append(out, b)
	}
	return out, command.DB(rows.Err())
}

func batchList(c *command.Ctx, b json.RawMessage) (any, error) {
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
	empty, _, err := o.boolean("include_empty")
	if err != nil {
		return nil, err
	}
	return batchRows(c, item, loc, empty, -1)
}

// expiryReport: batches with stock that expire within days (default 30) or already have.
func expiryReport(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	days, ok, err := o.int("days", 0, 3650)
	if err != nil {
		return nil, err
	}
	if !ok {
		days = 30
	}
	rows, err := batchRows(c, "", "", false, days)
	if err != nil {
		return nil, err
	}
	return map[string]any{"days": days, "batches": rows}, nil
}

func serialList_(c *command.Ctx, b json.RawMessage) (any, error) {
	o, err := decode(b)
	if err != nil {
		return nil, err
	}
	item, err := o.id("item_id", true)
	if err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select m.serial, m.location_id::text, l.name from inventory.movements m join inventory.locations l on l.id = m.location_id
		where m.item_id = $1::uuid and m.serial is not null group by m.serial, m.location_id, l.name having sum(m.qty) > 0 order by m.serial limit 1000`, item)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	type row struct {
		Serial     string `json:"serial"`
		LocationID string `json:"location_id"`
		Location   string `json:"location"`
	}
	out := []row{}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.Serial, &r.LocationID, &r.Location); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

// ExpiryTile is the home tile: batches with stock that expire within days or already have, and the soonest three.
type ExpiryTile struct {
	Soon, Expired int64
	Sample        []batchOut
}

// Expiring reports nothing (tracked false) for a workspace that has no batch-tracked item.
func Expiring(c *command.Ctx, days int64) (t ExpiryTile, tracked bool, err error) {
	t.Sample = []batchOut{}
	if err = c.Tx.QueryRow(c.Context, "select exists (select 1 from inventory.items where active and tracking = 'batch')").Scan(&tracked); err != nil || !tracked {
		return t, false, command.DB(err)
	}
	rows, err := batchRows(c, "", "", false, days)
	if err != nil {
		return t, true, err
	}
	for _, b := range rows {
		if b.Expired {
			t.Expired++
		} else {
			t.Soon++
		}
		if len(t.Sample) < 3 {
			t.Sample = append(t.Sample, b)
		}
	}
	return t, true, nil
}
