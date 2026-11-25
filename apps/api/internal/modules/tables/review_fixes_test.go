package tables

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
)

// Tests for the Tables 1a review findings. Real database, like records_test.go.

// commitAs is commit for any person (an import must run as the person who started it).
func (x *tfix) commitAs(user string, role command.Role, fn func(*command.Ctx) error) error {
	ctx := context.Background()
	tx, err := db.BeginTenant(ctx, x.app, x.ws)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	id := x.users[user]
	if err := fn(&command.Ctx{Context: ctx, Tx: tx, Pool: x.app, Principal: command.Principal{Kind: command.KindUser, UserID: &id, WorkspaceID: x.ws, Role: role}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (x *tfix) lockField(id string) {
	x.t.Helper()
	if _, err := x.admin.Exec(context.Background(), `update tables.fields set locked=true where id=$1`, id); err != nil {
		x.t.Fatal(err)
	}
}

func TestLockedLinkFieldNeedsManager(t *testing.T) {
	x := newTFix(t)
	cust, cf := x.mkTable(x.openBase, "Customers", fspec{"Name", "text", ""})
	ord, of := x.mkTable(x.openBase, "Orders", fspec{"Name", "text", ""}, fspec{"Customer", "link", `{"table_id":"` + cust + `","multiple":true}`})
	var c1, o1 string
	x.commit(func(c *command.Ctx) error {
		r, err := rcall(recordCreate, c, map[string]any{"table_id": cust, "values": map[string]any{cf["Name"]: "c1"}})
		if err != nil {
			t.Fatal(err)
		}
		c1 = r["id"].(string)
		r, err = rcall(recordCreate, c, map[string]any{"table_id": ord, "values": map[string]any{of["Name"]: "o1"}})
		if err != nil {
			t.Fatal(err)
		}
		o1 = r["id"].(string)
		return nil
	})
	x.lockField(of["Customer"])
	links := map[string]any{of["Customer"]: []string{c1}}
	x.as("outsider", command.Member, func(c *command.Ctx) { // a writer, not a manager
		if _, err := rcall(recordCreate, c, map[string]any{"table_id": ord, "links": links}); rcode(err) != command.Forbidden {
			t.Errorf("create: %v", err)
		}
		if _, err := rcall(recordCreateMany, c, map[string]any{"table_id": ord, "records": []any{map[string]any{"links": links}}}); err == nil || !strings.Contains(err.Error(), "locked") { // create_many reports it per record
			t.Errorf("create_many: %v", err)
		}
		if _, err := rcall(recordUpdate, c, map[string]any{"id": o1, "links": links}); rcode(err) != command.Forbidden {
			t.Errorf("update: %v", err)
		}
	})
	x.as("admin", command.Admin, func(c *command.Ctx) {
		if _, err := rcall(recordUpdate, c, map[string]any{"id": o1, "links": links}); err != nil {
			t.Errorf("a manager may: %v", err)
		}
	})
}

func TestImportIntoLockedField(t *testing.T) {
	x := newTFix(t)
	tid, f := x.mkTable(x.openBase, "T", fspec{"Name", "text", ""}, fspec{"Secret", "text", ""})
	x.lockField(f["Secret"])
	var handle string
	if err := x.commit(func(c *command.Ctx) error {
		out, err := StoreUpload(c, tid, "", "t.csv", []byte("Name,Secret\na,b\n"))
		handle = out["handle"].(string)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	x.as("outsider", command.Member, func(c *command.Ctx) { // a writer
		// the Secret column matches the locked field by name
		if _, err := rcall(importPreview, c, map[string]any{"handle": handle}); rcode(err) != command.Forbidden {
			t.Errorf("name-matched preview: %v", err)
		}
		if _, err := rcall(importRun, c, map[string]any{"handle": handle}); rcode(err) != command.Forbidden {
			t.Errorf("name-matched run: %v", err)
		}
		m := map[string]any{"columns": []any{map[string]any{"index": 1, "field_id": f["Secret"]}}}
		if _, err := rcall(importRun, c, map[string]any{"handle": handle, "mapping": m}); rcode(err) != command.Forbidden {
			t.Errorf("mapped run: %v", err)
		}
		skip := map[string]any{"columns": []any{map[string]any{"index": 1, "skip": true}}}
		if _, err := rcall(importPreview, c, map[string]any{"handle": handle, "mapping": skip}); err != nil {
			t.Errorf("skipping the locked column must work: %v", err)
		}
	})
	x.as("admin", command.Admin, func(c *command.Ctx) { // a manager may
		if _, err := rcall(importPreview, c, map[string]any{"handle": handle}); err != nil {
			t.Errorf("manager preview: %v", err)
		}
	})
}

func TestImportJobRejectsFieldLockedAfterRun(t *testing.T) {
	x := newTFix(t)
	tid, f := x.mkTable(x.openBase, "T", fspec{"Name", "text", ""}, fspec{"Secret", "text", ""})
	var handle string
	if err := x.commitAs("outsider", command.Member, func(c *command.Ctx) error {
		out, err := StoreUpload(c, tid, "", "t.csv", []byte("Name,Secret\na,b\n"))
		handle = out["handle"].(string)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := x.commitAs("outsider", command.Member, func(c *command.Ctx) error {
		_, err := rcall(importRun, c, map[string]any{"handle": handle})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	x.lockField(f["Secret"])
	if err := x.commit(func(c *command.Ctx) error { return ImportJob(c, rmustJSON(map[string]string{"import_id": handle})) }); err != nil {
		t.Fatal(err)
	}
	var status string
	var n int
	x.admin.QueryRow(context.Background(), `select status from tables.imports where id=$1`, handle).Scan(&status)
	x.admin.QueryRow(context.Background(), `select count(*) from tables.records where table_id=$1`, tid).Scan(&n)
	if status != "failed" || n != 0 {
		t.Errorf("status %q, %d records", status, n)
	}
}

func TestRecordCountMatchesLiveRecords(t *testing.T) {
	x := newTFix(t)
	ctx := context.Background()
	base := x.openBase
	t1, f1 := x.mkTable(base, "A", fspec{"Name", "text", ""})
	t2, f2 := x.mkTable(base, "B", fspec{"Name", "text", ""})
	check := func(step string, want int) {
		t.Helper()
		var got, live int
		x.admin.QueryRow(ctx, `select record_count from tables.bases where id=$1`, base).Scan(&got)
		x.admin.QueryRow(ctx, `select count(*) from tables.records r join tables.tables t on t.id=r.table_id where t.base_id=$1 and t.deleted_at is null and r.deleted_at is null`, base).Scan(&live)
		if got != live || live != want {
			t.Errorf("%s: record_count %d, live records %d, want %d", step, got, live, want)
		}
	}
	var ids []string
	x.commit(func(c *command.Ctx) error {
		for i := 0; i < 3; i++ {
			r, err := rcall(recordCreate, c, map[string]any{"table_id": t1, "values": map[string]any{f1["Name"]: fmt.Sprint("a", i)}})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, r["id"].(string))
		}
		for i := 0; i < 2; i++ {
			if _, err := rcall(recordCreate, c, map[string]any{"table_id": t2, "values": map[string]any{f2["Name"]: fmt.Sprint("b", i)}}); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	check("after creates", 5)
	run := func(name string, in any) error {
		return x.commit(func(c *command.Ctx) error { _, err := callErr(c, name, in); return err })
	}
	if err := run("record.delete", map[string]any{"ids": ids[:1]}); err != nil {
		t.Fatal(err)
	}
	check("record deleted", 4)
	if err := run("table.delete", map[string]any{"id": t1}); err != nil {
		t.Fatal(err)
	}
	check("table deleted", 2)
	if err := run("table.restore", map[string]any{"id": t1}); err != nil {
		t.Fatal(err)
	}
	check("table restored", 4)
	// restoring a table into a full base is refused and leaves the count alone
	run("table.delete", map[string]any{"id": t1})
	x.admin.Exec(ctx, `update tables.bases set record_count = 5000 where id=$1`, base) // free plan cap is below this
	x.admin.Exec(ctx, `update core.workspaces set plan='free' where id=$1`, x.ws)
	if err := run("table.restore", map[string]any{"id": t1}); rcode(err) != command.Conflict {
		t.Errorf("restore over the cap: %v", err)
	}
	x.admin.Exec(ctx, `update core.workspaces set plan='business' where id=$1`, x.ws)
	x.admin.Exec(ctx, `update tables.bases set record_count = 2 where id=$1`, base)
	check("restore refused", 2)
	if err := run("table.restore", map[string]any{"id": t1}); err != nil {
		t.Fatal(err)
	}
	check("restored again", 4)
	// base delete and restore keep the count; field delete does not touch it
	run("base.delete", map[string]any{"id": base})
	run("base.restore", map[string]any{"id": base})
	check("base round trip", 4)
	if err := run("field.delete", map[string]any{"id": f2["Name"]}); err != nil {
		t.Log("field.delete:", err) // the primary field cannot go; the count is checked either way
	}
	check("field delete", 4)
	// purge after 30 days: trashed table and trashed record are already out of the count
	x.admin.Exec(ctx, `update tables.records set deleted_at=now()-interval '31 days' where id=$1`, ids[0])
	run("table.delete", map[string]any{"id": t2})
	x.admin.Exec(ctx, `update tables.tables set deleted_at=now()-interval '31 days' where id=$1`, t2)
	if err := purgeWorkspace(ctx, x.app, x.ws); err != nil {
		t.Fatal(err)
	}
	check("after purge", 2)
}

func TestConvertWaitsForWriters(t *testing.T) {
	x := newTFix(t)
	tid, f := x.mkTable(x.openBase, "T", fspec{"Name", "text", ""}, fspec{"Amt", "number", ""})
	x.admin.Exec(context.Background(), `insert into tables.records(workspace_id,table_id,seq,data) values ($1,$2,1,$3::jsonb)`, x.ws, tid, fmt.Sprintf(`{"%s":3}`, f["Amt"]))
	x.admin.Exec(context.Background(), `update tables.tables set next_seq=2 where id=$1`, tid)
	var wg sync.WaitGroup
	wg.Add(2)
	var cerr error
	go func() { // a writer that has validated against "number" and not yet committed
		defer wg.Done()
		x.commit(func(c *command.Ctx) error {
			_, err := rcall(recordCreate, c, map[string]any{"table_id": tid, "values": map[string]any{f["Amt"]: 12.5}})
			time.Sleep(300 * time.Millisecond)
			return err
		})
	}()
	go func() {
		defer wg.Done()
		time.Sleep(100 * time.Millisecond)
		cerr = x.commit(func(c *command.Ctx) error {
			_, err := callErr(c, "field.update", map[string]any{"id": f["Amt"], "type": "currency", "confirm": true})
			return err
		})
	}()
	wg.Wait()
	if cerr != nil {
		t.Fatal(cerr)
	}
	var n int
	var typ string
	x.admin.QueryRow(context.Background(), `select count(*) from tables.records where table_id=$1 and data->>$2 in ('300','1250')`, tid, f["Amt"]).Scan(&n)
	x.admin.QueryRow(context.Background(), `select type from tables.fields where id=$1`, f["Amt"]).Scan(&typ)
	if typ != "currency" || n != 2 {
		t.Errorf("type %s, %d of 2 records converted (the concurrent insert was missed)", typ, n)
	}
	// and a writer that arrives after the conversion validates against the new type
	x.commit(func(c *command.Ctx) error {
		if _, err := rcall(recordCreate, c, map[string]any{"table_id": tid, "values": map[string]any{f["Amt"]: 12.5}}); rcode(err) != command.Invalid {
			t.Errorf("a fractional paise must be refused now: %v", err)
		}
		return nil
	})
}

func TestConvertRefusesMoreThanTwoDecimals(t *testing.T) {
	x := newTFix(t)
	for _, typ := range []string{"number", "percent"} {
		tid, f := x.mkTable(x.openBase, "T"+typ, fspec{"Name", "text", ""}, fspec{"V", typ, ""})
		x.admin.Exec(context.Background(), `insert into tables.records(workspace_id,table_id,seq,data) values ($1,$2,1,$3::jsonb),($1,$2,2,$4::jsonb),($1,$2,3,$5::jsonb)`,
			x.ws, tid, fmt.Sprintf(`{"%s":1.005}`, f["V"]), fmt.Sprintf(`{"%s":2.5}`, f["V"]), fmt.Sprintf(`{"%s":0.1234}`, f["V"]))
		err := x.commit(func(c *command.Ctx) error {
			_, err := callErr(c, "field.update", map[string]any{"id": f["V"], "type": "currency", "confirm": true})
			return err
		})
		if rcode(err) != command.Conflict || !strings.Contains(err.Error(), "2 values have more than 2 decimals") {
			t.Errorf("%s: %v", typ, err)
		}
		var typNow string
		x.admin.QueryRow(context.Background(), `select type from tables.fields where id=$1`, f["V"]).Scan(&typNow)
		if typNow != typ {
			t.Errorf("%s was converted anyway", typ)
		}
	}
}

func TestInverseLinkCardinalityCheck(t *testing.T) {
	x := newTFix(t)
	cust, cf := x.mkTable(x.openBase, "Customers", fspec{"Name", "text", ""})
	ord, of := x.mkTable(x.openBase, "Orders", fspec{"Name", "text", ""}, fspec{"Customer", "link", `{"table_id":"` + cust + `","multiple":false}`})
	invID := newID()
	x.admin.Exec(context.Background(), `insert into tables.fields(id,workspace_id,table_id,name,type,config,position) values ($1,$2,$3,'Orders','link',$4::jsonb,1)`,
		invID, x.ws, cust, fmt.Sprintf(`{"table_id":"%s","multiple":true,"inverse_of":"%s"}`, ord, of["Customer"]))
	var c1, o1, o2 string
	x.commit(func(c *command.Ctx) error {
		mk := func(table, field, name string) string {
			r, err := rcall(recordCreate, c, map[string]any{"table_id": table, "values": map[string]any{field: name}})
			if err != nil {
				t.Fatal(err)
			}
			return r["id"].(string)
		}
		c1 = mk(cust, cf["Name"], "c1")
		o1, o2 = mk(ord, of["Name"], "o1"), mk(ord, of["Name"], "o2")
		for _, o := range []string{o1, o2} {
			if _, err := rcall(recordUpdate, c, map[string]any{"id": o, "links": map[string]any{of["Customer"]: []string{c1}}}); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	single := func() error {
		return x.commit(func(c *command.Ctx) error {
			_, err := callErr(c, "field.update", map[string]any{"id": invID, "config": map[string]any{"multiple": false}})
			return err
		})
	}
	if err := single(); rcode(err) != command.Conflict {
		t.Fatalf("customer c1 has two orders, the inverse must stay multiple: %v", err)
	}
	x.admin.Exec(context.Background(), `delete from tables.links where from_record=$1`, o2)
	if err := single(); err != nil {
		t.Errorf("one order per customer: %v", err)
	}
}
