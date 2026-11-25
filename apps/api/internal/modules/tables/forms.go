package tables

// Phase 1b public forms (design 18.2, revised by 18.8: forms take no file uploads). The token is 128 random bits, only
// its sha256 is stored. Everything after the resolver runs under RLS as a system principal acting for the form's
// creator, with the form row locked FOR NO KEY UPDATE so rotate and disable (FOR UPDATE) cannot interleave with a submission.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cnote/api/internal/command"
)

const (
	maxFormsPerTable = 5
	formTokenBytes   = 16 // 128 bits, base64url (22 characters); the stored value is the hex sha256
	maxFormFields    = 50
)

func init() {
	handlers["table_form.list"] = formList
	handlers["table_form.get"] = formGet
	handlers["table_form.create"] = formCreate
	handlers["table_form.update"] = formUpdate
	handlers["table_form.rotate"] = formRotate
	handlers["table_form.disable"] = formDisable
}

// FormConfig is tables.form_links.config. A "uploads" key sent by an older client is ignored and never stored.
type FormConfig struct {
	Title    string      `json:"title,omitempty"`     // default: the form name
	Intro    string      `json:"intro,omitempty"`     // plain text, at most 2,000 characters
	ThankYou string      `json:"thank_you,omitempty"` // plain text, at most 2,000 characters
	Fields   []FormField `json:"fields"`              // order shown; 1 to 50
}
type FormField struct {
	FieldID  string `json:"field_id"`
	Label    string `json:"label,omitempty"` // at most 100 characters; default the field's name
	Help     string `json:"help,omitempty"`  // at most 300 characters
	Required bool   `json:"required,omitempty"`
}

// ResolvedForm is what tables.resolve_form returns.
type ResolvedForm struct {
	ID, WorkspaceID, BaseID, TableID, CreatedBy, Plan string
	Config                                            FormConfig
}

// ErrNoForm is the single "not found" the public handler turns into the one 404 body.
var ErrNoForm = command.NewError(command.NotFound, "form")

// Allowed field types on a form: no link, user, attachment or computed field.
var formAllowed = map[FieldType]bool{Text: true, LongText: true, Number: true, Currency: true, Percent: true, Date: true,
	Checkbox: true, SingleSelect: true, MultiSelect: true, Phone: true, Email: true, URL: true}

// formDayCap is the submissions a form takes per UTC day, by plan. Placeholders until pricing is set.
var formDayCap = map[string]int{"free": 50, "team": 500, "business": 2000, "enterprise": 5000}

func FormDailyCap(plan string) int {
	if n, ok := formDayCap[plan]; ok {
		return n
	}
	return formDayCap["free"]
}

var formTokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

// ValidFormToken is the shape of a token (not whether it exists).
func ValidFormToken(s string) bool { return formTokenRe.MatchString(s) }

// HashFormToken is hex(sha256(token)), the only form of the token that is stored or looked up.
func HashFormToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func newFormToken() (string, error) {
	b := make([]byte, formTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", command.NewError(command.Internal, "could not make a link")
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ResolveForm looks a token hash up through tables.resolve_form (no tenant is known yet). Unknown, disabled and
// trashed all return ErrNoForm.
func ResolveForm(ctx context.Context, pool *pgxpool.Pool, tokenHash string) (ResolvedForm, error) {
	var f ResolvedForm
	var cfg []byte
	err := pool.QueryRow(ctx, `select id::text, workspace_id::text, base_id::text, table_id::text, config, created_by::text, plan from tables.resolve_form($1)`, tokenHash).
		Scan(&f.ID, &f.WorkspaceID, &f.BaseID, &f.TableID, &cfg, &f.CreatedBy, &f.Plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNoForm
	}
	if err != nil {
		return f, command.DB(err)
	}
	if json.Unmarshal(cfg, &f.Config) != nil {
		return f, ErrNoForm
	}
	return f, nil
}

// ---- the public path ----

type formRow struct {
	Name   string
	Config FormConfig
}

// withForm runs fn in one tenant transaction, as a system principal for the form's creator capped at Member. First it
// locks the form row FOR NO KEY UPDATE (so submits serialise and can count in-transaction) and re-checks the presented token hash, enabled, and the current config; then the live
// table and base and the creator's access. Any failure of those is ErrNoForm (the one 404).
func withForm(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, tokenHash string, f ResolvedForm, fn func(*command.Ctx, *openedTable, formRow) error) error {
	creator := f.CreatorPtr()
	p := command.Principal{Kind: command.KindSystem, UserID: creator, WorkspaceID: f.WorkspaceID, Role: command.Member}
	return reg.WithTx(ctx, pool, p, func(c *command.Ctx) error {
		var fr formRow
		var cfg []byte
		var tableID, createdBy string
		err := c.Tx.QueryRow(ctx, `select name, config, table_id::text, created_by::text from tables.form_links
			where workspace_id=$1::uuid and id=$2::uuid and token_hash=$3 and enabled for no key update`, f.WorkspaceID, f.ID, tokenHash).Scan(&fr.Name, &cfg, &tableID, &createdBy)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoForm
		}
		if err != nil {
			return command.DB(err)
		}
		if json.Unmarshal(cfg, &fr.Config) != nil {
			return ErrNoForm
		}
		c.Principal.UserID = &createdBy // the creator now on record, not the one the resolver saw
		var role string
		err = c.Tx.QueryRow(ctx, `select role from core.memberships where workspace_id=$1::uuid and user_id=$2::uuid`, f.WorkspaceID, createdBy).Scan(&role)
		if errors.Is(err, pgx.ErrNoRows) || role == "guest" {
			return ErrNoForm
		}
		if err != nil {
			return command.DB(err)
		}
		tc, err := openTable(c, tableID, RoleWrite)
		var ce *command.Error
		if errors.As(err, &ce) && (ce.Code == command.NotFound || ce.Code == command.Forbidden) {
			return ErrNoForm
		}
		if err != nil {
			return err
		}
		if tc.Base.ID != f.BaseID {
			return ErrNoForm
		}
		return fn(c, tc, fr)
	})
}

// CreatorPtr is the creator's id as a pointer.
func (f ResolvedForm) CreatorPtr() *string { s := f.CreatedBy; return &s }

// liveFormFields is the config's fields that still exist, are not trashed and still have an allowed type, in order.
func liveFormFields(tc *openedTable, cfg FormConfig) []FormField {
	out := []FormField{}
	for _, ff := range cfg.Fields {
		if f, ok := tc.byID[ff.FieldID]; ok && formAllowed[f.Type] {
			out = append(out, ff)
		}
	}
	return out
}

func formLabel(tc *openedTable, ff FormField) string {
	if ff.Label != "" {
		return ff.Label
	}
	return tc.byID[ff.FieldID].Name
}

type pubOpt struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type pubField struct {
	ID       string   `json:"field_id"`
	Label    string   `json:"label"`
	Help     string   `json:"help"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Options  []pubOpt `json:"options,omitempty"`
	Decimals *int     `json:"decimals,omitempty"`
}

// PublicForm is the GET body: only what the page needs (no field ids of fields not on the form, no records, no names
// of tables, bases or the workspace).
func PublicForm(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, tokenHash string, f ResolvedForm) (json.RawMessage, error) {
	var out json.RawMessage
	err := withForm(ctx, pool, reg, tokenHash, f, func(c *command.Ctx, tc *openedTable, fr formRow) error {
		fields := []pubField{}
		for _, ff := range liveFormFields(tc, fr.Config) {
			fl := tc.byID[ff.FieldID]
			pf := pubField{ID: fl.ID, Label: formLabel(tc, ff), Help: ff.Help, Type: string(fl.Type), Required: ff.Required}
			switch fl.Type {
			case SingleSelect, MultiSelect:
				for _, o := range cfgOf[SelectConfig](fl).Options {
					pf.Options = append(pf.Options, pubOpt{o.ID, o.Name})
				}
			case Number, Percent, Currency:
				d := cfgOf[NumberConfig](fl).Decimals
				pf.Decimals = &d
			}
			fields = append(fields, pf)
		}
		title := fr.Config.Title
		if title == "" {
			title = fr.Name
		}
		b, err := command.Marshal(map[string]any{"title": title, "intro": fr.Config.Intro, "thank_you": fr.Config.ThankYou, "fields": fields})
		out = b
		return err
	})
	return out, err
}

// formValues validates a submission against the current config and live fields. Unknown ids, a missing required
// field and a bad value are refused with a message that names the field's label. Locked fields are accepted: a manager
// put them on the form.
func formValues(tc *openedTable, cfg FormConfig, vals map[string]any) (map[string]any, error) {
	shown := liveFormFields(tc, cfg)
	on := map[string]bool{}
	for _, ff := range shown {
		on[ff.FieldID] = true
	}
	for k := range vals {
		if !on[k] {
			return nil, command.ErrInvalid("this form has changed: please reload it and try again")
		}
	}
	set := map[string]any{}
	for _, ff := range shown {
		f, label := tc.byID[ff.FieldID], formLabel(tc, ff)
		var v any
		var err error
		switch x := vals[ff.FieldID].(type) {
		case nil:
		case string:
			v, err = Coerce(f, x)
		default:
			v, err = Validate(f, x)
		}
		if err != nil {
			var ce *command.Error
			if errors.As(err, &ce) {
				return nil, command.ErrInvalid(label + ": " + ce.Message)
			}
			return nil, err
		}
		if v == nil {
			if ff.Required {
				return nil, command.ErrInvalid(label + " is required")
			}
			continue
		}
		set[f.ID] = v
	}
	if len(set) == 0 {
		return nil, command.ErrInvalid("Fill in at least one field")
	}
	return set, nil
}

// SubmitForm creates one record from a public submission as the "form" actor (created_by null, event via "form") in
// one transaction. payload is the JSON {values: {field id: string|bool|array}, hp?: string}. A honeypot hit stores
// nothing and reports success.
func SubmitForm(ctx context.Context, pool *pgxpool.Pool, reg *command.Registry, tokenHash string, f ResolvedForm, payload json.RawMessage) error {
	var in struct {
		Values map[string]any `json:"values"`
		Hp     string         `json:"hp"`
	}
	if json.Unmarshal(payload, &in) != nil {
		return command.ErrInvalid("invalid request")
	}
	return withForm(ctx, pool, reg, tokenHash, f, func(c *command.Ctx, tc *openedTable, fr formRow) error {
		if in.Hp != "" {
			return nil // honeypot: same lock and recheck as a real post, then store nothing
		}
		set, err := formValues(tc, fr.Config, in.Values)
		if err != nil {
			return err
		}
		creator := c.Principal.UserID
		c.Principal.UserID = nil // created_by and updated_by stay null: the grid shows "Form"
		ids, err := insertRecords(c, tc, []newRec{{set: set}})
		c.Principal.UserID = creator
		var ce *command.Error
		if errors.As(err, &ce) && ce.Code == command.Conflict {
			return command.ErrConflict("This form is not accepting responses right now.") // no plan numbers
		}
		if err != nil {
			return err
		}
		if _, err = emitRecordEvents(c, tc, "created", []evRec{{ID: ids[0], Fields: sortedKeys(set)}}, map[string]any{"via": "form", "form_id": f.ID}); err != nil {
			return err
		}
		// The form row is held FOR NO KEY UPDATE, so this increment is atomic with the submission.
		_, err = c.Tx.Exec(ctx, `update tables.form_links set submissions = submissions + 1, last_submission_at = now() where workspace_id=$1::uuid and id=$2::uuid`, f.WorkspaceID, f.ID)
		return err
	})
}

// ---- commands ----

type formOut struct {
	ID               string     `json:"id"`
	TableID          string     `json:"table_id"`
	Name             string     `json:"name"`
	Enabled          bool       `json:"enabled"`
	Config           FormConfig `json:"config"`
	Submissions      int        `json:"submissions"`
	LastSubmissionAt *time.Time `json:"last_submission_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

const formCols = `id::text, table_id::text, name, enabled, config, submissions, last_submission_at, created_at, updated_at`

func scanForm(row pgx.Row) (formOut, error) {
	var o formOut
	var cfg []byte
	if err := row.Scan(&o.ID, &o.TableID, &o.Name, &o.Enabled, &cfg, &o.Submissions, &o.LastSubmissionAt, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return o, command.DB(err)
	}
	_ = json.Unmarshal(cfg, &o.Config)
	if o.Config.Fields == nil {
		o.Config.Fields = []FormField{}
	}
	return o, nil
}

// loadForm reads a form; lock is "" or "for update" (rotate, disable and update serialise against submissions).
func loadForm(c *command.Ctx, id, lock string) (formOut, error) {
	if err := needID("id", id); err != nil {
		return formOut{}, err
	}
	o, err := scanForm(c.Tx.QueryRow(c.Context, `select `+formCols+` from tables.form_links where workspace_id=$1::uuid and id=$2::uuid `+lock, c.WorkspaceID(), id))
	var ce *command.Error
	if errors.As(err, &ce) && ce.Code == command.NotFound {
		return o, command.ErrNotFound("form")
	}
	return o, err
}

func formList(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		TableID string `json:"table_id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("table_id", in.TableID); err != nil {
		return nil, err
	}
	if _, err := openTable(c, in.TableID, RoleWrite); err != nil {
		return nil, err
	}
	rows, err := c.Tx.Query(c.Context, `select `+formCols+` from tables.form_links where workspace_id=$1::uuid and table_id=$2::uuid order by created_at, id`, c.WorkspaceID(), in.TableID)
	if err != nil {
		return nil, command.DB(err)
	}
	defer rows.Close()
	out := []formOut{}
	for rows.Next() {
		o, err := scanForm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, command.DB(rows.Err())
}

func formGet(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	o, err := loadForm(c, in.ID, "")
	if err != nil {
		return nil, err
	}
	if _, err := openTable(c, o.TableID, RoleWrite); err != nil {
		return nil, command.ErrNotFound("form")
	}
	return o, nil
}

func formText(what, s string, max int) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", command.ErrInvalid(what + " can be at most " + itoa(max) + " characters")
	}
	return s, nil
}

// cleanFormConfig checks a config against the table's live fields and returns the normalised one.
func cleanFormConfig(tc *openedTable, in FormConfig) (FormConfig, error) {
	var out FormConfig
	var err error
	for _, p := range []struct {
		what string
		src  string
		dst  *string
		max  int
	}{{"title", in.Title, &out.Title, 120}, {"intro", in.Intro, &out.Intro, 2000}, {"thank-you text", in.ThankYou, &out.ThankYou, 2000}} {
		if *p.dst, err = formText(p.what, p.src, p.max); err != nil {
			return out, err
		}
	}
	if n := len(in.Fields); n < 1 || n > maxFormFields {
		return out, command.ErrInvalid("a form shows 1 to 50 fields")
	}
	seen := map[string]bool{}
	out.Fields = make([]FormField, 0, len(in.Fields))
	for _, ff := range in.Fields {
		f, ok := tc.byID[ff.FieldID]
		switch {
		case !ok:
			return out, command.ErrInvalid("unknown field " + ff.FieldID)
		case seen[f.ID]:
			return out, command.ErrInvalid("field " + f.Name + " is listed twice")
		case !formAllowed[f.Type]:
			return out, command.ErrInvalid("a form cannot show the " + string(f.Type) + " field " + f.Name)
		}
		seen[f.ID] = true
		nf := FormField{FieldID: f.ID, Required: ff.Required}
		if nf.Label, err = formText("a label", ff.Label, 100); err != nil {
			return out, err
		}
		if nf.Help, err = formText("help text", ff.Help, 300); err != nil {
			return out, err
		}
		out.Fields = append(out.Fields, nf)
	}
	if b, _ := json.Marshal(out); len(b) > 16384 {
		return out, command.ErrInvalid("the form settings are too large (16 KB at most)")
	}
	return out, nil
}

func formCreate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		TableID string     `json:"table_id"`
		Name    string     `json:"name"`
		Config  FormConfig `json:"config"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if err := needID("table_id", in.TableID); err != nil {
		return nil, err
	}
	name, err := cleanName("form", in.Name, 100)
	if err != nil {
		return nil, err
	}
	tc, err := openTable(c, in.TableID, RoleManage)
	if err != nil {
		return nil, err
	}
	cfg, err := cleanFormConfig(tc, in.Config)
	if err != nil {
		return nil, err
	}
	// Lock the table row before counting, so two creators cannot both see 4.
	if _, err := c.Tx.Exec(c.Context, `select 1 from tables.tables where workspace_id=$1::uuid and id=$2::uuid for update`, c.WorkspaceID(), tc.Table.ID); err != nil {
		return nil, command.DB(err)
	}
	var n int
	if err := c.Tx.QueryRow(c.Context, `select count(*) from tables.form_links where workspace_id=$1::uuid and table_id=$2::uuid`, c.WorkspaceID(), tc.Table.ID).Scan(&n); err != nil {
		return nil, command.DB(err)
	}
	if n >= maxFormsPerTable {
		return nil, command.ErrConflict("A table can have 5 forms")
	}
	token, err := newFormToken()
	if err != nil {
		return nil, err
	}
	cb, _ := json.Marshal(cfg)
	o, err := scanForm(c.Tx.QueryRow(c.Context, `insert into tables.form_links (workspace_id, base_id, table_id, name, token_hash, config, created_by)
		values ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::jsonb,$7::uuid) returning `+formCols,
		c.WorkspaceID(), tc.Base.ID, tc.Table.ID, name, HashFormToken(token), string(cb), c.Principal.UserID))
	if err != nil {
		return nil, err
	}
	return map[string]any{"form": o, "url": "/tf/" + token}, nil
}

// openFormForChange locks the form FOR UPDATE and checks the caller manages its table.
func openFormForChange(c *command.Ctx, id string) (formOut, *openedTable, error) {
	o, err := loadForm(c, id, "for update")
	if err != nil {
		return o, nil, err
	}
	tc, err := openTable(c, o.TableID, RoleManage)
	return o, tc, err
}

func formUpdate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID     string      `json:"id"`
		Name   *string     `json:"name"`
		Config *FormConfig `json:"config"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	o, tc, err := openFormForChange(c, in.ID)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		if o.Name, err = cleanName("form", *in.Name, 100); err != nil {
			return nil, err
		}
	}
	if in.Config != nil {
		if o.Config, err = cleanFormConfig(tc, *in.Config); err != nil {
			return nil, err
		}
	}
	cb, _ := json.Marshal(o.Config)
	return scanForm(c.Tx.QueryRow(c.Context, `update tables.form_links set name=$3, config=$4::jsonb, updated_at=now() where workspace_id=$1::uuid and id=$2::uuid returning `+formCols,
		c.WorkspaceID(), o.ID, o.Name, string(cb)))
}

func formRotate(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	o, _, err := openFormForChange(c, in.ID)
	if err != nil {
		return nil, err
	}
	token, err := newFormToken()
	if err != nil {
		return nil, err
	}
	o, err = scanForm(c.Tx.QueryRow(c.Context, `update tables.form_links set token_hash=$3, enabled=true, updated_at=now() where workspace_id=$1::uuid and id=$2::uuid returning `+formCols,
		c.WorkspaceID(), o.ID, HashFormToken(token)))
	if err != nil {
		return nil, err
	}
	return map[string]any{"form": o, "url": "/tf/" + token}, nil
}

func formDisable(c *command.Ctx, raw json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	o, _, err := openFormForChange(c, in.ID)
	if err != nil {
		return nil, err
	}
	return scanForm(c.Tx.QueryRow(c.Context, `update tables.form_links set enabled=false, updated_at=now() where workspace_id=$1::uuid and id=$2::uuid returning `+formCols,
		c.WorkspaceID(), o.ID))
}
