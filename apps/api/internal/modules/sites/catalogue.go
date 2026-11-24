package sites

// Catalogue (design section 2, revised by 17.5): the owner picks inventory items; publishing copies a snapshot that
// visitors read. Stock, SKU, cost and tax details never enter this module: items are read through the narrow command
// stock_item.public_list. With price mode "hide" no number is stored in the snapshot.

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
)

// Plan limits for catalogue items (design section 0).
const (
	itemsIncluded = 12
	itemsPro      = 60
)

func itemLimit(pro bool) int {
	if pro {
		return itemsPro
	}
	return itemsIncluded
}

const (
	priceIncl = "show_incl"
	pricePlus = "show_plus"
	priceHide = "hide"
)

var priceModes = []string{priceIncl, pricePlus, priceHide}

// snapItem is one item of the public snapshot. PriceMinor is the amount shown, in paise, and is absent for "Ask for price".
type snapItem struct {
	ID          string `json:"id"` // same as item_id; the builder reads id
	ItemID      string `json:"item_id"`
	Name        string `json:"name"`
	Unit        string `json:"unit"`
	PriceMinor  *int64 `json:"price_minor,omitempty"`
	Mode        string `json:"mode"`
	Category    string `json:"category"`
	Description string `json:"description"`
	ImageID     string `json:"image_id,omitempty"`
	Position    int    `json:"position"`
}

type selItem struct {
	ItemID, Category, Description, ImageID string
	Position                               int
}

type liveItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Unit       string `json:"unit"`
	PriceMinor int64  `json:"price_minor"`
	GSTRateBP  int64  `json:"gst_rate_bp"`
}

type change struct {
	ItemID string `json:"item_id"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // price, renamed, removed or added
	Old    string `json:"old,omitempty"`
	New    string `json:"new,omitempty"`
}

func CatalogueCommands() []command.Command {
	R, W := command.GroupRead, command.GroupWriteItems
	id := str("site id")
	return []command.Command{
		mk("site.catalog_get", R, command.Member, false, "The catalogue of the website: the picked items with their live name and price, items that can be added, the price mode and what changed since the snapshot visitors see.",
			obj([]string{"id"}, props{"id": id, "q": str("search the items that can be added"), "cursor": str("next_cursor of the previous page of candidates")}), catalogGet),
		mk("site.catalog_set", W, command.Member, false, "Replace the picked items (array order is display order) and optionally the price mode: show_incl, show_plus or hide.",
			obj([]string{"id", "items"}, props{"id": id, "price_mode": str("show_incl, show_plus or hide"), "items": props{"type": "array", "items": props{"type": "object"}, "description": "{item_id, category?, description?, image_id?}"}}), catalogSet),
		mk("site.catalog_sync", W, command.Admin, true, "Update prices: re-read the picked items and replace the snapshot visitors see, without publishing page edits. Returns what changed.",
			obj([]string{"id"}, props{"id": id}), catalogSync),
	}
}

func catalogPriceMode(c *command.Ctx, siteID string) (mode string, synced *time.Time, snap []snapItem, err error) {
	var raw []byte
	err = c.Tx.QueryRow(c.Context, "select catalog_price, catalog_synced_at, catalog_published from sites.sites where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), siteID).Scan(&mode, &synced, &raw)
	if err == nil && len(raw) > 0 {
		err = json.Unmarshal(raw, &snap)
	}
	return mode, synced, snap, command.DB(err)
}

func selection(c *command.Ctx, siteID string) ([]selItem, error) {
	rows, err := c.Tx.Query(c.Context, `select item_id::text, position, category, description, coalesce(image_id::text, '') from sites.catalog_items
		where workspace_id = $1::uuid and site_id = $2::uuid order by position, item_id`, c.WorkspaceID(), siteID)
	if err != nil {
		return nil, command.DB(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (s selItem, err error) {
		err = r.Scan(&s.ItemID, &s.Position, &s.Category, &s.Description, &s.ImageID)
		return
	})
	return out, command.DB(err)
}

// liveItems reads active items by id (never by search: a selection does not depend on paging).
func liveItems(c *command.Ctx, ids []string) (map[string]liveItem, error) {
	out := map[string]liveItem{}
	if len(ids) == 0 {
		return out, nil
	}
	raw, err := c.Call("stock_item.public_list", props{"ids": ids})
	if err != nil {
		return nil, err
	}
	var r struct{ Items []liveItem }
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	for _, it := range r.Items {
		out[it.ID] = it
	}
	return out, nil
}

// shownPrice is the amount a visitor sees, in paise: price plus GST rounded half-up to the paisa (the rule of
// billing.taxPaise, whose test table the catalogue test repeats), or the bare price. ok is false for "Ask for price".
func shownPrice(mode string, priceMinor, rateBP int64) (amount int64, ok bool) {
	if mode == priceHide || priceMinor <= 0 {
		return 0, false
	}
	if mode == pricePlus {
		return priceMinor, true
	}
	x := new(big.Int).Mul(big.NewInt(priceMinor), big.NewInt(rateBP))
	x.Add(x, big.NewInt(5000)).Div(x, big.NewInt(10000))
	x.Add(x, big.NewInt(priceMinor))
	if !x.IsInt64() {
		return 0, false
	}
	return x.Int64(), true
}

// snapshot re-reads the picked items and builds what visitors will see. Items archived or deleted are dropped. The
// caller holds the site row lock.
func snapshot(c *command.Ctx, siteID string) ([]snapItem, error) {
	mode, _, _, err := catalogPriceMode(c, siteID)
	if err != nil {
		return nil, err
	}
	sel, err := selection(c, siteID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(sel))
	for i, s := range sel {
		ids[i] = s.ItemID
	}
	live, err := liveItems(c, ids)
	if err != nil {
		return nil, err
	}
	out := []snapItem{}
	for _, s := range sel {
		it, ok := live[s.ItemID]
		if !ok {
			continue
		}
		x := snapItem{ID: s.ItemID, ItemID: s.ItemID, Name: it.Name, Unit: it.Unit, Mode: mode, Category: s.Category, Description: s.Description, ImageID: s.ImageID, Position: len(out)}
		if amount, ok := shownPrice(mode, it.PriceMinor, it.GSTRateBP); ok {
			x.PriceMinor = &amount
		} else {
			x.Mode = priceHide
		}
		out = append(out, x)
	}
	return out, nil
}

func priceText(x snapItem) string {
	if x.PriceMinor == nil {
		return "Ask for price"
	}
	s := FormatINR(*x.PriceMinor)
	if x.Mode == pricePlus {
		s += " + GST"
	}
	return s
}

// FormatINR prints paise as rupees with Indian digit grouping: 12345600 is "₹1,23,456".
func FormatINR(paise int64) string {
	r, p := paise/100, paise%100
	s := strconv.FormatInt(r, 10)
	if len(s) > 3 {
		head, tail := s[:len(s)-3], s[len(s)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		if head != "" {
			parts = append([]string{head}, parts...)
		}
		s = strings.Join(parts, ",") + "," + tail
	}
	if p != 0 {
		s += fmt.Sprintf(".%02d", p)
	}
	return "₹" + s
}

// diffSnapshots lists what a re-snapshot would change: prices, names, removed and added items.
func diffSnapshots(old, next []snapItem) []change {
	byID := map[string]snapItem{}
	for _, o := range old {
		byID[o.ItemID] = o
	}
	out := []change{}
	seen := map[string]bool{}
	for _, n := range next {
		seen[n.ItemID] = true
		o, ok := byID[n.ItemID]
		switch {
		case !ok:
			out = append(out, change{ItemID: n.ItemID, Name: n.Name, Kind: "added", New: priceText(n)})
		case o.Name != n.Name:
			out = append(out, change{ItemID: n.ItemID, Name: n.Name, Kind: "renamed", Old: o.Name, New: n.Name})
		case priceText(o) != priceText(n) || o.Unit != n.Unit:
			out = append(out, change{ItemID: n.ItemID, Name: n.Name, Kind: "price", Old: priceText(o), New: priceText(n)})
		}
	}
	for _, o := range old {
		if !seen[o.ItemID] {
			out = append(out, change{ItemID: o.ItemID, Name: o.Name, Kind: "removed", Old: priceText(o)})
		}
	}
	return out
}

func catalogState(c *command.Ctx, site siteOut, q, cursor string) (props, error) {
	mode, synced, snap, err := catalogPriceMode(c, site.ID)
	if err != nil {
		return nil, err
	}
	sel, err := selection(c, site.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(sel))
	picked := map[string]bool{}
	for i, s := range sel {
		ids[i], picked[s.ItemID] = s.ItemID, true
	}
	live, err := liveItems(c, ids)
	if err != nil {
		return nil, err
	}
	known := map[string]snapItem{}
	for _, s := range snap {
		known[s.ItemID] = s
	}
	items := make([]props, 0, len(sel))
	for _, s := range sel {
		x := props{"item_id": s.ItemID, "position": s.Position, "category": s.Category, "description": s.Description, "image_id": nilIfEmpty(s.ImageID)}
		if it, ok := live[s.ItemID]; ok {
			x["name"], x["unit"], x["price_minor"], x["gst_rate_bp"], x["missing"] = it.Name, it.Unit, it.PriceMinor, it.GSTRateBP, false
		} else {
			x["name"], x["unit"], x["missing"] = known[s.ItemID].Name, known[s.ItemID].Unit, true
		}
		items = append(items, x)
	}
	candidates := []liveItem{}
	lp := props{"q": q, "limit": 100}
	if cursor != "" {
		var cur any
		if json.Unmarshal([]byte(cursor), &cur) != nil {
			return nil, command.ErrInvalid("invalid cursor")
		}
		lp["cursor"] = cur
	}
	raw, err := c.Call("stock_item.public_list", lp)
	if err != nil {
		return nil, err
	}
	var r struct {
		Items      []liveItem
		NextCursor json.RawMessage `json:"next_cursor"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, err
	}
	var next any
	if len(r.NextCursor) > 0 && string(r.NextCursor) != "null" {
		next = string(r.NextCursor)
	}
	for _, it := range r.Items {
		if !picked[it.ID] {
			candidates = append(candidates, it)
		}
	}
	changes := []change{}
	if snap != nil {
		next, err := snapshot(c, site.ID)
		if err != nil {
			return nil, err
		}
		changes = diffSnapshots(snap, next)
	}
	if snap == nil {
		snap = []snapItem{}
	}
	return props{"price_mode": mode, "limit": itemLimit(site.Pro), "items": items, "candidates": candidates, "next_cursor": next, "synced_at": synced,
		"changed": len(changes), "changes": changes, "snapshot": snap}, nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func catalogGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID, Q, Cursor string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if utf8.RuneCountInString(in.Q) > 80 {
		return nil, command.ErrInvalid("q is too long")
	}
	site, _, err := loadSite(c, in.ID, false)
	if err != nil {
		return nil, err
	}
	return catalogState(c, site, in.Q, in.Cursor)
}

func catalogSet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID        string
		PriceMode string `json:"price_mode"`
		Items     []struct {
			ItemID      string  `json:"item_id"`
			Category    string  `json:"category"`
			Description string  `json:"description"`
			ImageID     *string `json:"image_id"`
		} `json:"items"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.PriceMode != "" && !contains(priceModes, in.PriceMode) {
		return nil, command.ErrInvalid("price_mode must be show_incl, show_plus or hide")
	}
	site, _, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	before, err := selection(c, site.ID)
	if err != nil {
		return nil, err
	}
	if lim := itemLimit(site.Pro); len(in.Items) > lim && len(in.Items) > len(before) {
		return nil, command.NewError(command.PlanLimit, fmt.Sprintf("Your plan allows %d items on the website.", lim))
	}
	was := map[string]bool{}
	for _, b := range before {
		was[b.ItemID] = true
	}
	seen := map[string]bool{}
	ids := []string{}
	for i := range in.Items {
		it := &in.Items[i]
		it.ItemID = strings.ToLower(it.ItemID)
		if !uuidRe.MatchString(it.ItemID) || seen[it.ItemID] {
			return nil, command.ErrInvalid("each item must be listed once, by id")
		}
		seen[it.ItemID] = true
		if !was[it.ItemID] { // a new pick must be an active item; one already picked may have gone missing
			ids = append(ids, it.ItemID)
		}
		it.Category = strings.TrimSpace(stripControl(it.Category))
		it.Description = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(it.Description, "\r\n", "\n"), "\r", "\n"))
		if utf8.RuneCountInString(it.Category) > 40 {
			return nil, command.ErrInvalid("a category may be at most 40 characters")
		}
		if utf8.RuneCountInString(it.Description) > 300 {
			return nil, command.ErrInvalid("a description may be at most 300 characters")
		}
		if strings.ContainsFunc(strings.ReplaceAll(it.Description, "\n", ""), func(r rune) bool { return r < ' ' || r == 0x7f }) {
			return nil, command.ErrInvalid("a description has characters that are not allowed")
		}
	}
	live, err := liveItems(c, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := live[id]; !ok {
			return nil, command.ErrInvalid("an item you picked is not available (it may be archived)")
		}
	}
	owned, err := siteImages(c, site.ID)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "delete from sites.catalog_items where workspace_id = $1::uuid and site_id = $2::uuid", c.WorkspaceID(), site.ID); err != nil {
		return nil, command.DB(err)
	}
	for i, it := range in.Items {
		var img *string
		if it.ImageID != nil && *it.ImageID != "" {
			id := strings.ToLower(*it.ImageID)
			if !owned[id] {
				return nil, command.ErrInvalid("an item photo is not on this website")
			}
			img = &id
		}
		if _, err := c.Tx.Exec(c.Context, `insert into sites.catalog_items (workspace_id, site_id, item_id, position, category, description, image_id)
			values ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7::uuid)`, c.WorkspaceID(), site.ID, it.ItemID, i, it.Category, it.Description, img); err != nil {
			return nil, command.DB(err)
		}
	}
	if in.PriceMode != "" {
		if _, err := c.Tx.Exec(c.Context, "update sites.sites set catalog_price = $3 where workspace_id = $1::uuid and id = $2::uuid", c.WorkspaceID(), site.ID, in.PriceMode); err != nil {
			return nil, command.DB(err)
		}
	}
	if _, err := c.Emit("site.catalog_changed", nil, props{"site_id": site.ID}); err != nil {
		return nil, err
	}
	return catalogState(c, site, "", "")
}

func catalogSync(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct{ ID string }
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	site, _, err := loadSite(c, in.ID, true)
	if err != nil {
		return nil, err
	}
	if site.PublishedVersionID == nil {
		return nil, command.ErrConflict("Prices are taken when you publish your website.")
	}
	pub, err := versionData(c, site.ID, *site.PublishedVersionID)
	if err != nil {
		return nil, err
	}
	if !HasBlock(pub, "Catalogue") {
		return nil, command.ErrConflict("The published page has no Catalogue section. Publish it first.")
	}
	_, _, old, err := catalogPriceMode(c, site.ID)
	if err != nil {
		return nil, err
	}
	next, err := snapshot(c, site.ID)
	if err != nil {
		return nil, err
	}
	var synced time.Time
	if err := c.Tx.QueryRow(c.Context, `update sites.sites set catalog_published = $3::jsonb, catalog_synced_at = now() where workspace_id = $1::uuid and id = $2::uuid returning catalog_synced_at`,
		c.WorkspaceID(), site.ID, mustJSON(next)).Scan(&synced); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("site.catalog_synced", nil, props{"site_id": site.ID}); err != nil {
		return nil, err
	}
	return props{"synced_at": synced, "count": len(next), "changes": diffSnapshots(old, next)}, nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// aboutLine is the first line of a lead that came from "Ask a question" on an item. The name and price come from the
// published snapshot, never from the client; an unknown id yields "".
func aboutLine(snap []byte, itemID string) string {
	var items []snapItem
	if len(snap) == 0 || json.Unmarshal(snap, &items) != nil {
		return ""
	}
	for _, x := range items {
		if x.ItemID == strings.ToLower(itemID) {
			if x.PriceMinor == nil {
				return "About: " + x.Name
			}
			return "About: " + x.Name + " (" + priceText(x) + ")"
		}
	}
	return ""
}
