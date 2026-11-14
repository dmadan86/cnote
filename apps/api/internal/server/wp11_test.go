package server_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// WP11: quote decision notifications and file attachments.

func (p *pl) notified(w who) []string {
	return p.dbStrings(p.owner.ws, "select title from core.notifications where kind = 'quote_decided' and user_id = $1::uuid and note_id = $2::uuid order by created_at", w.userID, p.quoteID)
}

func TestQuoteDecisionNotifiesOwnerAndAdminsOnce(t *testing.T) {
	p := newPL(t)
	sink := startSMTP(t)
	t.Setenv("SMTP_URL", "smtp://"+sink.ln.Addr().String())
	t.Setenv("EMAIL_FROM", "billing@acme.example.test")
	adm := p.addMember(p.owner, p.signup("Ada", false), "admin")
	mem := p.addMember(p.owner, p.signup("Mia", false), "member")
	tok := p.share(p.quoteID, nil)["token"].(string)
	eq(t, p.decide(tok, "accept", "Ravi Kumar").status, 200)
	for _, w := range []who{p.owner, adm} {
		got := p.notified(w)
		if len(got) != 1 || !strings.HasPrefix(got[0], "Ravi Kumar accepted quote QT/") {
			t.Fatalf("notifications %v", got)
		}
		eq(t, p.notifs(w)[0][:13], "quote_decided")
	}
	eq(t, len(p.notified(mem)), 0) // an ordinary member who does not own the quote hears nothing
	// replay and the opposite decision add nothing
	eq(t, p.decide(tok, "accept", "Ravi Kumar").status, 200)
	eq(t, p.decide(tok, "decline", "Ravi Kumar").status, 409)
	eq(t, p.dbStrings(p.owner.ws, "select count(*)::text from core.notifications where kind = 'quote_decided'"), []string{"2"})
	sink.mu.Lock()
	n := len(sink.msgs)
	first := ""
	if n > 0 {
		first = sink.msgs[0]
	}
	sink.mu.Unlock()
	if n != 2 || !strings.Contains(first, "Ravi Kumar accepted quote QT/") || !strings.Contains(first, "/w/"+p.owner.ws+"/n/"+p.quoteID) {
		t.Fatalf("emails %d %q", n, first)
	}
}

func TestQuoteDeclineNotifiesAndNoMailWithoutSMTP(t *testing.T) {
	p := newPL(t)
	tok := p.share(p.quoteID, nil)["token"].(string)
	eq(t, p.decide(tok, "decline", "Asha").status, 200) // SMTP unset: the decision still succeeds
	got := p.notified(p.owner)
	if len(got) != 1 || !strings.HasPrefix(got[0], "Asha declined quote QT/") {
		t.Fatalf("%v", got)
	}
	// a staff decision is not a customer decision: no notification
	q2 := p.quote(p.contact, "")
	p.q("quote.send", q2)
	p.q("quote.accept", q2)
	eq(t, p.dbStrings(p.owner.ws, "select count(*)::text from core.notifications where kind = 'quote_decided'"), []string{"1"})
}

// ---------- files ----------

var png = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00")

func (b *bt) upload(w who, ws, note, name string, data []byte, declared string) resp {
	b.t.Helper()
	req, _ := http.NewRequest("POST", b.gosv.URL+"/api/w/"+ws+"/files?note_id="+note+"&filename="+url.QueryEscape(name), bytes.NewReader(data))
	req.Header.Set("Content-Type", declared)
	w.with(req.Header)
	return b.send(req)
}

func (b *bt) fetch(w who, ws, id string) (resp, http.Header) {
	b.t.Helper()
	req, _ := http.NewRequest("GET", b.gosv.URL+"/api/w/"+ws+"/files/"+id, nil)
	w.with(req.Header)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, body}, res.Header
}

func (b *bt) send(req *http.Request) resp {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, body}
}

func TestFileUploadLimitsAndContentSniffing(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	id := b.claim(m, 10_000)
	ws := b.owner.ws
	// the type comes from the bytes: a PNG labelled .exe / application/pdf is stored as image/png
	r := b.upload(m, ws, id, "../../evil.exe", png, "application/pdf")
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	f := r.json()
	eq(t, f["content_type"], "image/png")
	eq(t, f["filename"], "evil.exe")
	eq(t, f["size_bytes"], float64(len(png)))
	got, h := b.fetch(m, ws, f["id"].(string))
	if got.status != 200 || !bytes.Equal(got.body, png) || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download %d %v", got.status, h)
	}
	pdf := []byte("%PDF-1.4\n1 0 obj<<>>endobj\n")
	eq(t, b.upload(m, ws, id, "r.pdf", pdf, "application/octet-stream").json()["content_type"], "application/pdf")
	// disallowed or lying content, whatever the header says
	for name, data := range map[string][]byte{"html": []byte("<html><script>alert(1)</script>"), "text": []byte("hello"), "exe": []byte("MZ\x90\x00\x03"),
		"svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), "empty": {}} {
		if r := b.upload(m, ws, id, name+".png", data, "image/png"); r.status != 400 {
			t.Fatalf("%s accepted: %d %s", name, r.status, r.body)
		}
	}
	// size: exactly 10 MB passes, one byte more is refused
	big := append(append([]byte{}, png...), make([]byte, 10<<20-len(png))...)
	if r := b.upload(m, ws, id, "big.png", big, "image/png"); r.status != 200 {
		t.Fatalf("10 MB: %d %s", r.status, r.body)
	}
	if r := b.upload(m, ws, id, "big.png", append(big, 0), "image/png"); r.status != 400 || !strings.Contains(string(r.body), "10 MB") {
		t.Fatalf("10 MB + 1: %d %s", r.status, r.body)
	}
	eq(t, len(b.okAsList(m, "file.list", map[string]any{"note_id": id})), 3)
	// input errors
	b.errAs(m, "file.upload", map[string]any{"note_id": "nope", "filename": "x", "data_base64": "AA=="}, 400, "invalid")
	b.errAs(m, "file.upload", map[string]any{"note_id": id, "filename": "x", "data_base64": "%%%"}, 400, "invalid")
	b.errAs(m, "file.download", map[string]any{}, 400, "invalid")
	// at most 10 files per page
	for i := 0; i < 7; i++ {
		eq(t, b.upload(m, ws, id, "p.png", png, "image/png").status, 200)
	}
	eq(t, b.upload(m, ws, id, "p.png", png, "image/png").status, 409)
	// delete removes the bytes
	b.okAs(m, "file.delete", map[string]any{"id": f["id"]})
	if got, _ := b.fetch(m, ws, f["id"].(string)); got.status != 404 {
		t.Fatalf("deleted file: %d", got.status)
	}
}

func (b *bt) okAsList(w who, cmd string, in any) []map[string]any {
	b.t.Helper()
	r := b.call(w, cmd, in)
	if r.status != 200 {
		b.t.Fatalf("%s: %d %s", cmd, r.status, r.body)
	}
	return r.jsonList()
}

func TestFilesAreWorkspaceScoped(t *testing.T) {
	b := newBT(t, false)
	other := b.env.tenant(false)
	id := b.claim(b.owner, 10_000)
	f := b.upload(b.owner, b.owner.ws, id, "r.png", png, "image/png").json()
	fid := f["id"].(string)
	// the other workspace cannot read, list, delete or attach, with its own or my workspace id in the path
	for _, ws := range []string{other.owner.ws, b.owner.ws} {
		if got, _ := b.fetch(other.owner, ws, fid); got.status == 200 {
			t.Fatalf("foreign download via %s", ws)
		}
		if r := b.upload(other.owner, ws, id, "x.png", png, "image/png"); r.status == 200 {
			t.Fatalf("foreign upload via %s", ws)
		}
	}
	other.errIs("file.list", map[string]any{"note_id": id}, 404, "not_found")
	other.errIs("file.delete", map[string]any{"id": fid}, 404, "not_found")
	other.errIs("file.download", map[string]any{"id": fid}, 404, "not_found")
	eq(t, other.dbStrings(other.owner.ws, "select count(*)::text from core.attachments where id = $1::uuid", fid), []string{"0"}) // RLS
	// no session at all
	if got, _ := b.fetch(who{}, b.owner.ws, fid); got.status != 401 {
		t.Fatalf("anonymous: %d", got.status)
	}
	// a composite foreign key stops a row for another workspace's page
	if err := other.raw(fmt.Sprintf("insert into core.attachments (workspace_id, note_id, filename, content_type, size_bytes, storage_key) values ('%s','%s','x','image/png',1,'pg')", other.owner.ws, id)); err == nil {
		t.Fatal("cross-tenant attachment accepted")
	}
}

func TestFileFinanceGateAndClaimLock(t *testing.T) {
	b := newBT(t, false)
	m := b.addMember(b.owner, b.signup("Mia", false), "member")
	o := b.addMember(b.owner, b.signup("Other", false), "member")
	adm := b.addMember(b.owner, b.signup("Ada", false), "admin")
	ws := b.owner.ws
	pl := func(w who, note string) int { return b.upload(w, ws, note, "r.png", png, "image/png").status }

	// an invoice is a finance document: needs finance access
	inv := b.draft(b.contact, 0)
	eq(t, pl(b.owner, inv), 200)
	eq(t, pl(m, inv), 200) // finance_access is on by default for members
	b.ok("member.update", map[string]any{"user_id": m.userID, "finance_access": false})
	b.errAs(m, "file.list", map[string]any{"note_id": inv}, 403, "forbidden")
	eq(t, pl(m, inv), 403)
	fid := b.okAsList(b.owner, "file.list", map[string]any{"note_id": inv})[0]["id"].(string)
	b.errAs(m, "file.download", map[string]any{"id": fid}, 403, "forbidden")
	b.okAs(adm, "file.list", map[string]any{"note_id": inv}) // admins always have finance access

	// an expense claim: its claimant (even without finance access), admins; nobody else
	cl := b.claim(m, 10_000)
	eq(t, pl(m, cl), 200)
	eq(t, pl(o, cl), 404)
	b.errAs(o, "file.list", map[string]any{"note_id": cl}, 404, "not_found")
	eq(t, pl(adm, cl), 200)
	// locked once submitted, open again when rejected
	b.eid("expense.submit", m, cl)
	eq(t, pl(m, cl), 409)
	b.errAs(m, "file.delete", map[string]any{"id": b.okAsList(m, "file.list", map[string]any{"note_id": cl})[0]["id"]}, 409, "conflict")
	if got := len(b.okAsList(adm, "file.list", map[string]any{"note_id": cl})); got != 2 { // the reviewer sees the receipts
		t.Fatalf("receipts %d", got)
	}
	b.ok("expense.reject", map[string]any{"id": cl, "reason": "blurry"})
	eq(t, pl(m, cl), 200)

	// an ordinary page needs no finance access
	page := b.ok("note.create", map[string]any{"space_id": b.space, "title": "Plain"})["id"].(string)
	eq(t, pl(m, page), 200)
}
