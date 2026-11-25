package tables

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
)

// Public forms against the real database (design 18.2 / 18.8).

type formFx struct {
	*tfix
	tid string
	f   map[string]string
}

func newFormFx(t *testing.T) *formFx {
	x := newTFix(t)
	tid, f := x.mkTable(x.openBase, "Leads", fspec{"Name", "text", ""}, fspec{"Email", "email", ""}, fspec{"Qty", "number", ""},
		fspec{"Pic", "attachment", ""}, fspec{"Owner", "user", `{}`}, fspec{"Note", "long_text", ""})
	return &formFx{tfix: x, tid: tid, f: f}
}

func (x *formFx) cfg(req ...string) map[string]any {
	fs := []any{map[string]any{"field_id": x.f["Name"], "label": "Your name", "required": true}, map[string]any{"field_id": x.f["Email"]}}
	for _, r := range req {
		fs = append(fs, map[string]any{"field_id": x.f[r]})
	}
	return map[string]any{"title": "Contact", "fields": fs, "uploads": true}
}

// create makes a form as the owner and returns the form id and token.
func (x *formFx) create(cfg map[string]any) (id, token string) {
	x.t.Helper()
	err := x.commit(func(c *command.Ctx) error {
		r, err := rcall(formCreate, c, map[string]any{"table_id": x.tid, "name": "F", "config": cfg})
		if err != nil {
			return err
		}
		id = r["form"].(map[string]any)["id"].(string)
		token = strings.TrimPrefix(r["url"].(string), "/tf/")
		return nil
	})
	if err != nil {
		x.t.Fatal(err)
	}
	return
}

func (x *formFx) submit(token string, payload any) error {
	b, _ := json.Marshal(payload)
	h := HashFormToken(token)
	f, err := ResolveForm(context.Background(), x.app, h)
	if err != nil {
		return err
	}
	return SubmitForm(context.Background(), x.app, command.NewRegistry(), h, f, b)
}

func (x *formFx) count() (n int) {
	x.admin.QueryRow(context.Background(), `select count(*) from tables.records where table_id=$1`, x.tid).Scan(&n)
	return
}

func TestFormTokenEntropyAndHash(t *testing.T) {
	x := newFormFx(t)
	id1, t1 := x.create(x.cfg())
	_, t2 := x.create(x.cfg())
	if !ValidFormToken(t1) || len(t1) != 22 || t1 == t2 {
		t.Fatalf("tokens %q %q", t1, t2)
	}
	sum := sha256.Sum256([]byte(t1))
	var h, row string
	x.admin.QueryRow(context.Background(), `select token_hash, f::text from tables.form_links f where id=$1`, id1).Scan(&h, &row)
	if h != hex.EncodeToString(sum[:]) || strings.Contains(row, t1) {
		t.Errorf("hash %q row %q", h, row)
	}
	x.as("owner", command.Owner, func(c *command.Ctx) {
		for _, name := range []string{"table_form.list", "table_form.get"} {
			out, err := handlers[name](c, json.RawMessage(fmt.Sprintf(`{"table_id":%q,"id":%q}`, x.tid, id1)))
			b, _ := json.Marshal(out)
			if err != nil || strings.Contains(string(b), t1) {
				t.Errorf("%s: %v %s", name, err, b)
			}
		}
	})
	var n int
	x.admin.QueryRow(context.Background(), `select count(*) from core.events where workspace_id=$1 and payload::text like '%'||$2||'%'`, x.ws, t1).Scan(&n)
	if n != 0 {
		t.Error("token in events")
	}
}

func TestFormConfigRules(t *testing.T) {
	x := newFormFx(t)
	bad := func(cfg map[string]any, what string) {
		x.as("owner", command.Owner, func(c *command.Ctx) {
			if _, err := rcall(formCreate, c, map[string]any{"table_id": x.tid, "name": "F", "config": cfg}); rcode(err) != command.Invalid {
				t.Errorf("%s: %v", what, err)
			}
		})
	}
	bad(x.cfg("Pic"), "attachment")
	bad(x.cfg("Owner"), "user")
	bad(map[string]any{"fields": []any{}}, "no fields")
	bad(map[string]any{"fields": []any{map[string]any{"field_id": newID()}}}, "unknown field")
	bad(map[string]any{"fields": []any{map[string]any{"field_id": x.f["Name"]}, map[string]any{"field_id": x.f["Name"]}}}, "twice")
	bad(map[string]any{"fields": []any{map[string]any{"field_id": x.f["Name"], "label": strings.Repeat("x", 101)}}}, "long label")
	id, _ := x.create(x.cfg())
	var cfg string
	x.admin.QueryRow(context.Background(), `select config::text from tables.form_links where id=$1`, id).Scan(&cfg)
	if strings.Contains(cfg, "uploads") {
		t.Errorf("uploads stored: %s", cfg)
	}
	x.as("outsider", command.Member, func(c *command.Ctx) { // a writer, not a manager
		if _, err := rcall(formCreate, c, map[string]any{"table_id": x.tid, "name": "F", "config": x.cfg()}); rcode(err) != command.Forbidden {
			t.Errorf("writer create: %v", err)
		}
		if _, err := rcall(formList, c, map[string]any{"table_id": x.tid}); err != nil {
			t.Errorf("writer list: %v", err)
		}
	})
}

func TestFormsPerTableCapUnderConcurrency(t *testing.T) {
	x := newFormFx(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, conflict := 0, 0
	for i := 0; i < 9; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := x.commit(func(c *command.Ctx) error {
				_, err := rcall(formCreate, c, map[string]any{"table_id": x.tid, "name": "F", "config": x.cfg()})
				return err
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else if rcode(err) == command.Conflict {
				conflict++
			} else {
				t.Errorf("unexpected %v", err)
			}
		}()
	}
	wg.Wait()
	var n int
	x.admin.QueryRow(context.Background(), `select count(*) from tables.form_links where table_id=$1`, x.tid).Scan(&n)
	if ok != 5 || conflict != 4 || n != 5 {
		t.Errorf("ok %d conflict %d rows %d", ok, conflict, n)
	}
}

func TestFormSubmitValidatesAgainstCurrentConfigAndEventHasNoValues(t *testing.T) {
	x := newFormFx(t)
	id, tok := x.create(x.cfg())
	name, email := x.f["Name"], x.f["Email"]
	inv := func(p any, want string) {
		t.Helper()
		err := x.submit(tok, p)
		if rcode(err) != command.Invalid || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: got %v, want %q", p, err, want)
		}
	}
	inv(map[string]any{"values": map[string]any{email: "a@b.in"}}, "Your name is required")
	inv(map[string]any{"values": map[string]any{name: "n", email: "nope"}}, "Email")
	inv(map[string]any{"values": map[string]any{name: "n", x.f["Qty"]: "5"}}, "changed") // not on the form
	inv(map[string]any{"values": map[string]any{name: "n", x.f["Pic"]: "x"}}, "changed")
	if x.count() != 0 {
		t.Fatal("a refused submission stored a record")
	}
	if err := x.submit(tok, map[string]any{"values": map[string]any{name: "SECRET-VALUE-77", email: "a@b.in"}}); err != nil {
		t.Fatal(err)
	}
	var cb, ub *string
	var subs int
	x.admin.QueryRow(context.Background(), `select (select created_by::text from tables.records where table_id=$1), (select updated_by::text from tables.records where table_id=$1), (select submissions from tables.form_links where id=$2)`, x.tid, id).Scan(&cb, &ub, &subs)
	if cb != nil || ub != nil || subs != 1 {
		t.Errorf("created_by %v updated_by %v submissions %d", cb, ub, subs)
	}
	var payload string
	if err := x.admin.QueryRow(context.Background(), `select payload::text from core.events where workspace_id=$1 and kind='record.created' order by id desc limit 1`, x.ws).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var ev map[string]any
	_ = json.Unmarshal([]byte(payload), &ev)
	if ev["via"] != "form" || ev["form_id"] != id || strings.Contains(payload, "SECRET-VALUE-77") || strings.Contains(payload, "a@b.in") {
		t.Errorf("event %s", payload)
	}
	// honeypot: success, nothing stored
	if err := x.submit(tok, map[string]any{"values": map[string]any{name: "bot"}, "hp": "x"}); err != nil || x.count() != 1 {
		t.Errorf("honeypot: %v, %d records", err, x.count())
	}
	// the config changes: Email leaves the form, Qty joins; the next submission follows the new config
	if err := x.commit(func(c *command.Ctx) error {
		cfg := map[string]any{"fields": []any{map[string]any{"field_id": name}, map[string]any{"field_id": x.f["Qty"]}}}
		_, err := rcall(formUpdate, c, map[string]any{"id": id, "config": cfg})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	inv(map[string]any{"values": map[string]any{name: "n", email: "a@b.in"}}, "changed")
	if err := x.submit(tok, map[string]any{"values": map[string]any{name: "n", x.f["Qty"]: "5"}}); err != nil {
		t.Errorf("new config: %v", err)
	}
	// a trashed field drops out of what the form accepts
	x.admin.Exec(context.Background(), `update tables.fields set deleted_at=now() where id=$1`, x.f["Qty"])
	inv(map[string]any{"values": map[string]any{name: "n", x.f["Qty"]: "5"}}, "changed")
	if err := x.submit(tok, map[string]any{"values": map[string]any{name: "n2"}}); err != nil {
		t.Errorf("others keep working: %v", err)
	}
}

func TestFormRecordLimitIsRespectedWithoutPlanNumbers(t *testing.T) {
	x := newFormFx(t)
	_, tok := x.create(x.cfg())
	x.admin.Exec(context.Background(), `update core.workspaces set plan='free' where id=$1`, x.ws)
	x.admin.Exec(context.Background(), `update tables.bases set record_count=1000 where id=$1`, x.openBase)
	err := x.submit(tok, map[string]any{"values": map[string]any{x.f["Name"]: "n"}})
	if rcode(err) != command.Conflict || strings.ContainsAny(err.Error(), "0123456789") || x.count() != 0 {
		t.Errorf("got %v, %d records", err, x.count())
	}
}

func TestFormIsGoneWhenDisabledRotatedTrashedOrCreatorLostAccess(t *testing.T) {
	x := newFormFx(t)
	ctx := context.Background()
	gone := func(tok, why string) {
		t.Helper()
		h := HashFormToken(tok)
		if f, err := ResolveForm(ctx, x.app, h); err == nil {
			// the resolver may still answer (creator gone): the submit and GET must not
			if err := x.submit(tok, map[string]any{"values": map[string]any{x.f["Name"]: "n"}}); err != ErrNoForm {
				t.Errorf("%s: submit %v", why, err)
			}
			if _, err := PublicForm(ctx, x.app, command.NewRegistry(), h, f); err != ErrNoForm {
				t.Errorf("%s: get %v", why, err)
			}
		} else if err != ErrNoForm {
			t.Errorf("%s: %v", why, err)
		}
	}
	id, tok := x.create(x.cfg())
	x.commit(func(c *command.Ctx) error { _, err := rcall(formDisable, c, map[string]any{"id": id}); return err })
	gone(tok, "disabled")
	var tok2 string
	x.commit(func(c *command.Ctx) error {
		r, err := rcall(formRotate, c, map[string]any{"id": id})
		tok2 = strings.TrimPrefix(r["url"].(string), "/tf/")
		return err
	})
	gone(tok, "rotated away")
	if err := x.submit(tok2, map[string]any{"values": map[string]any{x.f["Name"]: "n"}}); err != nil {
		t.Errorf("rotate re-enables with the new link: %v", err)
	}
	x.admin.Exec(ctx, `update tables.tables set deleted_at=now() where id=$1`, x.tid)
	gone(tok2, "table trashed")
	x.admin.Exec(ctx, `update tables.tables set deleted_at=null where id=$1`, x.tid)
	x.admin.Exec(ctx, `update tables.bases set deleted_at=now() where id=$1`, x.openBase)
	gone(tok2, "base trashed")
	x.admin.Exec(ctx, `update tables.bases set deleted_at=null where id=$1`, x.openBase)
	x.admin.Exec(ctx, `delete from core.memberships where workspace_id=$1 and user_id=$2`, x.ws, x.users["owner"])
	gone(tok2, "creator left")
}

// A submission that started before a rotate or disable must die with the old link.
func TestRotateAndDisableRaceAnInFlightSubmit(t *testing.T) {
	for _, op := range []string{"rotate", "disable"} {
		t.Run(op, func(t *testing.T) {
			x := newFormFx(t)
			ctx := context.Background()
			id, tok := x.create(x.cfg())
			h := HashFormToken(tok)
			f, err := ResolveForm(ctx, x.app, h) // resolved while the link was still good
			if err != nil {
				t.Fatal(err)
			}
			tx, err := x.admin.Begin(ctx) // the rotate/disable in progress holds the row FOR UPDATE
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `select 1 from tables.form_links where id=$1 for update`, id); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				b, _ := json.Marshal(map[string]any{"values": map[string]any{x.f["Name"]: "late"}})
				done <- SubmitForm(ctx, x.app, command.NewRegistry(), h, f, b)
			}()
			select {
			case err := <-done:
				t.Fatalf("submit did not wait for the lock: %v", err)
			case <-time.After(400 * time.Millisecond):
			}
			q := `update tables.form_links set token_hash='x'||token_hash where id=$1`
			if op == "disable" {
				q = `update tables.form_links set enabled=false where id=$1`
			}
			if _, err := tx.Exec(ctx, q, id); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != ErrNoForm || x.count() != 0 {
				t.Errorf("got %v, %d records", err, x.count())
			}
		})
	}
}

func TestResolveFormAsAppWithNoTenant(t *testing.T) {
	x := newFormFx(t)
	ctx := context.Background()
	idA, tokA := x.create(x.cfg())
	x.create(x.cfg())
	var n int
	x.app.QueryRow(ctx, `select count(*) from tables.form_links`).Scan(&n)
	if n != 0 {
		t.Fatalf("cnote_app with no tenant sees %d form rows", n)
	}
	rows, err := x.app.Query(ctx, `select id::text from tables.resolve_form($1)`, HashFormToken(tokA))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 1 || ids[0] != idA {
		t.Errorf("resolved %v, want only %s", ids, idA)
	}
	var secdef bool
	var cfg []string
	var acl string
	x.admin.QueryRow(ctx, `select prosecdef, coalesce(proconfig, '{}'), coalesce(proacl::text,'') from pg_proc where oid='tables.resolve_form(text)'::regprocedure`).Scan(&secdef, &cfg, &acl)
	if !secdef || len(cfg) != 1 || cfg[0] != "search_path=pg_catalog, pg_temp" || strings.Contains(acl, "{=") || strings.Contains(acl, ",=") || !strings.Contains(acl, "cnote_app=X") {
		t.Errorf("secdef %v config %v acl %s", secdef, cfg, acl)
	}
}
