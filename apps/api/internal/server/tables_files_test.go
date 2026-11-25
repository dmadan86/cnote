package server_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/modules/files"
	"github.com/cnote/api/internal/modules/tables"
)

// Phase 1b record attachments (design 18.1, 18.8): HTTP contract, ownership chain, counter, object lifecycle.

var pdfBytes = []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n")

func pngN(n int) []byte { return append(append([]byte{}, png...), bytes.Repeat([]byte{0}, n)...) }

type tfs struct {
	b                        *bt
	ws                       string
	base, table, field, name string
}

// newTFS makes a base with one table and one attachment field in the owner's workspace.
func newTFS(b *bt) *tfs {
	b.t.Helper()
	base := b.ok("base.create", map[string]any{"space_id": b.space, "name": "B"})["id"].(string)
	return b.tfsIn(b.owner, base, "T")
}

// tfsIn adds a table with an attachment field to an existing base.
func (b *bt) tfsIn(w who, base, name string) *tfs {
	tb := b.okAs(w, "table.create", map[string]any{"base_id": base, "name": name})
	tid := tb["id"].(string)
	fld := b.okAs(w, "field.create", map[string]any{"table_id": tid, "name": "Files", "type": "attachment"})["id"].(string)
	return &tfs{b: b, ws: w.ws, base: base, table: tid, field: fld, name: tb["primary_field_id"].(string)}
}

func (b *bt) stageAs(w who, ws, table, field, filename string, data []byte) resp {
	b.t.Helper()
	q := url.Values{"ws": {ws}, "table_id": {table}, "field_id": {field}, "filename": {filename}}
	req, _ := http.NewRequest("POST", b.gosv.URL+"/api/tables/files?"+q.Encode(), bytes.NewReader(data))
	w.with(req.Header)
	return b.send(req)
}

func (s *tfs) stage(w who, filename string, data []byte) resp {
	return s.b.stageAs(w, s.ws, s.table, s.field, filename, data)
}

func (s *tfs) handle(w who, filename string, data []byte) string {
	s.b.t.Helper()
	r := s.stage(w, filename, data)
	if r.status != 200 {
		s.b.t.Fatalf("stage: %d %s", r.status, r.body)
	}
	return r.json()["handle"].(string)
}

func (s *tfs) files(h ...string) map[string]any {
	return map[string]any{s.field: map[string]any{"add": h}}
}

func (s *tfs) newRecord(w who, handles ...string) map[string]any {
	s.b.t.Helper()
	return s.b.okAs(w, "record.create", map[string]any{"table_id": s.table, "values": map[string]any{}, "files": s.files(handles...)})
}

func (b *bt) download(w who, ws, record, file string) (resp, http.Header) {
	b.t.Helper()
	req, _ := http.NewRequest("GET", b.gosv.URL+"/api/tables/files/"+file+"?"+url.Values{"ws": {ws}, "record_id": {record}}.Encode(), nil)
	w.with(req.Header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, body}, res.Header
}

func (s *tfs) counter() string {
	return s.b.dbStrings(s.ws, "select attachment_bytes::text from tables.bases where id=$1::uuid", s.base)[0]
}

// rowsSum is what the counter must equal: every record file of the base, pending and attached.
func (s *tfs) rowsSum() string {
	return s.b.dbStrings(s.ws, "select coalesce(sum(size_bytes),0)::text from core.attachments where base_id=$1::uuid and field_id is not null", s.base)[0]
}

func cellIDs(rec map[string]any, field string) []string {
	data, _ := rec["data"].(map[string]any)
	var out []string
	l, _ := data[field].([]any)
	for _, e := range l {
		out = append(out, e.(map[string]any)["id"].(string))
	}
	return out
}

func TestRecordFilesStageAttachDownloadAndHeaders(t *testing.T) {
	b := newBT(t, false)
	s := newTFS(b)
	o := b.owner

	// a PDF and an image; the sniffed type decides, the name and the client's type do not
	h1 := s.handle(o, "résumé \"final\".png", pngN(2000))
	if got := s.counter(); got != fmt.Sprint(len(png)+2000) {
		t.Fatalf("counter after stage: %s", got)
	}
	h2 := s.handle(o, "scan.png", pdfBytes) // a PDF under a .png name stays a PDF
	r := s.stage(o, "x.png", pngN(10))
	st := r.json()
	if st["content_type"] != "image/png" || int(st["size"].(float64)) != len(png)+10 || st["filename"] != "x.png" {
		t.Fatalf("stage answer %v", st)
	}
	for name, data := range map[string][]byte{"a.svg": []byte("<svg xmlns='http://www.w3.org/2000/svg'><script>1</script></svg>"), "a.html": []byte("<html><script>1</script>"),
		"a.zip": []byte("PK\x03\x04rest"), "a.csv": []byte("a,b\n1,2\n"), "empty.png": {}} {
		if r := s.stage(o, name, data); r.status != 400 {
			t.Errorf("%s: staged with %d", name, r.status)
		}
	}
	if r := b.stageAs(o, s.ws, s.table, s.field, "a\r\nSet-Cookie: x=1.png", png); r.status != 400 {
		t.Errorf("raw CR/LF in a name: %d", r.status)
	}
	if r := s.stage(o, "big.png", pngN(10<<20)); r.status != 400 {
		t.Errorf("11 MB staged: %d", r.status)
	}

	rec := s.newRecord(o, h1, h2)
	recID := rec["id"].(string)
	if ids := cellIDs(rec, s.field); len(ids) != 2 || ids[0] != h1 || ids[1] != h2 {
		t.Fatalf("cell %v want [%s %s]", ids, h1, h2)
	}
	meta := rec["data"].(map[string]any)[s.field].([]any)[0].(map[string]any)
	if meta["name"] != "résumé final.png" || meta["type"] != "image/png" || int(meta["size"].(float64)) != len(png)+2000 {
		t.Fatalf("meta %v", meta)
	}
	// the handle is consumed: a second record cannot take it
	if r := b.call(o, "record.create", map[string]any{"table_id": s.table, "values": map[string]any{}, "files": s.files(h1)}); r.status != 400 {
		t.Errorf("handle reused: %d %s", r.status, r.body)
	}

	// image: inline, by its sniffed type; the RFC 5987 name carries the unicode
	dl, h := b.download(o, s.ws, recID, h1)
	if dl.status != 200 || !bytes.Equal(dl.body, pngN(2000)) {
		t.Fatalf("download %d", dl.status)
	}
	cd := h.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "inline; filename=\"r_sum_ final.png\"; filename*=UTF-8''r%C3%A9sum%C3%A9%20final.png") ||
		h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("image headers: %q %v", cd, h)
	}
	// PDF: always an attachment, even under a .png name
	_, h = b.download(o, s.ws, recID, h2)
	if !strings.HasPrefix(h.Get("Content-Disposition"), "attachment; ") || h.Get("Content-Type") != "application/pdf" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("pdf headers: %v", h)
	}
	// stored as an image but the bytes are not one any more: served as an opaque attachment
	b.adminSQL("update core.attachments set data = '<html>x</html>'::bytea where id = '" + h1 + "'")
	_, h = b.download(o, s.ws, recID, h1)
	if h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("mismatched image headers: %v", h)
	}

	// the file belongs to its record only
	rec2 := s.newRecord(o)
	if r, _ := b.download(o, s.ws, rec2["id"].(string), h1); r.status != 404 {
		t.Errorf("file through another record: %d", r.status)
	}
	// a record or field in the trash hides its files; restoring brings them back
	b.ok("record.delete", map[string]any{"ids": []string{recID}})
	if r, _ := b.download(o, s.ws, recID, h2); r.status != 404 {
		t.Errorf("file of a trashed record: %d", r.status)
	}
	b.ok("record.restore", map[string]any{"ids": []string{recID}})
	if r, _ := b.download(o, s.ws, recID, h2); r.status != 200 {
		t.Errorf("after restore: %d", r.status)
	}
	b.ok("field.delete", map[string]any{"id": s.field})
	if r, _ := b.download(o, s.ws, recID, h2); r.status != 404 {
		t.Errorf("file of a trashed field: %d", r.status)
	}
	b.ok("field.restore", map[string]any{"id": s.field})
	if r, _ := b.download(o, s.ws, recID, h2); r.status != 200 {
		t.Errorf("after field restore: %d", r.status)
	}

	// values and clear cannot touch an attachment field
	if r := b.call(o, "record.update", map[string]any{"id": recID, "values": map[string]any{s.field: []any{}}}); r.status != 400 {
		t.Errorf("values on a file field: %d", r.status)
	}
	if r := b.call(o, "record.update", map[string]any{"id": recID, "clear": []string{s.field}}); r.status != 400 {
		t.Errorf("clear on a file field: %d", r.status)
	}
	// detach: row gone at once, bytes released at once, history and event name the field
	before := s.counter()
	up := b.ok("record.update", map[string]any{"id": recID, "files": map[string]any{s.field: map[string]any{"remove": []string{h2}}}})
	if ids := cellIDs(up, s.field); len(ids) != 1 || ids[0] != h1 {
		t.Fatalf("after remove %v", ids)
	}
	if r, _ := b.download(o, s.ws, recID, h2); r.status != 404 {
		t.Errorf("detached file: %d", r.status)
	}
	if s.counter() == before || s.counter() != s.rowsSum() {
		t.Errorf("counter %s rows %s (was %s)", s.counter(), s.rowsSum(), before)
	}
	if n := b.dbStrings(s.ws, "select count(*)::text from tables.record_history where record_id=$1::uuid and change ? $2", recID, s.field); n[0] != "1" {
		t.Errorf("history rows %v", n)
	}
	// removing a file that is not in this cell changes nothing
	if r := b.call(o, "record.update", map[string]any{"id": recID, "files": map[string]any{s.field: map[string]any{"remove": []string{h2}}}}); r.status != 400 {
		t.Errorf("remove of a foreign id: %d", r.status)
	}
	// at most 10 per cell
	var hs []string
	for i := 0; i < 10; i++ {
		hs = append(hs, s.handle(o, fmt.Sprintf("n%d.png", i), png))
	}
	if r := b.call(o, "record.update", map[string]any{"id": recID, "files": s.files(hs...)}); r.status != 409 {
		t.Errorf("11 files in a cell: %d %s", r.status, r.body)
	}
	// base.get reports the numbers
	got := b.ok("base.get", map[string]any{"id": s.base})
	if int64(got["limits"].(map[string]any)["attachment_bytes_per_base"].(float64)) != 100<<20 || got["usage"].(map[string]any)["attachment_bytes"].(float64) <= 0 {
		t.Errorf("base.get %v %v", got["limits"], got["usage"])
	}
	// an unattached upload can be dropped before saving; someone else's cannot
	adm := b.addMember(o, b.signup("Ada", false), "admin")
	if r := b.deleteHandle(adm, s.ws, hs[0]); r.status != 404 {
		t.Errorf("discard of another's handle: %d", r.status)
	}
	if r := b.deleteHandle(o, s.ws, hs[0]); r.status != 200 {
		t.Errorf("discard: %d %s", r.status, r.body)
	}
	if s.counter() != s.rowsSum() {
		t.Errorf("counter %s rows %s after discard", s.counter(), s.rowsSum())
	}
}

func (b *bt) deleteHandle(w who, ws, handle string) resp {
	req, _ := http.NewRequest("DELETE", b.gosv.URL+"/api/tables/files/"+handle+"?"+url.Values{"ws": {ws}}.Encode(), nil)
	w.with(req.Header)
	return b.send(req)
}

func TestRecordFilesOwnershipChainAndForeignHandles(t *testing.T) {
	b := newBT(t, false)
	o := b.owner
	a := newTFS(b)
	// same workspace: a second table in the same base, and a second base
	a2 := b.tfsIn(o, a.base, "T2")
	base2 := b.ok("base.create", map[string]any{"space_id": b.space, "name": "B2"})["id"].(string)
	c := b.tfsIn(o, base2, "T3")
	// another workspace
	b2 := newBT(t, false)
	x := newTFS(b2)

	// stage: a field of another table, another base, another workspace, or a table that does not match the field
	for name, r := range map[string]resp{
		"field of another table":     b.stageAs(o, a.ws, a.table, a2.field, "a.png", png),
		"field of another base":      b.stageAs(o, a.ws, a.table, c.field, "a.png", png),
		"table of another base":      b.stageAs(o, a.ws, c.table, a.field, "a.png", png),
		"foreign workspace field":    b.stageAs(o, a.ws, a.table, x.field, "a.png", png),
		"foreign workspace table":    b.stageAs(o, a.ws, x.table, x.field, "a.png", png),
		"foreign ws via own session": b.stageAs(o, x.ws, x.table, x.field, "a.png", png),
		"a text field":               b.stageAs(o, a.ws, a.table, a.name, "a.png", png),
	} {
		if r.status != 404 && r.status != 401 && r.status != 403 {
			t.Errorf("%s: %d %s", name, r.status, r.body)
		}
	}
	if n := b.dbStrings(a.ws, "select count(*)::text from core.attachments where field_id is not null")[0]; n != "0" {
		t.Fatalf("rows after refused stages: %s", n)
	}

	// attach: a handle staged for table A cannot go to a record of A2 (same base), of C (another base) or of X (another workspace)
	h := a.handle(o, "a.png", png)
	rA2 := a2.newRecord(o)
	rC := c.newRecord(o)
	for name, req := range map[string]map[string]any{
		"another table, same base":                   {"id": rA2["id"], "files": map[string]any{a2.field: map[string]any{"add": []string{h}}}},
		"another table, same base, field of staging": {"id": rA2["id"], "files": map[string]any{a.field: map[string]any{"add": []string{h}}}},
		"another base":                               {"id": rC["id"], "files": map[string]any{c.field: map[string]any{"add": []string{h}}}},
	} {
		if r := b.call(o, "record.update", req); r.status == 200 {
			t.Errorf("%s: attached", name)
		}
	}
	rX := b2.ok("record.create", map[string]any{"table_id": x.table, "values": map[string]any{}})
	if r := b2.call(b2.owner, "record.update", map[string]any{"id": rX["id"], "files": map[string]any{x.field: map[string]any{"add": []string{h}}}}); r.status == 200 {
		t.Error("a handle of another workspace attached")
	}
	// another workspace's handle used in this one
	hx := x.handle(b2.owner, "x.png", png)
	rA := a.newRecord(o)
	if r := b.call(o, "record.update", map[string]any{"id": rA["id"], "files": a.files(hx)}); r.status == 200 {
		t.Error("foreign workspace handle attached")
	}
	// another user's handle in the same table and field is refused; the owner of the handle still can
	adm := b.addMember(o, b.signup("Ada", false), "admin")
	if r := b.call(adm, "record.update", map[string]any{"id": rA["id"], "files": a.files(h)}); r.status != 400 {
		t.Errorf("another user's handle: %d %s", r.status, r.body)
	}
	if r := b.call(o, "record.update", map[string]any{"id": rA["id"], "files": a.files(h)}); r.status != 200 {
		t.Errorf("own handle after a refusal (nothing was consumed): %d %s", r.status, r.body)
	}
	// download: through the wrong record, the wrong table's record, another workspace
	for name, rec := range map[string]string{"record of the same table": rA2["id"].(string), "record of another base": rC["id"].(string), "foreign record": rX["id"].(string)} {
		if r, _ := b.download(o, a.ws, rec, h); r.status != 404 {
			t.Errorf("%s: %d", name, r.status)
		}
	}
	if r, _ := b.download(b2.owner, x.ws, rX["id"].(string), h); r.status != 404 {
		t.Errorf("foreign file through own record: %d", r.status)
	}
	// detach of another record's file
	if r := b.call(o, "record.update", map[string]any{"id": rA2["id"], "files": map[string]any{a2.field: map[string]any{"remove": []string{h}}}}); r.status == 200 {
		t.Error("detached a file of another record")
	}
	// the database itself refuses a row that mixes parents
	bad := fmt.Sprintf(`insert into core.attachments (workspace_id, base_id, table_id, field_id, status, staged_by, filename, content_type, size_bytes, storage_key, data)
		values ('%s','%s','%s','%s','pending','u:x','a','image/png',1,'pg','\x00')`, a.ws, a.base, a.table, c.field)
	if err := b.raw(bad); err == nil {
		t.Error("a row with a field of another table was accepted")
	}
	bad = fmt.Sprintf(`insert into core.attachments (workspace_id, base_id, table_id, field_id, status, staged_by, filename, content_type, size_bytes, storage_key, data)
		values ('%s','%s','%s','%s','pending','u:x','a','image/png',1,'pg','\x00')`, a.ws, c.base, a.table, a.field)
	if err := b.raw(bad); err == nil {
		t.Error("a row with a table of another base was accepted")
	}
	// a viewer cannot stage; a stranger cannot download
	viewer := b.addMember(o, b.signup("Vic", false), "guest")
	if r := b.stageAs(viewer, a.ws, a.table, a.field, "a.png", png); r.status == 200 {
		t.Error("a guest staged a file")
	}
	stranger := b.signup("Sam", false)
	stranger.ws = a.ws
	if r, _ := b.download(stranger, a.ws, rA["id"].(string), h); r.status == 200 {
		t.Error("a stranger downloaded a file")
	}
}

func TestRecordFilesLimitsAndSharedUploadUnchanged(t *testing.T) {
	b := newBT(t, false)
	s := newTFS(b)
	o := b.owner
	// 20 unattached uploads per person and base
	for i := 0; i < 20; i++ {
		s.handle(o, fmt.Sprintf("p%d.png", i), png)
	}
	if r := s.stage(o, "p21.png", png); r.status != 409 {
		t.Errorf("21st pending: %d %s", r.status, r.body)
	}
	if s.counter() != s.rowsSum() {
		t.Errorf("counter %s rows %s", s.counter(), s.rowsSum())
	}
	// storage cap: the reservation is refused in the same statement that would pass it
	b.adminSQL(fmt.Sprintf("update tables.bases set attachment_bytes = %d where id = '%s'", 100<<20-50, s.base))
	b.adminSQL(fmt.Sprintf("delete from core.attachments where base_id = '%s'", s.base)) // free the pending slots, keep the forged counter
	b.adminSQL(fmt.Sprintf("update tables.bases set attachment_bytes = %d where id = '%s'", 100<<20-50, s.base))
	r := s.stage(o, "over.png", pngN(100))
	if r.status != 409 || !strings.Contains(string(r.body), "storage limit (100 MB on the Free plan)") {
		t.Errorf("over the cap: %d %s", r.status, r.body)
	}
	if got := s.counter(); got != fmt.Sprint(100<<20-50) {
		t.Errorf("a refused reservation moved the counter: %s", got)
	}
	// The shared file.upload list is exactly what it was: images and PDF accepted, everything else refused.
	m := b.addMember(o, b.signup("Mia", false), "member")
	note := b.claim(m, 10_000)
	for _, c := range []struct {
		name string
		data []byte
		want int
	}{{"a.pdf", pdfBytes, 200}, {"a.png", png, 200}, {"a.zip", []byte("PK\x03\x04x"), 400}, {"a.csv", []byte("a,b\n1,2\n"), 400}, {"a.svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), 400}} {
		if r := b.upload(m, o.ws, note, c.name, c.data, "application/octet-stream"); r.status != c.want {
			t.Errorf("file.upload %s: %d want %d (%s)", c.name, r.status, c.want, r.body)
		}
	}
}

func TestRecordFilesCounterStaysExactUnderConcurrency(t *testing.T) {
	b := newBT(t, false)
	s := newTFS(b)
	o := b.owner
	var wg sync.WaitGroup
	var mu sync.Mutex
	bad := []string{}
	note := func(f string, a ...any) { mu.Lock(); bad = append(bad, fmt.Sprintf(f, a...)); mu.Unlock() }
	stop := make(chan struct{})
	// purge and sweep run the whole time
	bg := sync.WaitGroup{}
	bg.Add(1)
	go func() {
		defer bg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := files.RecordSweep(context.Background(), b.pool); err != nil {
				note("sweep: %v", err)
			}
			if err := tables.PurgeSweep(context.Background(), b.pool); err != nil {
				note("purge: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 6; i++ {
				r := s.stage(o, "c.png", pngN(g*100+i))
				if r.status != 200 {
					if r.status >= 500 || r.status == 409 && g == 0 { // 409 is fine for the pending cap of the shared owner
						note("stage %d: %s", r.status, r.body)
					}
					continue
				}
				h := r.json()["handle"].(string)
				switch i % 3 {
				case 0: // attach, then detach
					rc := b.call(o, "record.create", map[string]any{"table_id": s.table, "values": map[string]any{}, "files": s.files(h)})
					if rc.status != 200 {
						note("create %d %s", rc.status, rc.body)
						continue
					}
					id := rc.json()["id"].(string)
					if u := b.call(o, "record.update", map[string]any{"id": id, "files": map[string]any{s.field: map[string]any{"remove": []string{h}}}}); u.status != 200 {
						note("remove %d %s", u.status, u.body)
					}
				case 1: // attach, then trash the record and purge it
					rc := b.call(o, "record.create", map[string]any{"table_id": s.table, "values": map[string]any{}, "files": s.files(h)})
					if rc.status != 200 {
						note("create %d %s", rc.status, rc.body)
						continue
					}
					id := rc.json()["id"].(string)
					if d := b.call(o, "record.delete", map[string]any{"ids": []string{id}}); d.status != 200 {
						note("delete %d %s", d.status, d.body)
					}
					b.adminSQL("update tables.records set deleted_at = now() - interval '31 days' where id = '" + id + "'")
				default: // never attached
					if d := b.deleteHandle(o, s.ws, h); d.status != 200 {
						note("discard %d %s", d.status, d.body)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	close(stop)
	bg.Wait()
	if err := tables.PurgeSweep(context.Background(), b.pool); err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Fatalf("failures: %v", bad)
	}
	if s.counter() != s.rowsSum() {
		t.Fatalf("counter %s but rows add up to %s", s.counter(), s.rowsSum())
	}
}

// ---------- object lifecycle (S3) ----------

type memS3 struct {
	mu                 sync.Mutex
	objs               map[string][]byte
	failPut, failDel   bool
	deletes, putsTotal int
}

func (m *memS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	m.mu.Lock()
	defer m.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		if m.failPut {
			http.Error(w, "boom", 500)
			return
		}
		m.putsTotal++
		m.objs[r.URL.Path] = body
	case http.MethodGet:
		if o, ok := m.objs[r.URL.Path]; ok {
			_, _ = w.Write(o)
		} else {
			http.NotFound(w, r)
		}
	case http.MethodDelete:
		if m.failDel {
			http.Error(w, "boom", 500)
			return
		}
		m.deletes++
		delete(m.objs, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (m *memS3) has(ws, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs["/bkt/"+ws+"/"+id]
	return ok
}

func useMemS3(t *testing.T) *memS3 {
	m := &memS3{objs: map[string][]byte{}}
	srv := httptest.NewServer(m)
	files.UseS3(&files.S3{Endpoint: srv.URL, Bucket: "bkt", Region: "r", KeyID: "k", Secret: "s"})
	t.Cleanup(func() { files.UseS3(nil); srv.Close() })
	return m
}

func (b *bt) sweep() {
	b.t.Helper()
	if err := files.RecordSweep(context.Background(), b.pool); err != nil {
		b.t.Fatal(err)
	}
}

func TestRecordFilesObjectLifecycleAndGC(t *testing.T) {
	m := useMemS3(t)
	b := newBT(t, false)
	s := newTFS(b)
	o := b.owner

	// stage: intent first, live when attached
	h := s.handle(o, "a.png", pngN(500))
	if !m.has(s.ws, h) {
		t.Fatal("object not written")
	}
	eq(t, b.dbStrings(s.ws, "select state from core.file_objects where key = $1", s.ws+"/"+h), []string{"intent"})
	rec := s.newRecord(o, h)
	recID := rec["id"].(string)
	eq(t, b.dbStrings(s.ws, "select state from core.file_objects where key = $1", s.ws+"/"+h), []string{"live"})
	if dl, _ := b.download(o, s.ws, recID, h); dl.status != 200 || !bytes.Equal(dl.body, pngN(500)) {
		t.Fatalf("download from the bucket: %d", dl.status)
	}

	// GC never deletes a referenced key: a stray queue entry for a live row is dropped, the object stays
	b.adminSQL(fmt.Sprintf("insert into core.file_gc (workspace_id, object_key) values ('%s', '%s/%s')", s.ws, s.ws, h))
	b.sweep()
	if !m.has(s.ws, h) {
		t.Fatal("GC deleted the object of a live row")
	}
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_gc where object_key = $1", s.ws+"/"+h), []string{"0"})
	eq(t, b.dbStrings(s.ws, "select state from core.file_objects where key = $1", s.ws+"/"+h), []string{"live"})

	// detach: the row goes at once, the object stays until the sweep, and a failing bucket keeps the entry and counts tries
	b.ok("record.update", map[string]any{"id": recID, "files": map[string]any{s.field: map[string]any{"remove": []string{h}}}})
	if !m.has(s.ws, h) {
		t.Fatal("object deleted inside the command")
	}
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_gc where object_key = $1", s.ws+"/"+h), []string{"1"})
	m.mu.Lock()
	m.failDel = true
	m.mu.Unlock()
	b.sweep()
	eq(t, b.dbStrings(s.ws, "select tries::text || ':' || (last_error is not null)::text from core.file_gc where object_key = $1", s.ws+"/"+h), []string{"1:true"})
	if !m.has(s.ws, h) {
		t.Fatal("object gone although the bucket failed")
	}
	m.mu.Lock()
	m.failDel = false
	m.mu.Unlock()
	b.adminSQL("update core.file_gc set next_try_at = now() - interval '1 minute'")
	b.sweep()
	if m.has(s.ws, h) {
		t.Fatal("object not deleted after the bucket recovered")
	}
	eq(t, b.dbStrings(s.ws, "select (select count(*) from core.file_gc where object_key = $1)::text || ':' || (select count(*) from core.file_objects where key = $1)::text", s.ws+"/"+h), []string{"0:0"})
	eq(t, s.counter(), "0")

	// a stage that rolls back after the object was announced (the bucket refuses the write) leaves an intent and nothing else
	m.mu.Lock()
	m.failPut = true
	m.mu.Unlock()
	if r := s.stage(o, "lost.png", png); r.status < 500 {
		t.Fatalf("stage with a failing bucket: %d", r.status)
	}
	m.mu.Lock()
	m.failPut = false
	m.mu.Unlock()
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.attachments where field_id is not null"), []string{"0"})
	eq(t, s.counter(), "0")
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_objects where state='intent'"), []string{"1"})
	b.sweep() // too young: kept
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_objects where state='intent'"), []string{"1"})
	// 24 hours later (the clock is the created_at column): collected, and its object, had it been written, deleted
	m.mu.Lock()
	var strayKey string
	m.mu.Unlock()
	strayKey = b.dbStrings(s.ws, "select split_part(key, '/', 2) from core.file_objects where state='intent'")[0]
	m.mu.Lock()
	m.objs["/bkt/"+s.ws+"/"+strayKey] = []byte("orphan from a failed commit")
	m.mu.Unlock()
	b.adminSQL("update core.file_objects set created_at = now() - interval '25 hours' where state='intent'")
	b.sweep()
	b.sweep()
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_objects"), []string{"0"})
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_gc"), []string{"0"})
	if m.has(s.ws, strayKey) {
		t.Fatal("orphan object of an expired intent was kept")
	}

	// a pending upload nobody attached: swept after 24 hours, bytes released, object deleted
	hp := s.handle(o, "p.png", pngN(300))
	if s.counter() != fmt.Sprint(len(png)+300) {
		t.Fatalf("pending bytes count: %s", s.counter())
	}
	b.adminSQL("update core.attachments set created_at = now() - interval '25 hours' where status = 'pending'")
	if r := b.call(o, "record.create", map[string]any{"table_id": s.table, "values": map[string]any{}, "files": s.files(hp)}); r.status != 400 {
		t.Errorf("an expired handle attached: %d", r.status)
	}
	b.sweep()
	eq(t, s.counter(), "0")
	if m.has(s.ws, hp) {
		t.Fatal("object of an expired pending upload kept")
	}

	// purge: a record trashed for 30 days takes its files with it; the counter follows in the same transaction
	h3 := s.handle(o, "z.png", pngN(100))
	r3 := s.newRecord(o, h3)["id"].(string)
	b.ok("record.delete", map[string]any{"ids": []string{r3}})
	eq(t, s.counter(), fmt.Sprint(len(png)+100)) // trashed files stay counted
	b.adminSQL("update tables.records set deleted_at = now() - interval '31 days' where id = '" + r3 + "'")
	if err := tables.PurgeSweep(context.Background(), b.pool); err != nil {
		t.Fatal(err)
	}
	eq(t, s.counter(), "0")
	b.sweep()
	if m.has(s.ws, h3) {
		t.Fatal("object of a purged record kept")
	}
}

func TestRecordFilesObjectsOfDeletedWorkspaceAreCollected(t *testing.T) {
	m := useMemS3(t)
	b := newBT(t, false)
	s := newTFS(b)
	h := s.handle(b.owner, "a.png", png)
	s.newRecord(b.owner, h)
	h2 := s.handle(b.owner, "b.png", png) // pending when the workspace goes
	adminURL := os.Getenv("DATABASE_ADMIN_URL")
	admin, err := db.Connect(context.Background(), adminURL, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// There is no workspace hard delete yet, and the audit trail is append-only: lift that trigger for this one delete.
	tx, err := admin.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"alter table core.events disable trigger events_append_only", "delete from core.workspaces where id = '" + s.ws + "'", "alter table core.events enable trigger events_append_only"} {
		if _, err := tx.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	var queued int
	_ = admin.QueryRow(context.Background(), "select count(*) from core.file_gc where workspace_id = $1::uuid", s.ws).Scan(&queued)
	if queued != 2 {
		t.Fatalf("queued after the workspace was deleted: %d", queued)
	}
	b.sweep()
	if m.has(s.ws, h) || m.has(s.ws, h2) {
		t.Fatal("objects of a deleted workspace were kept")
	}
	_ = admin.QueryRow(context.Background(), "select count(*) from core.file_gc where workspace_id = $1::uuid", s.ws).Scan(&queued)
	if queued != 0 {
		t.Fatalf("queue not drained: %d", queued)
	}
}

func TestRecordFilesRacingSweepsDeleteEachObjectOnce(t *testing.T) {
	// two sweeps racing on one queue must not delete twice or fail (SKIP LOCKED)
	m := useMemS3(t)
	b := newBT(t, false)
	s := newTFS(b)
	var hs []string
	for i := 0; i < 8; i++ {
		h := s.handle(b.owner, "a.png", pngN(i))
		hs = append(hs, h)
	}
	rec := s.newRecord(b.owner, hs...)
	_ = rec
	b.ok("record.update", map[string]any{"id": rec["id"], "files": map[string]any{s.field: map[string]any{"remove": hs}}})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); b.sweep() }()
	}
	wg.Wait()
	for _, h := range hs {
		if m.has(s.ws, h) {
			t.Errorf("object %s kept", h)
		}
	}
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_gc"), []string{"0"})
}

// A stage that stalls across the 24 hour threshold holds its intent FOR UPDATE; the sweep must skip it.
func TestRecordSweepSkipsIntentHeldByAStage(t *testing.T) {
	useMemS3(t)
	b := newBT(t, false)
	s := newTFS(b)
	ctx := context.Background()
	key := s.ws + "/" + "11111111-1111-4111-8111-111111111111"
	b.adminSQL(fmt.Sprintf("insert into core.file_objects (key, workspace_id, state) values ('%s', '%s', 'intent')", key, s.ws))
	b.adminSQL("update core.file_objects set created_at = now() - interval '25 hours' where state='intent'")
	tx, err := db.BeginTenant(ctx, b.pool, s.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var one int
	if err := tx.QueryRow(ctx, `select 1 from core.file_objects where key=$1 and state='intent' for update`, key).Scan(&one); err != nil {
		t.Fatal(err)
	}
	b.sweep()
	b.sweep()
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_objects where state='intent'"), []string{"1"})
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_gc"), []string{"0"})
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	b.sweep() // the stage is done and attached nothing: now it is collected
	eq(t, b.dbStrings(s.ws, "select count(*)::text from core.file_objects"), []string{"0"})
}
