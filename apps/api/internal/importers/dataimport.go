package importers

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/dataimport"
)

// Data importers (docs/design/importers.md): contacts, items, opening documents and Tally masters.
// A job is a row of importers.jobs (kind != 'notes') with one importers.rows row per record of the file.
// Everything here talks to other modules through their commands (c.Call); the owning modules' import-only commands
// (contact.import_*, stock_item.import_keys, stock.import_*, invoice.import_opening*, bill.import_opening*, ledger.*) do the writes.

const undoDays = 7

// ---- stored shapes ----

type dataInput struct {
	Headers   []string          `json:"headers,omitempty"`
	HeaderRow int               `json:"header_row,omitempty"`
	Mapping   map[string]int    `json:"mapping,omitempty"` // field key -> column index
	SpaceID   string            `json:"space_id,omitempty"`
	Options   map[string]string `json:"options,omitempty"` // default_kind / default_type
	Include   map[string]bool   `json:"include,omitempty"`
	Tally     *tallyMeta        `json:"tally,omitempty"`
}

type tallyMeta struct {
	Found    dataimport.Found `json:"found"`
	Warnings []string         `json:"warnings"`
}

type contactFields struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
	Company string `json:"company,omitempty"`
	GSTIN   string `json:"gstin,omitempty"`
	PAN     string `json:"pan,omitempty"`
	State   string `json:"state_code,omitempty"`
	Address string `json:"address,omitempty"`
}

type itemFields struct {
	SKU        string `json:"sku"`
	Name       string `json:"name"`
	HSN        string `json:"hsn,omitempty"`
	Unit       string `json:"unit,omitempty"`
	GSTRateBP  int    `json:"gst_rate_bp"`
	PriceMinor int64  `json:"price_minor"`
	QtyMilli   int64  `json:"qty_milli,omitempty"`
	ValueMinor int64  `json:"value_minor,omitempty"`
	Location   string `json:"location,omitempty"`
}

type docFields struct {
	DocType     string `json:"doc_type"`
	Party       string `json:"party"`
	Number      string `json:"number"`
	Date        string `json:"date"`
	DueDate     string `json:"due_date,omitempty"`
	AmountMinor int64  `json:"amount_minor"`
	ContactID   string `json:"contact_id,omitempty"`
}

type ledgerFields struct {
	Name  string `json:"name"`
	Class string `json:"class"`
	Dr    int64  `json:"dr_minor"`
	Cr    int64  `json:"cr_minor"`
	Code  string `json:"code,omitempty"` // set once applied: the account the balance went to
}

type dataJob struct {
	ID, Kind, Status string
	Filename         *string
	CreatedBy        *string
	CreatedAt        time.Time
	FinishedAt       *time.Time
	Input            dataInput
	Report           map[string]any
}

type rowRec struct {
	No     int
	Entity string
	Raw    json.RawMessage
	Fields json.RawMessage
	Status string
	Reason *string
}

func loadJob(c *command.Ctx, id string, lock bool) (*dataJob, error) {
	if id == "" {
		return nil, command.ErrInvalid("id is required")
	}
	q := `select id::text, kind, status, filename, created_by::text, created_at, finished_at, input, report from importers.jobs where id = $1::uuid and kind <> 'notes'`
	if lock {
		q += " for update"
	}
	var j dataJob
	var in, rep []byte
	err := c.Tx.QueryRow(c.Context, q, id).Scan(&j.ID, &j.Kind, &j.Status, &j.Filename, &j.CreatedBy, &j.CreatedAt, &j.FinishedAt, &in, &rep)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, command.ErrNotFound("import")
	}
	if err != nil {
		return nil, command.DB(err)
	}
	_ = json.Unmarshal(in, &j.Input)
	_ = json.Unmarshal(rep, &j.Report)
	return &j, nil
}

func loadRows(c *command.Ctx, id, where string, limit int, args ...any) ([]rowRec, error) {
	q := "select row_no, entity, raw, fields, status, reason from importers.rows where job_id = $1::uuid " + where + " order by row_no"
	if limit > 0 {
		q += fmt.Sprintf(" limit %d", limit)
	}
	rows, err := c.Tx.Query(c.Context, q, append([]any{id}, args...)...)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []rowRec
	for rows.Next() {
		var r rowRec
		if err := rows.Scan(&r.No, &r.Entity, &r.Raw, &r.Fields, &r.Status, &r.Reason); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, r)
	}
	return out, command.DB(rows.Err())
}

func saveInput(c *command.Ctx, j *dataJob) error {
	b, _ := json.Marshal(j.Input)
	_, err := c.Tx.Exec(c.Context, "update importers.jobs set input = $2 where id = $1::uuid", j.ID, b)
	return command.DB(err)
}

func errText(err error) string {
	var ce *command.Error
	if errors.As(err, &ce) {
		return ce.Message
	}
	return err.Error()
}

func isRate(err error) bool {
	var ce *command.Error
	return errors.As(err, &ce) && ce.Code == command.RateLimited
}

func callJSON(c *command.Ctx, name string, in any, out any) error {
	b, err := c.Call(name, in)
	if err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func validKind(k string) bool {
	return k == "contacts" || k == "items" || k == "opening_docs" || k == "tally"
}

// ---- upload ----

// DataUpload parses the file into a preview job. Nothing is written to the books.
func DataUpload(c *command.Ctx, kind, filename, spaceID string, data []byte) (any, error) {
	if !validKind(kind) {
		return nil, command.ErrInvalid("kind must be contacts, items, opening_docs or tally")
	}
	if len(data) == 0 {
		return nil, command.ErrInvalid("the file is empty")
	}
	if len(data) > dataimport.MaxBytes {
		return nil, command.ErrInvalid("file is larger than 10 MB")
	}
	if len(filename) > 200 {
		filename = filename[:200]
	}
	if spaceID == "" {
		if err := c.Tx.QueryRow(c.Context, "select id::text from core.spaces order by created_at, id limit 1").Scan(&spaceID); err != nil {
			return nil, command.ErrInvalid("create a space first: imported contacts and documents live in one")
		}
	}
	if err := access.AssertSpaceAccess(c, spaceID, true); err != nil {
		return nil, err
	}
	in := dataInput{SpaceID: spaceID}
	type pending struct {
		entity string
		raw    any
	}
	var recs []pending
	var rowNos []int
	if kind == "tally" {
		t, err := dataimport.ParseTally(data)
		if err != nil {
			return nil, command.ErrInvalid(err.Error())
		}
		in.Tally = &tallyMeta{Found: t.Found, Warnings: t.Warnings}
		for _, l := range t.Ledgers {
			e := "ledger"
			if l.Class == dataimport.ClassCustomer || l.Class == dataimport.ClassVendor {
				e = l.Class
			}
			recs = append(recs, pending{e, l})
		}
		for _, it := range t.Items {
			recs = append(recs, pending{"item", it})
		}
		if len(recs) > dataimport.MaxRows*2 {
			return nil, command.ErrInvalid("this file has too many records; export fewer masters")
		}
		for i := range recs {
			rowNos = append(rowNos, i+1)
		}
	} else {
		t, err := dataimport.ReadCSV(data, dataimport.Kind(kind))
		if err != nil {
			return nil, command.ErrInvalid(err.Error())
		}
		in.Headers, in.HeaderRow, in.Mapping = t.Headers, t.HeaderRow, dataimport.AutoMap(dataimport.Kind(kind), t.Headers)
		for i, r := range t.Rows {
			recs = append(recs, pending{"row", map[string]any{"cells": r}})
			rowNos = append(rowNos, t.RowNums[i])
		}
	}
	if len(recs) == 0 {
		return nil, command.ErrInvalid("nothing to import: the file has no records")
	}
	ib, _ := json.Marshal(in)
	var id string
	if err := c.Tx.QueryRow(c.Context,
		`insert into importers.jobs (workspace_id, source, kind, status, filename, input, created_by) values ($1::uuid,'data_import',$2,'preview',$3,$4,$5::uuid) returning id::text`,
		c.WorkspaceID(), kind, nilIfEmpty(filename), ib, c.Principal.UserID).Scan(&id); err != nil {
		return nil, command.DB(err)
	}
	b := &pgx.Batch{}
	for i, r := range recs {
		rb, _ := json.Marshal(r.raw)
		b.Queue("insert into importers.rows (workspace_id, job_id, row_no, entity, raw) values ($1::uuid,$2::uuid,$3,$4,$5)", c.WorkspaceID(), id, rowNos[i], r.entity, rb)
	}
	br := c.Tx.SendBatch(c.Context, b)
	for range recs {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return nil, command.DB(err)
		}
	}
	if err := br.Close(); err != nil {
		return nil, command.DB(err)
	}
	j, err := loadJob(c, id, false)
	if err != nil {
		return nil, err
	}
	if _, err := classify(c, j); err != nil {
		return nil, err
	}
	return jobView(c, j)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- classification: parse, validate, find duplicates ----

func normName(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) > 10 {
		d = d[len(d)-10:]
	}
	return d
}

type existingContact struct {
	ID, Name, GSTIN, Phone, Email string
	Vendor                        bool
}

func contactKeys(c *command.Ctx) ([]existingContact, error) {
	var out struct {
		Contacts []struct {
			ID     string  `json:"id"`
			Name   string  `json:"name"`
			GSTIN  *string `json:"gstin"`
			Phone  *string `json:"phone"`
			Email  *string `json:"email"`
			Vendor bool    `json:"is_vendor"`
		} `json:"contacts"`
	}
	if err := callJSON(c, "contact.import_keys", map[string]any{}, &out); err != nil {
		return nil, err
	}
	var l []existingContact
	for _, x := range out.Contacts {
		e := existingContact{ID: x.ID, Name: x.Name, Vendor: x.Vendor}
		if x.GSTIN != nil {
			e.GSTIN = *x.GSTIN
		}
		if x.Phone != nil {
			e.Phone = digits(*x.Phone)
		}
		if x.Email != nil {
			e.Email = strings.ToLower(*x.Email)
		}
		l = append(l, e)
	}
	return l, nil
}

// contactIndex finds a contact by GSTIN, else by normalised name plus phone or email.
type contactIndex struct {
	byGSTIN map[string]string
	byName  map[string][]existingContact
}

func newContactIndex(l []existingContact) *contactIndex {
	x := &contactIndex{byGSTIN: map[string]string{}, byName: map[string][]existingContact{}}
	for _, e := range l {
		x.add(e)
	}
	return x
}

func (x *contactIndex) add(e existingContact) {
	if e.GSTIN != "" {
		x.byGSTIN[e.GSTIN] = e.ID
	}
	n := normName(e.Name)
	x.byName[n] = append(x.byName[n], e)
}

func (x *contactIndex) find(name, gstin, phone, email string) (string, bool) {
	if gstin != "" {
		if id, ok := x.byGSTIN[gstin]; ok {
			return id, true
		}
	}
	phone, email = digits(phone), strings.ToLower(email)
	for _, e := range x.byName[normName(name)] {
		switch {
		case phone != "" && e.Phone == phone, email != "" && e.Email == email:
			return e.ID, true
		case phone == "" && email == "" && e.Phone == "" && e.Email == "":
			return e.ID, true
		}
	}
	return "", false
}

// byParty finds the contact an opening document names: GSTIN, else the name.
func (x *contactIndex) byParty(name, gstin string) (existingContact, bool) {
	if gstin != "" {
		if id, ok := x.byGSTIN[gstin]; ok {
			for _, l := range x.byName {
				for _, e := range l {
					if e.ID == id {
						return e, true
					}
				}
			}
		}
	}
	if l := x.byName[normName(name)]; len(l) > 0 {
		return l[0], true
	}
	return existingContact{}, false
}

type verdict struct {
	status, reason string
	fields         any
}

func fix(reasons []string) verdict {
	return verdict{status: "fix", reason: strings.Join(reasons, "; ")}
}

func tableOf(j *dataJob, rows []rowRec) *dataimport.Table {
	t := &dataimport.Table{Headers: j.Input.Headers, HeaderRow: j.Input.HeaderRow}
	for _, r := range rows {
		var raw struct {
			Cells []string `json:"cells"`
		}
		_ = json.Unmarshal(r.Raw, &raw)
		t.Rows = append(t.Rows, raw.Cells)
		t.RowNums = append(t.RowNums, r.No)
	}
	return t
}

func classify(c *command.Ctx, j *dataJob) (map[string]int, error) {
	rows, err := loadRows(c, j.ID, "", 0)
	if err != nil {
		return nil, err
	}
	vs := make([]verdict, len(rows))
	switch j.Kind {
	case "contacts":
		keys, err := contactKeys(c)
		if err != nil {
			return nil, err
		}
		idx := newContactIndex(keys)
		for i, p := range dataimport.ParseContacts(tableOf(j, rows), dataimport.Mapping(j.Input.Mapping), j.Input.Options["default_kind"]) {
			vs[i] = contactVerdict(idx, p.Errors, contactFields{Name: p.Name, Kind: p.Kind, Email: p.Email, Phone: p.Phone, Company: p.Company,
				GSTIN: p.GSTIN, PAN: p.PAN, State: p.StateCode, Address: p.Address})
		}
	case "items":
		keys, err := itemKeys(c)
		if err != nil {
			return nil, err
		}
		for i, p := range dataimport.ParseItems(tableOf(j, rows), dataimport.Mapping(j.Input.Mapping)) {
			vs[i] = itemVerdict(keys, p.Errors, itemFields{SKU: p.SKU, Name: p.Name, HSN: p.HSN, Unit: p.Unit, GSTRateBP: p.GSTRateBP,
				PriceMinor: p.SalePriceMinor, QtyMilli: p.OpeningQtyMilli, ValueMinor: p.OpeningValueMinor, Location: p.Location})
		}
	case "opening_docs":
		keys, err := contactKeys(c)
		if err != nil {
			return nil, err
		}
		idx := newContactIndex(keys)
		for i, p := range dataimport.ParseOpeningDocs(tableOf(j, rows), dataimport.Mapping(j.Input.Mapping), j.Input.Options["default_type"]) {
			vs[i] = docVerdict(idx, p)
		}
	case "tally":
		if err := classifyTally(c, rows, vs); err != nil {
			return nil, err
		}
	}
	b := &pgx.Batch{}
	counts := map[string]int{"ready": 0, "fix": 0, "existing": 0, "skipped": 0}
	for i, r := range rows {
		v := vs[i]
		fb, _ := json.Marshal(v.fields)
		if v.fields == nil {
			fb = []byte("{}")
		}
		b.Queue("update importers.rows set fields = $3, status = $4, reason = $5 where job_id = $1::uuid and row_no = $2", j.ID, r.No, fb, v.status, nilIfEmpty(v.reason))
		counts[v.status]++
	}
	br := c.Tx.SendBatch(c.Context, b)
	for range rows {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return nil, command.DB(err)
		}
	}
	return counts, command.DB(br.Close())
}

func contactVerdict(idx *contactIndex, errs []string, f contactFields) verdict {
	if len(errs) > 0 {
		return fix(errs)
	}
	if _, ok := idx.find(f.Name, f.GSTIN, f.Phone, f.Email); ok {
		return verdict{"existing", "Already in C Note", f}
	}
	idx.add(existingContact{ID: "in-file", Name: f.Name, GSTIN: f.GSTIN, Phone: digits(f.Phone), Email: strings.ToLower(f.Email)}) // a repeat in the same file
	return verdict{"ready", "", f}
}

type itemKeySet struct{ sku, name map[string]bool }

func itemKeys(c *command.Ctx) (*itemKeySet, error) {
	var out struct {
		Items []struct{ SKU, Name string } `json:"items"`
	}
	if err := callJSON(c, "stock_item.import_keys", map[string]any{}, &out); err != nil {
		return nil, err
	}
	k := &itemKeySet{map[string]bool{}, map[string]bool{}}
	for _, i := range out.Items {
		k.sku[strings.ToLower(i.SKU)], k.name[normName(i.Name)] = true, true
	}
	return k, nil
}

func itemVerdict(k *itemKeySet, errs []string, f itemFields) verdict {
	if len(errs) > 0 {
		return fix(errs)
	}
	if k.sku[strings.ToLower(f.SKU)] || (f.SKU == "" && k.name[normName(f.Name)]) || k.name[normName(f.Name)] {
		return verdict{"existing", "Already in C Note", f}
	}
	return verdict{"ready", "", f}
}

func docVerdict(idx *contactIndex, p dataimport.OpeningDocRow) verdict {
	if len(p.Errors) > 0 {
		return fix(p.Errors)
	}
	e, ok := idx.byParty(p.Party, p.GSTIN)
	f := docFields{DocType: p.DocType, Party: p.Party, Number: p.Number, Date: p.Date, DueDate: p.DueDate, AmountMinor: p.AmountMinor, ContactID: e.ID}
	switch {
	case !ok && p.DocType == "bill":
		return verdict{"fix", fmt.Sprintf("No vendor named %q in C Note: import contacts first", p.Party), f}
	case !ok:
		return verdict{"fix", fmt.Sprintf("No customer named %q in C Note: import contacts first", p.Party), f}
	case p.DocType == "bill" && !e.Vendor:
		return verdict{"fix", fmt.Sprintf("%q is not a vendor in C Note", p.Party), f}
	}
	return verdict{"ready", "", f}
}

func classifyTally(c *command.Ctx, rows []rowRec, vs []verdict) error {
	cks, err := contactKeys(c)
	if err != nil {
		return err
	}
	idx := newContactIndex(cks)
	ik, err := itemKeys(c)
	if err != nil {
		return err
	}
	names := map[string]int{}
	for _, r := range rows {
		if r.Entity == "item" {
			var it dataimport.TallyItem
			_ = json.Unmarshal(r.Raw, &it)
			names[normName(it.Name)]++
		}
	}
	for i, r := range rows {
		switch r.Entity {
		case "customer", "vendor":
			var l dataimport.TallyLedger
			_ = json.Unmarshal(r.Raw, &l)
			var errs []string
			gstin := ""
			if l.GSTIN != "" {
				g, err := dataimport.CheckGSTIN(l.GSTIN)
				if err != nil {
					errs = append(errs, err.Error())
				}
				gstin = g
			}
			pan := ""
			if l.PAN != "" {
				p, err := dataimport.CheckPAN(l.PAN)
				if err != nil {
					errs = append(errs, err.Error())
				}
				pan = p
			}
			if strings.TrimSpace(l.Name) == "" || len(l.Name) > 200 {
				errs = append(errs, "Name is missing or too long")
			}
			state := l.StateCode
			if gstin != "" {
				state = ""
			}
			vs[i] = contactVerdict(idx, errs, contactFields{Name: strings.TrimSpace(l.Name), Kind: r.Entity, Email: l.Email, Phone: l.Phone, GSTIN: gstin, PAN: pan, State: state, Address: l.Address})
		case "item":
			var it dataimport.TallyItem
			_ = json.Unmarshal(r.Raw, &it)
			f := itemFields{SKU: strings.TrimSpace(it.Name), Name: strings.TrimSpace(it.Name), HSN: it.HSN, Unit: it.BaseUnit, GSTRateBP: it.GSTRateBP, PriceMinor: max(it.RateMinor, 0), QtyMilli: it.QtyMilli, ValueMinor: it.ValueMinor}
			var errs []string
			if f.Name == "" || len(f.Name) > 40 {
				errs = append(errs, "Item name is empty or longer than 40 characters, so it cannot be used as the SKU")
			}
			if names[normName(it.Name)] > 1 {
				errs = append(errs, "Another item in this file has the same name, so it cannot be used as the SKU")
			}
			if !it.HasGSTRate {
				errs = append(errs, "GST rate is missing")
			}
			if it.HSN != "" {
				if err := dataimport.ValidateHSN(it.HSN); err != nil {
					errs = append(errs, err.Error())
				}
			}
			if len(f.Unit) > 12 {
				errs = append(errs, "Unit is longer than 12 characters")
			}
			if f.QtyMilli < 0 || f.ValueMinor < 0 {
				errs = append(errs, "Opening stock cannot be negative")
			}
			vs[i] = itemVerdict(ik, errs, f)
		case "ledger":
			var l dataimport.TallyLedger
			_ = json.Unmarshal(r.Raw, &l)
			f := ledgerFields{Name: strings.TrimSpace(l.Name), Class: l.Class, Dr: l.OpeningDr, Cr: l.OpeningCr}
			switch {
			case f.Name == "" || len(f.Name) > 120:
				vs[i] = verdict{"fix", "Ledger name is empty or too long", f}
			case accountKind(l.Class) == "":
				vs[i] = verdict{"fix", fmt.Sprintf("The group %q is not recognised: pick the account type in C Note later", l.PrimaryGroup), f}
			case l.OpeningDr == 0 && l.OpeningCr == 0:
				vs[i] = verdict{"skipped", "No opening balance", f}
			default:
				vs[i] = verdict{"ready", "", f}
			}
		}
	}
	return nil
}

func accountKind(class string) string {
	switch class {
	case dataimport.ClassBank, dataimport.ClassAsset:
		return "asset"
	case dataimport.ClassTax, dataimport.ClassLiability:
		return "liability"
	case dataimport.ClassEquity:
		return "equity"
	case dataimport.ClassRevenue:
		return "revenue"
	case dataimport.ClassExpense:
		return "expense"
	}
	return ""
}

// ---- template / preview ----

func DataTemplate(kind string) (any, error) {
	if kind == "tally" {
		return map[string]any{"fields": []dataimport.Field{}, "sample_csv": ""}, nil
	}
	if !validKind(kind) {
		return nil, command.ErrInvalid("kind must be contacts, items, opening_docs or tally")
	}
	return map[string]any{"fields": dataimport.Fields(dataimport.Kind(kind)), "sample_csv": dataimport.SampleCSV(dataimport.Kind(kind))}, nil
}

// DataPreview re-reads the job with the given mapping (header -> field key, "" = unmapped) and options, and returns counts and rows.
func DataPreview(c *command.Ctx, id string, mapping map[string]string, hasMapping bool, options map[string]string, status string) (any, error) {
	j, err := loadJob(c, id, true)
	if err != nil {
		return nil, err
	}
	if j.Status != "preview" {
		return nil, command.ErrConflict("this import has already started")
	}
	dirty := false
	if hasMapping && j.Kind != "tally" {
		m := dataimport.Mapping{}
		for header, field := range mapping {
			if field == "" {
				continue
			}
			i := slices.Index(j.Input.Headers, header)
			if i < 0 {
				return nil, command.ErrInvalid("unknown column " + header)
			}
			if known(dataimport.Kind(j.Kind), field) == false {
				return nil, command.ErrInvalid("unknown field " + field)
			}
			if _, dup := m[field]; dup {
				return nil, command.ErrInvalid("two columns are mapped to " + dataimport.Label(dataimport.Kind(j.Kind), field))
			}
			m[field] = i
		}
		j.Input.Mapping, dirty = m, true
	}
	for k, v := range options {
		if k != "default_kind" && k != "default_type" {
			continue
		}
		if j.Input.Options == nil {
			j.Input.Options = map[string]string{}
		}
		j.Input.Options[k], dirty = v, true
	}
	if dirty {
		if err := saveInput(c, j); err != nil {
			return nil, err
		}
	}
	counts, err := classify(c, j)
	if err != nil {
		return nil, err
	}
	where, args := "", []any{}
	if status != "" {
		where, args = "and status = $2", []any{status}
	}
	rows, err := loadRows(c, j.ID, where, 200, args...)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": j.ID, "kind": j.Kind, "counts": map[string]int{"ready": counts["ready"], "fix": counts["fix"], "existing": counts["existing"] + counts["skipped"]},
		"rows": rowViews(j, rows)}
	if j.Kind == "tally" {
		out["tally"] = j.Input.Tally
		by := map[string]int{}
		all, _ := loadRows(c, j.ID, "", 0)
		for _, r := range all {
			if r.Status == "ready" {
				by[r.Entity]++
			}
		}
		out["ready_by_type"] = by
		out["columns"] = []any{}
	} else {
		t := &dataimport.Table{Headers: j.Input.Headers}
		all, err := loadRows(c, j.ID, "", 0)
		if err != nil {
			return nil, err
		}
		t.Rows = tableOf(j, all).Rows
		out["columns"] = t.Columns(dataimport.Mapping(j.Input.Mapping))
		out["missing_required"] = dataimport.MissingRequired(dataimport.Kind(j.Kind), dataimport.Mapping(j.Input.Mapping))
	}
	return out, nil
}

func known(kind dataimport.Kind, key string) bool {
	for _, f := range dataimport.Fields(kind) {
		if f.Key == key {
			return true
		}
	}
	return false
}

func rowViews(j *dataJob, rows []rowRec) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		v := map[string]any{"row_no": r.No, "status": r.Status, "reason": r.Reason, "values": rowValues(j, r)}
		if j.Kind == "tally" {
			v["entity"] = r.Entity
		}
		out = append(out, v)
	}
	return out
}

func rowValues(j *dataJob, r rowRec) map[string]string {
	vals := map[string]string{}
	if j.Kind == "tally" {
		var m map[string]any
		_ = json.Unmarshal(r.Raw, &m)
		for _, k := range []string{"name", "class", "gstin", "hsn", "base_unit"} {
			if s, ok := m[k].(string); ok && s != "" {
				vals[k] = s
			}
		}
		vals["type"] = r.Entity
		return vals
	}
	var raw struct {
		Cells []string `json:"cells"`
	}
	_ = json.Unmarshal(r.Raw, &raw)
	for f, i := range j.Input.Mapping {
		if i >= 0 && i < len(raw.Cells) {
			vals[f] = raw.Cells[i]
		}
	}
	return vals
}

// ---- run ----

// DataRun starts a previewed job: the worker applies the rows that are ready (and selected, for Tally).
func DataRun(c *command.Ctx, id string, include map[string]bool) (any, error) {
	j, err := loadJob(c, id, true)
	if err != nil {
		return nil, err
	}
	if j.Status != "preview" { // a second run, or a run that is already going, is a no-op
		if j.Status == "queued" || j.Status == "running" || j.Status == "done" {
			return jobView(c, j)
		}
		return nil, command.ErrConflict("this import cannot be started: it is " + j.Status)
	}
	if j.Kind == "opening_docs" || j.Kind == "tally" {
		if st, err := openingStatus(c); err != nil {
			return nil, err
		} else if st == "posted" {
			return nil, command.ErrConflict("opening balances are already posted: reverse them before importing more opening documents or balances")
		}
	}
	if j.Kind == "tally" {
		if include == nil {
			include = map[string]bool{"customers": true, "vendors": true, "ledgers": true, "items": true}
		}
		j.Input.Include = include
		skip := []string{}
		for ent, key := range map[string]string{"customer": "customers", "vendor": "vendors", "ledger": "ledgers", "item": "items"} {
			if !include[key] {
				skip = append(skip, ent)
			}
		}
		if _, err := c.Tx.Exec(c.Context, "update importers.rows set status = 'skipped', reason = 'Not selected' where job_id = $1::uuid and status = 'ready' and entity = any($2::text[])", j.ID, skip); err != nil {
			return nil, command.DB(err)
		}
		if err := saveInput(c, j); err != nil {
			return nil, err
		}
	}
	var ready int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from importers.rows where job_id = $1::uuid and status = 'ready'", j.ID).Scan(&ready); err != nil {
		return nil, command.DB(err)
	}
	if ready == 0 {
		return nil, command.ErrInvalid("nothing to import: no row is ready")
	}
	// state CAS: only one run ever moves the job out of preview
	tag, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'queued' where id = $1::uuid and status = 'preview'", j.ID)
	if err != nil {
		return nil, command.DB(err)
	}
	if tag.RowsAffected() != 1 {
		return jobView(c, j)
	}
	if err := enqueueRun(c, j.ID, 0); err != nil {
		return nil, err
	}
	if _, err := c.Emit("data_import.started", nil, map[string]any{"import_id": j.ID, "kind": j.Kind, "rows": ready}); err != nil {
		return nil, err
	}
	j.Status = "queued"
	return jobView(c, j)
}

func enqueueRun(c *command.Ctx, id string, delaySeconds int) error {
	payload, _ := command.Marshal(map[string]string{"job_id": id})
	_, err := c.Tx.Exec(c.Context, "insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,'data_import.run',$2, now() + make_interval(secs => $3))",
		c.WorkspaceID(), json.RawMessage(payload), delaySeconds)
	return command.DB(err)
}

const chunk = 100

// RunDataJob is the worker handler of kind data_import.run: one chunk of rows, each in its own savepoint. It is safe to
// run twice or concurrently: the job row is locked, only rows still 'ready' are applied, and each row's target and
// status are written in the same savepoint as the record it created.
func RunDataJob(c *command.Ctx, payload json.RawMessage) error {
	var p struct {
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(payload, &p) != nil || p.JobID == "" {
		return command.ErrInvalid("job_id missing")
	}
	j, err := loadJob(c, p.JobID, true)
	if err != nil {
		return nil // deleted meanwhile
	}
	if j.Status != "queued" && j.Status != "running" {
		return nil
	}
	if _, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'running' where id = $1::uuid and status = 'queued'", j.ID); err != nil {
		return command.DB(err)
	}
	saved := c.Principal
	defer func() { c.Principal = saved }()
	// The rows are applied as the admin who asked for the import, through the same gates as if they typed each record.
	var role string
	if j.CreatedBy == nil {
		return failJob(c, j, "the person who started this import no longer exists")
	}
	if err := c.Tx.QueryRow(c.Context, "select role from core.memberships where user_id = $1::uuid", *j.CreatedBy).Scan(&role); err != nil || command.ParseRole(role) < command.Admin {
		return failJob(c, j, "the person who started this import is no longer an admin of the workspace")
	}
	c.Principal = command.Principal{Kind: command.KindUser, UserID: j.CreatedBy, WorkspaceID: c.WorkspaceID(), Role: command.ParseRole(role)}
	if j.Kind == "opening_docs" || j.Kind == "tally" {
		if st, err := openingStatus(c); err != nil {
			return err
		} else if st == "posted" {
			return failJob(c, j, "opening balances were posted while this import was waiting: reverse them and import again")
		}
	}
	rows, err := loadRows(c, j.ID, "and status = 'ready'", chunk)
	if err != nil {
		return err
	}
	throttled := false
	for _, r := range rows {
		if _, err := c.Tx.Exec(c.Context, "savepoint import_row"); err != nil {
			return command.DB(err)
		}
		target, tkind, fields, err := applyRow(c, j, r)
		if err != nil {
			if _, e := c.Tx.Exec(c.Context, "rollback to savepoint import_row"); e != nil {
				return command.DB(e)
			}
			if isRate(err) {
				throttled = true
				break
			}
			if _, e := c.Tx.Exec(c.Context, "update importers.rows set status = 'failed', reason = $3, applied_at = now() where job_id = $1::uuid and row_no = $2", j.ID, r.No, errText(err)); e != nil {
				return command.DB(e)
			}
		} else {
			if fields == nil {
				fields = r.Fields
			}
			if _, e := c.Tx.Exec(c.Context, "update importers.rows set status = 'added', target_id = $3::uuid, target_kind = $4, fields = $5, applied_at = now() where job_id = $1::uuid and row_no = $2",
				j.ID, r.No, nilIfEmpty(target), tkind, fields); e != nil {
				return command.DB(e)
			}
		}
		if _, err := c.Tx.Exec(c.Context, "release savepoint import_row"); err != nil {
			return command.DB(err)
		}
	}
	var left int
	if err := c.Tx.QueryRow(c.Context, "select count(*) from importers.rows where job_id = $1::uuid and status = 'ready'", j.ID).Scan(&left); err != nil {
		return command.DB(err)
	}
	if left > 0 {
		delay := 0
		if throttled {
			delay = 3
		}
		return enqueueRun(c, j.ID, delay)
	}
	var added, failed, skipped int
	if err := c.Tx.QueryRow(c.Context,
		`select count(*) filter (where status = 'added'), count(*) filter (where status = 'failed'), count(*) filter (where status not in ('added','failed'))
		   from importers.rows where job_id = $1::uuid`, j.ID).Scan(&added, &failed, &skipped); err != nil {
		return command.DB(err)
	}
	rep, _ := json.Marshal(map[string]any{"added": added, "failed": failed, "skipped": skipped})
	if _, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'done', finished_at = now(), report = $2 where id = $1::uuid", j.ID, rep); err != nil {
		return command.DB(err)
	}
	_, err = c.Emit("data_import.completed", nil, map[string]any{"import_id": j.ID, "kind": j.Kind, "added": added, "failed": failed, "skipped": skipped})
	return err
}

func failJob(c *command.Ctx, j *dataJob, why string) error {
	rep, _ := json.Marshal(map[string]any{"error": why})
	_, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'failed', finished_at = now(), report = $2 where id = $1::uuid", j.ID, rep)
	return command.DB(err)
}

// applyRow creates the record of one row through the owning module's commands. It returns the new record's id.
func applyRow(c *command.Ctx, j *dataJob, r rowRec) (target, kind string, fields json.RawMessage, err error) {
	var idOut struct {
		ID string `json:"id"`
	}
	switch {
	case j.Kind == "contacts" || r.Entity == "customer" || r.Entity == "vendor":
		var f contactFields
		_ = json.Unmarshal(r.Fields, &f)
		vendor := f.Kind == "vendor" || f.Kind == "both"
		ck := "person"
		if f.Company != "" || f.GSTIN != "" {
			ck = "company"
		}
		in := map[string]any{"space_id": j.Input.SpaceID, "name": f.Name, "kind": ck, "is_vendor": vendor, "email": nilIfEmpty(f.Email), "phone": nilIfEmpty(f.Phone),
			"company": nilIfEmpty(f.Company), "gstin": nilIfEmpty(f.GSTIN), "pan": nilIfEmpty(f.PAN), "state_code": nilIfEmpty(f.State), "billing_address": nilIfEmpty(f.Address)}
		if err := callJSON(c, "contact.create", in, &idOut); err != nil {
			return "", "", nil, err
		}
		return idOut.ID, "contact", nil, nil
	case j.Kind == "items" || r.Entity == "item":
		var f itemFields
		_ = json.Unmarshal(r.Fields, &f)
		in := map[string]any{"sku": f.SKU, "name": f.Name, "gst_rate_bp": f.GSTRateBP, "price_minor": f.PriceMinor}
		if f.HSN != "" {
			in["hsn_sac"] = f.HSN
		}
		if f.Unit != "" {
			in["unit"] = f.Unit
		}
		if err := callJSON(c, "stock_item.create", in, &idOut); err != nil {
			return "", "", nil, err
		}
		if f.QtyMilli > 0 {
			if err := callJSON(c, "stock.import_opening", map[string]any{"job_id": j.ID, "item_id": idOut.ID, "qty_milli": f.QtyMilli, "value_minor": f.ValueMinor, "location": f.Location}, nil); err != nil {
				return "", "", nil, err
			}
		}
		return idOut.ID, "item", nil, nil
	case j.Kind == "opening_docs":
		var f docFields
		_ = json.Unmarshal(r.Fields, &f)
		if f.DocType == "bill" {
			in := map[string]any{"space_id": j.Input.SpaceID, "vendor_id": f.ContactID, "vendor_bill_number": f.Number, "bill_date": f.Date, "due_date": nilIfEmpty(f.DueDate), "amount_minor": f.AmountMinor}
			if err := callJSON(c, "bill.import_opening", in, &idOut); err != nil {
				return "", "", nil, err
			}
			return idOut.ID, "purchase_bill", nil, nil
		}
		in := map[string]any{"space_id": j.Input.SpaceID, "contact_id": f.ContactID, "number": f.Number, "invoice_date": f.Date, "due_date": nilIfEmpty(f.DueDate), "amount_minor": f.AmountMinor}
		if err := callJSON(c, "invoice.import_opening", in, &idOut); err != nil {
			return "", "", nil, err
		}
		return idOut.ID, "invoice", nil, nil
	case r.Entity == "ledger":
		var f ledgerFields
		_ = json.Unmarshal(r.Fields, &f)
		var acct struct {
			Code string `json:"code"`
		}
		if err := callJSON(c, "ledger.account_ensure", map[string]any{"name": f.Name, "kind": accountKind(f.Class)}, &acct); err != nil {
			return "", "", nil, err
		}
		if acct.Code == "1100" || acct.Code == "2100" || acct.Code == "3900" {
			return "", "", nil, command.ErrInvalid("account " + acct.Code + " is filled in from opening invoices and bills: leave it out")
		}
		if err := draftAddLine(c, acct.Code, f.Dr, f.Cr); err != nil {
			return "", "", nil, err
		}
		f.Code = acct.Code
		fb, _ := json.Marshal(f)
		return "", "opening_line", fb, nil
	}
	return "", "", nil, command.ErrInvalid("unknown row")
}

// ---- status / list / errors ----

func jobView(c *command.Ctx, j *dataJob) (map[string]any, error) {
	var total, ready, added, failed int
	if err := c.Tx.QueryRow(c.Context,
		`select count(*), count(*) filter (where status = 'ready'), count(*) filter (where status = 'added'), count(*) filter (where status = 'failed')
		   from importers.rows where job_id = $1::uuid`, j.ID).Scan(&total, &ready, &added, &failed); err != nil {
		return nil, command.DB(err)
	}
	processed := total
	if j.Status == "preview" {
		processed = 0
	} else {
		processed = total - ready
	}
	var errMsg any
	if s, ok := j.Report["error"].(string); ok {
		errMsg = s
	}
	var until any
	if j.Status == "done" && j.FinishedAt != nil {
		until = j.FinishedAt.UTC().AddDate(0, 0, undoDays).Format(time.RFC3339)
	}
	return map[string]any{"id": j.ID, "kind": j.Kind, "status": j.Status, "filename": j.Filename, "total": total, "processed": processed,
		"added": added, "skipped": total - added - failed - ready, "failed": failed, "error": errMsg, "undo_until": until,
		"created_at": j.CreatedAt.UTC().Format(time.RFC3339)}, nil
}

func DataStatus(c *command.Ctx, id string) (any, error) {
	j, err := loadJob(c, id, false)
	if err != nil {
		return nil, err
	}
	return jobView(c, j)
}

func DataList(c *command.Ctx) (any, error) {
	rows, err := c.Tx.Query(c.Context, "select id::text from importers.jobs where kind <> 'notes' order by created_at desc limit 50")
	if err != nil {
		return nil, command.DB(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, command.DB(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	out := []map[string]any{}
	for _, id := range ids {
		j, err := loadJob(c, id, false)
		if err != nil {
			return nil, err
		}
		v, err := jobView(c, j)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// DataErrors is the "rows that need fixing" download: the original cells plus the reason, formula-safe.
func DataErrors(c *command.Ctx, id string) (any, error) {
	j, err := loadJob(c, id, false)
	if err != nil {
		return nil, err
	}
	rows, err := loadRows(c, j.ID, "and status in ('fix','failed')", 0)
	if err != nil {
		return nil, err
	}
	var out [][]string
	if j.Kind == "tally" {
		out = append(out, []string{"Row", "Type", "Name", "Reason"})
		for _, r := range rows {
			v := rowValues(j, r)
			out = append(out, []string{fmt.Sprint(r.No), v["type"], v["name"], deref(r.Reason)})
		}
	} else {
		out = append(out, append(slices.Clone(j.Input.Headers), "Why this row needs fixing"))
		for _, r := range rows {
			var raw struct {
				Cells []string `json:"cells"`
			}
			_ = json.Unmarshal(r.Raw, &raw)
			cells := slices.Clone(raw.Cells)
			for len(cells) < len(j.Input.Headers) {
				cells = append(cells, "")
			}
			out = append(out, append(cells, deref(r.Reason)))
		}
	}
	name := strings.TrimSuffix(deref(j.Filename), ".csv")
	if name == "" {
		name = j.Kind
	}
	return map[string]any{"csv": dataimport.SafeCSV(out), "filename": name + "-rows-to-fix.csv", "rows": len(rows)}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---- undo ----

// DataUndo reverses a finished import in one step or not at all: every dependency is checked, and any blocker refuses the
// whole undo with a reason per row (nothing is changed).
func DataUndo(c *command.Ctx, id string) (any, error) {
	j, err := loadJob(c, id, true)
	if err != nil {
		return nil, err
	}
	if j.Status == "undone" {
		return jobView(c, j)
	}
	if j.Status != "done" || j.FinishedAt == nil {
		return nil, command.ErrConflict("only a finished import can be undone")
	}
	if time.Now().UTC().After(j.FinishedAt.UTC().AddDate(0, 0, undoDays)) {
		return nil, command.ErrConflict(fmt.Sprintf("the %d day window to undo this import has passed", undoDays))
	}
	rows, err := loadRows(c, j.ID, "and status = 'added'", 0)
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context, "savepoint import_undo"); err != nil {
		return nil, command.DB(err)
	}
	var reasons []string
	why := func(r rowRec, s string) { reasons = append(reasons, fmt.Sprintf("row %d: %s", r.No, s)) }
	byTarget := map[string]rowRec{}
	var contactIDs, itemIDs []string
	hasLedger := false
	targets, err := targetRows(c, j.ID)
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		byTarget[t.target] = t.row
		switch t.kind {
		case "contact":
			contactIDs = append(contactIDs, t.target)
		case "item":
			itemIDs = append(itemIDs, t.target)
		case "opening_line":
			hasLedger = true
		}
	}
	if st, err := openingStatus(c); err != nil {
		return nil, err
	} else if st == "posted" && (j.Kind == "opening_docs" || hasLedger) {
		reasons = append(reasons, "opening balances are posted: reverse the opening balances first")
	}
	if len(contactIDs) > 0 {
		var used struct {
			Used []string `json:"used"`
		}
		if err := callJSON(c, "contact.import_usage", map[string]any{"ids": contactIDs}, &used); err != nil {
			return nil, err
		}
		for _, u := range used.Used {
			why(byTarget[u], "this contact is used on a document, deal or ticket")
		}
	}
	if len(itemIDs) > 0 {
		var chk struct {
			Blocked []struct {
				ItemID string `json:"item_id"`
				SKU    string `json:"sku"`
			} `json:"blocked"`
		}
		if err := callJSON(c, "stock.import_check", map[string]any{"job_id": j.ID}, &chk); err != nil {
			return nil, err
		}
		for _, b := range chk.Blocked {
			why(byTarget[b.ItemID], "stock of "+b.SKU+" has been used since it was imported")
		}
	}
	if len(reasons) == 0 {
		reasons = applyUndo(c, j, targets, why)
	}
	if len(reasons) > 0 {
		if _, err := c.Tx.Exec(c.Context, "rollback to savepoint import_undo"); err != nil {
			return nil, command.DB(err)
		}
		if len(reasons) > 8 {
			reasons = append(reasons[:8], fmt.Sprintf("and %d more", len(reasons)-8))
		}
		return nil, command.ErrConflict("This import cannot be undone: " + strings.Join(reasons, "; "))
	}
	if _, err := c.Tx.Exec(c.Context, "release savepoint import_undo"); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context, "update importers.jobs set status = 'undone', undone_at = now() where id = $1::uuid", j.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("data_import.undone", nil, map[string]any{"import_id": j.ID, "kind": j.Kind, "rows": len(rows)}); err != nil {
		return nil, err
	}
	j.Status = "undone"
	return jobView(c, j)
}

type targetRow struct {
	target, kind string
	row          rowRec
}

func targetRows(c *command.Ctx, id string) ([]targetRow, error) {
	rows, err := c.Tx.Query(c.Context, "select row_no, entity, coalesce(target_id::text, ''), coalesce(target_kind, ''), fields from importers.rows where job_id = $1::uuid and status = 'added' order by row_no", id)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	var out []targetRow
	for rows.Next() {
		var t targetRow
		if err := rows.Scan(&t.row.No, &t.row.Entity, &t.target, &t.kind, &t.row.Fields); err != nil {
			return nil, command.DB(err)
		}
		out = append(out, t)
	}
	return out, command.DB(rows.Err())
}

func applyUndo(c *command.Ctx, j *dataJob, targets []targetRow, why func(rowRec, string)) []string {
	var reasons []string
	fail := func(t targetRow, err error) {
		reasons = append(reasons, fmt.Sprintf("row %d: %s", t.row.No, errText(err)))
	}
	stockDone := false
	for _, t := range targets {
		var err error
		switch t.kind {
		case "invoice":
			err = callJSON(c, "invoice.import_opening_cancel", map[string]any{"id": t.target}, nil)
		case "purchase_bill":
			err = callJSON(c, "bill.import_opening_cancel", map[string]any{"id": t.target}, nil)
		case "contact":
			err = callJSON(c, "note.delete", map[string]any{"id": t.target}, nil)
		case "item":
			if !stockDone {
				stockDone = true
				if e := callJSON(c, "stock.import_reverse", map[string]any{"job_id": j.ID}, nil); e != nil {
					fail(t, e)
					return reasons // the transaction is unusable after a database error
				}
			}
			err = callJSON(c, "stock_item.update", map[string]any{"id": t.target, "active": false}, nil)
		case "opening_line":
			var f ledgerFields
			_ = json.Unmarshal(t.row.Fields, &f)
			err = draftRemoveLine(c, f.Code)
		}
		if err != nil {
			fail(t, err)
			if _, isCmd := err.(*command.Error); !isCmd || strings.Contains(err.Error(), "database error") {
				return reasons
			}
		}
	}
	return reasons
}
