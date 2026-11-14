package server_test

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/cnote/api/internal/auth"
	"github.com/cnote/api/internal/db"
)

// WP23 SAML against a test identity provider: an RSA key and a self-signed certificate generated per run, assertions built
// and signed here with goxmldsig. Nothing talks to a real IdP.

type samlIdP struct {
	key      *rsa.PrivateKey
	der      []byte
	entityID string
	ssoURL   string
}

func newSAMLIdP(t *testing.T) *samlIdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "test idp"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return &samlIdP{key: k, der: der, entityID: "https://idp-" + rnd() + ".example.test/entity", ssoURL: "https://idp.example.test/sso"}
}

func (i *samlIdP) metadata() string {
	return fmt.Sprintf(`<?xml version="1.0"?><EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="%s"><IDPSSODescriptor protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
<KeyDescriptor use="signing"><KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#"><X509Data><X509Certificate>%s</X509Certificate></X509Data></KeyInfo></KeyDescriptor>
<SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="%s"/></IDPSSODescriptor></EntityDescriptor>`,
		i.entityID, base64.StdEncoding.EncodeToString(i.der), i.ssoURL)
}

// samlReq is what the IdP learns from the redirect.
type samlReq struct{ ID, ACS, SPEntity, Nonce string }

type samlOpts struct {
	sign        string // assertion, response, both, none
	signer      *samlIdP
	audience    string // "" = the service provider's entity id
	recipient   string
	destination string
	inResponse  string
	notBefore   time.Time
	notAfter    time.Time
	assertionID string
	email       string
	nameID      string
	nameFormat  string
	noEmailAttr bool
	mutate      func(doc *etree.Document, assertion *etree.Element)
}

func (i *samlIdP) response(t *testing.T, rq samlReq, o samlOpts) string {
	t.Helper()
	signer := o.signer
	if signer == nil {
		signer = i
	}
	now := time.Now().UTC()
	def := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	ts := func(v time.Time, d time.Time) string {
		if v.IsZero() {
			v = d
		}
		return v.UTC().Format(time.RFC3339)
	}
	aid := def(o.assertionID, "_a"+rnd())
	nameFormat := def(o.nameFormat, "urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified")
	attrs := ""
	if !o.noEmailAttr {
		attrs = fmt.Sprintf(`<saml:AttributeStatement><saml:Attribute Name="email"><saml:AttributeValue>%s</saml:AttributeValue></saml:Attribute><saml:Attribute Name="displayName"><saml:AttributeValue>Saml Person</saml:AttributeValue></saml:Attribute></saml:AttributeStatement>`, o.email)
	}
	raw := fmt.Sprintf(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_r%s" Version="2.0" IssueInstant="%s" Destination="%s" InResponseTo="%s">
<saml:Issuer>%s</saml:Issuer><samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></samlp:Status>
<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="%s" Version="2.0" IssueInstant="%s"><saml:Issuer>%s</saml:Issuer>
<saml:Subject><saml:NameID Format="%s">%s</saml:NameID><saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData NotOnOrAfter="%s" Recipient="%s" InResponseTo="%s"/></saml:SubjectConfirmation></saml:Subject>
<saml:Conditions NotBefore="%s" NotOnOrAfter="%s"><saml:AudienceRestriction><saml:Audience>%s</saml:Audience></saml:AudienceRestriction></saml:Conditions>
<saml:AuthnStatement AuthnInstant="%s"><saml:AuthnContext><saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport</saml:AuthnContextClassRef></saml:AuthnContext></saml:AuthnStatement>%s</saml:Assertion></samlp:Response>`,
		rnd(), ts(now, now), def(o.destination, rq.ACS), def(o.inResponse, rq.ID),
		i.entityID, aid, ts(now, now), i.entityID,
		nameFormat, def(o.nameID, "nid-"+o.email), ts(o.notAfter, now.Add(5*time.Minute)), def(o.recipient, rq.ACS), def(o.inResponse, rq.ID),
		ts(o.notBefore, now.Add(-time.Minute)), ts(o.notAfter, now.Add(5*time.Minute)), def(o.audience, rq.SPEntity),
		ts(now, now), attrs)
	doc := etree.NewDocument()
	if err := doc.ReadFromString(raw); err != nil {
		t.Fatal(err)
	}
	ks := dsig.TLSCertKeyStore(tls.Certificate{Certificate: [][]byte{signer.der}, PrivateKey: signer.key})
	sctx := dsig.NewDefaultSigningContext(ks)
	sctx.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	asrt := doc.Root().FindElement("./Assertion")
	if o.sign == "assertion" || o.sign == "both" {
		signed, err := sctx.SignEnveloped(asrt)
		if err != nil {
			t.Fatal(err)
		}
		idx := asrt.Index()
		doc.Root().RemoveChildAt(idx)
		doc.Root().InsertChildAt(idx, signed)
		asrt = signed
	}
	if o.mutate != nil {
		o.mutate(doc, asrt)
	}
	if o.sign == "response" || o.sign == "both" {
		signed, err := sctx.SignEnveloped(doc.Root())
		if err != nil {
			t.Fatal(err)
		}
		doc.SetRoot(signed)
	}
	out, err := doc.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(out)
}

// samlStart runs the browser's first leg and reads the AuthnRequest the IdP would receive.
func (e *env) samlStart(slug string) (samlReq, *http.Response) {
	e.t.Helper()
	res := e.get("/api/auth/saml/start?workspace=" + url.QueryEscape(slug))
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != 302 || loc == nil || loc.Query().Get("SAMLRequest") == "" {
		return samlReq{}, res
	}
	raw, err := base64.StdEncoding.DecodeString(loc.Query().Get("SAMLRequest"))
	if err != nil {
		e.t.Fatal(err)
	}
	xmlb, _ := io.ReadAll(flate.NewReader(bytes.NewReader(raw)))
	var ar struct {
		ID     string `xml:"ID,attr"`
		ACS    string `xml:"AssertionConsumerServiceURL,attr"`
		Issuer string `xml:"Issuer"`
		Dest   string `xml:"Destination,attr"`
	}
	if err := xml.Unmarshal(xmlb, &ar); err != nil || ar.ID == "" || ar.Dest != loc.Scheme+"://"+loc.Host+loc.Path {
		e.t.Fatalf("authn request: %v %s", err, xmlb)
	}
	return samlReq{ID: ar.ID, ACS: ar.ACS, SPEntity: ar.Issuer, Nonce: cookieOf(res, "cn_saml")}, res
}

func (e *env) samlPost(rq samlReq, resp string) *http.Response {
	e.t.Helper()
	return e.samlPostWith(rq, resp, rq.Nonce)
}

// samlPostWith is the IdP's form POST arriving from a browser holding this nonce cookie ("" = none).
func (e *env) samlPostWith(rq samlReq, resp, nonce string) *http.Response {
	e.t.Helper()
	u, _ := url.Parse(rq.ACS)
	req, _ := http.NewRequest("POST", e.gosv.URL+u.Path, strings.NewReader(url.Values{"SAMLResponse": {resp}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-For", fakeIP())
	if nonce != "" {
		req.Header.Set("Cookie", "cn_saml="+nonce)
	}
	res, err := noFollow.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	res.Body.Close()
	return res
}

// samlWorkspace: an owner, a test IdP, SAML switched on for corp.test (verified) with other.test added but not verified.
func samlWorkspace(t *testing.T) (*env, *samlIdP, acct, string, string) {
	t.Helper()
	e := sec(t)
	dns := fakeDNS(t)
	idp := newSAMLIdP(t)
	owner := e.newAcct("SamlOwner", true)
	ck := e.session(owner)
	e.verified(dns, ck, owner.ws, "corp.test")
	if r := e.run(ck, owner.ws, "security.domain_add", map[string]any{"domain": "other.test"}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r := e.run(ck, owner.ws, "security.update", map[string]any{"sso": map[string]any{"enabled": true, "protocol": "saml", "saml_metadata": idp.metadata()}})
	if r.status != 200 {
		t.Fatalf("configure: %d %s", r.status, r.body)
	}
	var w struct{ Slug string }
	_ = json.Unmarshal(e.run(ck, owner.ws, "workspace.get", map[string]any{}).body, &w)
	return e, idp, owner, ck, w.Slug
}

func samlOK(t *testing.T, res *http.Response) string {
	t.Helper()
	ck := cookieOf(res, "cn_session")
	if res.StatusCode != 303 || res.Header.Get("Location") != "/" || ck == "" {
		t.Fatalf("ACS: %d %s %q", res.StatusCode, res.Header.Get("Location"), loginError(res))
	}
	return ck
}

func TestSAMLHappyPathLinksByNameIDAndPinsTheSession(t *testing.T) {
	e, idp, owner, ownerCk, slug := samlWorkspace(t)
	for _, sign := range []string{"assertion", "response", "both"} {
		email := "sam-" + rnd() + "@corp.test"
		rq, _ := e.samlStart(slug)
		ck := samlOK(t, e.samlPost(rq, idp.response(t, rq, samlOpts{sign: sign, email: email})))
		if r := e.run(ck, owner.ws, "workspace.get", map[string]any{}); r.status != 200 {
			t.Fatalf("%s: session in its workspace: %d %s", sign, r.status, r.body)
		}
		var amr, ws string
		_ = e.pool.QueryRow(context.Background(), "select amr, sso_workspace_id::text from core.sessions where id = $1", sessHash(ck)).Scan(&amr, &ws)
		if amr != "sso" || ws != owner.ws {
			t.Fatalf("%s: amr=%s pinned=%s", sign, amr, ws)
		}
		if n := e.tcount(owner.ws, "select count(*) from core.sso_identities si join core.users u on u.id = si.user_id where si.issuer = $1 and si.sub = $2 and u.email = $3",
			"saml:"+idp.entityID, "nid-"+email, email); n != 1 {
			t.Fatalf("%s: identity (entity id, NameID) must be linked", sign)
		}
	}
	// the NameID, not the email, is the identity: a changed email follows the link; the same NameID twice is one person
	nid, first := "stable-"+rnd(), "one-"+rnd()+"@corp.test"
	rq, _ := e.samlStart(slug)
	samlOK(t, e.samlPost(rq, idp.response(t, rq, samlOpts{sign: "assertion", email: first, nameID: nid})))
	rq, _ = e.samlStart(slug)
	samlOK(t, e.samlPost(rq, idp.response(t, rq, samlOpts{sign: "assertion", email: "renamed-" + rnd() + "@corp.test", nameID: nid})))
	if users := e.tcount(owner.ws, "select count(*) from core.sso_identities where sub = $1", nid); users != 1 {
		t.Fatalf("one NameID, one identity: %d", users)
	}
	list := e.run(ownerCk, owner.ws, "member.list", map[string]any{})
	if !strings.Contains(string(list.body), first) || strings.Contains(string(list.body), "renamed-") {
		t.Fatalf("member.list: %s", list.body)
	}
	if e.events(owner.ws, "auth.sso_login") != 5 {
		t.Fatal("SAML sign-ins are audited like OIDC ones")
	}
	// a different NameID claiming a linked person's email is refused
	rq, _ = e.samlStart(slug)
	res := e.samlPost(rq, idp.response(t, rq, samlOpts{sign: "assertion", email: first, nameID: "someone-else-" + rnd()}))
	if cookieOf(res, "cn_session") != "" || !strings.Contains(loginError(res), "already linked") {
		t.Fatalf("hijack by email: %d %q", res.StatusCode, loginError(res))
	}
}

func sessHash(tok string) string { return fmt.Sprintf("%x", sum(tok)) }

func TestSAMLRefusesBadResponses(t *testing.T) {
	e, idp, owner, _, slug := samlWorkspace(t)
	var why string
	auth.OnSAMLReject = func(r string) { why = r }
	t.Cleanup(func() { auth.OnSAMLReject = nil })
	other := newSAMLIdP(t) // a stranger's key and certificate
	now := time.Now()
	good := func() samlOpts { return samlOpts{sign: "assertion", email: "ok-" + rnd() + "@corp.test"} }
	with := func(f func(*samlOpts)) samlOpts { o := good(); f(&o); return o }
	// a valid signed assertion to steal from
	stolen := func(rq samlReq) *etree.Element {
		raw, _ := base64.StdEncoding.DecodeString(idp.response(t, rq, samlOpts{sign: "assertion", email: "victim-" + rnd() + "@corp.test"}))
		d := etree.NewDocument()
		_ = d.ReadFromBytes(raw)
		return d.Root().FindElement("./Assertion")
	}
	evil := "evil-" + rnd() + "@corp.test"
	for _, tc := range []struct {
		name string
		o    func(rq samlReq) samlOpts
		want string // the private reason
		pub  string // text in the message the browser gets
	}{
		{"unsigned", func(samlReq) samlOpts { return with(func(o *samlOpts) { o.sign = "none" }) }, "signature", ""},
		{"signed by another key", func(samlReq) samlOpts { return with(func(o *samlOpts) { o.signer = other }) }, "signature", ""},
		{"wrong audience", func(samlReq) samlOpts {
			return with(func(o *samlOpts) { o.audience = "https://someone-else.example.test/sp" })
		}, "AudienceRestriction", ""},
		{"wrong recipient", func(samlReq) samlOpts {
			return with(func(o *samlOpts) { o.recipient = "https://evil.example.test/acs" })
		}, "Recipient", ""},
		{"wrong destination (signed response)", func(samlReq) samlOpts {
			return with(func(o *samlOpts) { o.sign, o.destination = "response", "https://evil.example.test/acs" })
		}, "Destination", ""},
		{"expired", func(samlReq) samlOpts {
			return with(func(o *samlOpts) { o.notBefore, o.notAfter = now.Add(-time.Hour), now.Add(-10*time.Minute) })
		}, "expired", ""},
		{"not yet valid", func(samlReq) samlOpts { return with(func(o *samlOpts) { o.notBefore = now.Add(10 * time.Minute) }) }, "not yet valid", ""},
		{"InResponseTo of another request", func(samlReq) samlOpts { return with(func(o *samlOpts) { o.inResponse = "id-not-mine" }) }, "InResponseTo", "expired"},
		{"transient NameID", func(samlReq) samlOpts {
			return with(func(o *samlOpts) { o.nameFormat = "urn:oasis:names:tc:SAML:2.0:nameid-format:transient" })
		}, "transient", ""},
		{"domain not verified", func(samlReq) samlOpts { return with(func(o *samlOpts) { o.email = "pat-" + rnd() + "@other.test" }) }, "", "domain is not allowed"},
		{"domain not listed", func(samlReq) samlOpts { return with(func(o *samlOpts) { o.email = "mallory@evil.test" }) }, "", "domain is not allowed"},
		{"no email", func(samlReq) samlOpts { return with(func(o *samlOpts) { o.noEmailAttr, o.nameID = true, "opaque-id" }) }, "", "no email"},
		{"content changed after signing", func(samlReq) samlOpts {
			return with(func(o *samlOpts) {
				o.mutate = func(_ *etree.Document, a *etree.Element) {
					a.FindElement(".//AttributeValue").SetText(evil)
				}
			})
		}, "signature", ""},
		{"wrapping: unsigned assertion first, signed one after", func(rq samlReq) samlOpts {
			return with(func(o *samlOpts) {
				o.sign, o.email = "none", evil
				victim := stolen(rq)
				o.mutate = func(d *etree.Document, a *etree.Element) { d.Root().AddChild(victim) }
			})
		}, "assertions in the response", ""},
		{"wrapping: signed assertion hidden in Advice of an unsigned one", func(rq samlReq) samlOpts {
			return with(func(o *samlOpts) {
				o.sign, o.email = "none", evil
				victim := stolen(rq)
				o.mutate = func(_ *etree.Document, a *etree.Element) { a.CreateElement("saml:Advice").AddChild(victim) }
			})
		}, "assertions in the response", ""},
		{"wrapping: evil body with the signature copied from a good assertion", func(rq samlReq) samlOpts {
			return with(func(o *samlOpts) {
				o.sign, o.email = "none", evil
				victim := stolen(rq)
				o.mutate = func(_ *etree.Document, a *etree.Element) {
					a.CreateAttr("ID", victim.SelectAttrValue("ID", ""))
					a.AddChild(victim.FindElement("./Signature").Copy())
				}
			})
		}, "signature", ""},
		{"wrapping: signed response with a second assertion inserted", func(rq samlReq) samlOpts {
			return with(func(o *samlOpts) {
				o.sign, o.email = "none", evil
				o.mutate = func(d *etree.Document, a *etree.Element) {
					extra := a.Copy()
					extra.FindElement(".//AttributeValue").SetText(evil)
					extra.CreateAttr("ID", "_extra"+rnd())
					d.Root().AddChild(extra)
				}
			})
		}, "assertions in the response", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why = ""
			rq, _ := e.samlStart(slug)
			o := tc.o(rq)
			res := e.samlPost(rq, idp.response(t, rq, o))
			if res.StatusCode != 302 || cookieOf(res, "cn_session") != "" || !strings.HasPrefix(res.Header.Get("Location"), "/login?sso_error=") {
				t.Fatalf("must be refused: %d %q cookie=%q", res.StatusCode, res.Header.Get("Location"), cookieOf(res, "cn_session"))
			}
			if tc.want != "" && !strings.Contains(why, tc.want) {
				t.Fatalf("refused for the wrong reason: %q (want %q)", why, tc.want)
			}
			if tc.pub != "" && !strings.Contains(loginError(res), tc.pub) {
				t.Fatalf("message: %q", loginError(res))
			}
			for _, em := range []string{o.email, evil} {
				var n int
				_ = e.pool.QueryRow(context.Background(), "select count(*) from core.users where email = $1", em).Scan(&n)
				if n != 0 {
					t.Fatalf("a refused login must not create %s", em)
				}
			}
		})
	}
	if e.events(owner.ws, "auth.sso_login") != 0 {
		t.Fatal("refused logins are not sign-ins")
	}
	// the library never sees an unsolicited response: no request was started at all
	rq, _ := e.samlStart(slug)
	rq.ID = "id-never-issued"
	res := e.samlPost(rq, idp.response(t, rq, good()))
	if cookieOf(res, "cn_session") != "" || !strings.Contains(why, "no pending request") {
		t.Fatalf("unsolicited response: %q %q", why, loginError(res))
	}
}

func TestSAMLRequestAndAssertionAreSingleUse(t *testing.T) {
	e, idp, owner, _, slug := samlWorkspace(t)
	var why string
	auth.OnSAMLReject = func(r string) { why = r }
	t.Cleanup(func() { auth.OnSAMLReject = nil })
	email := "once-" + rnd() + "@corp.test"
	rq, _ := e.samlStart(slug)
	resp := idp.response(t, rq, samlOpts{sign: "assertion", email: email, assertionID: "_fixed" + rnd()})
	samlOK(t, e.samlPost(rq, resp))
	// the same POST again (browser back, or a captured response)
	if res := e.samlPost(rq, resp); cookieOf(res, "cn_session") != "" || !strings.Contains(why, "no pending request") {
		t.Fatalf("replayed response: %q %q", why, loginError(res))
	}
	// a fresh request, but the IdP-signed assertion id is reused: the assertion cache refuses it
	aid := "_reuse" + rnd()
	rq1, _ := e.samlStart(slug)
	samlOK(t, e.samlPost(rq1, idp.response(t, rq1, samlOpts{sign: "assertion", email: email, assertionID: aid})))
	rq2, _ := e.samlStart(slug)
	why = ""
	if res := e.samlPost(rq2, idp.response(t, rq2, samlOpts{sign: "assertion", email: email, assertionID: aid})); cookieOf(res, "cn_session") != "" || why != "assertion replayed" {
		t.Fatalf("replayed assertion id: %q %q", why, loginError(res))
	}
	// the request stays usable after that refusal? no: it was consumed; a new sign-in is a new request
	rq3, _ := e.samlStart(slug)
	samlOK(t, e.samlPost(rq3, idp.response(t, rq3, samlOpts{sign: "assertion", email: email})))
	// a request that expired is refused
	rq4, _ := e.samlStart(slug)
	e.tcount(owner.ws, "with u as (update core.saml_requests set expires_at = now() - interval '1 minute' where request_id = $1 returning 1) select count(*) from u", rq4.ID)
	why = ""
	if res := e.samlPost(rq4, idp.response(t, rq4, samlOpts{sign: "assertion", email: email})); cookieOf(res, "cn_session") != "" || !strings.Contains(why, "no pending request") {
		t.Fatalf("expired request: %q", why)
	}
	// garbage in the form
	for _, body := range []string{"", "not base64 !!", base64.StdEncoding.EncodeToString([]byte("<nope/>")), base64.StdEncoding.EncodeToString([]byte(`<!DOCTYPE x [<!ENTITY a "b">]><x/>`))} {
		if res := e.samlPost(rq, body); cookieOf(res, "cn_session") != "" || res.StatusCode != 302 {
			t.Fatalf("garbage %q: %d", body, res.StatusCode)
		}
	}
}

func TestSAMLConfigurationAndMetadata(t *testing.T) {
	e := sec(t)
	dns := fakeDNS(t)
	idp := newSAMLIdP(t)
	owner := e.newAcct("SamlCfg", true)
	ck := e.session(owner)
	upd := func(ck string, sso map[string]any) resp {
		return e.run(ck, owner.ws, "security.update", map[string]any{"sso": sso})
	}
	// needs a verified domain and metadata
	if r := upd(ck, map[string]any{"enabled": true, "protocol": "saml", "saml_metadata": idp.metadata()}); r.status != 400 {
		t.Fatalf("no verified domain: %d %s", r.status, r.body)
	}
	e.verified(dns, ck, owner.ws, "corp.test")
	if r := upd(ck, map[string]any{"enabled": true, "protocol": "saml"}); r.status != 400 {
		t.Fatalf("no metadata: %d %s", r.status, r.body)
	}
	for name, md := range map[string]string{"not xml": "hello", "wrong root": "<a/>", "dtd": `<!DOCTYPE a [<!ENTITY x "y">]><a/>`,
		"no certificate": strings.Replace(idp.metadata(), "X509Certificate", "Nothing", 2), "http sso url": strings.Replace(idp.metadata(), "https://idp.example.test/sso", "http://idp.example.test/sso", 1)} {
		if r := upd(ck, map[string]any{"enabled": true, "protocol": "saml", "saml_metadata": md}); r.status != 400 {
			t.Fatalf("%s: %d %s", name, r.status, r.body)
		}
	}
	if r := upd(ck, map[string]any{"enabled": true, "protocol": "ldap", "saml_metadata": idp.metadata()}); r.status != 400 {
		t.Fatalf("protocol: %d %s", r.status, r.body)
	}
	r := upd(ck, map[string]any{"enabled": true, "protocol": "saml", "saml_metadata": idp.metadata(), "saml_email_attribute": "urn:custom:mail"})
	if r.status != 200 || !strings.Contains(string(r.body), idp.entityID) || strings.Contains(string(r.body), "X509Certificate") {
		t.Fatalf("configure: %d %s", r.status, r.body)
	}
	get := e.run(ck, owner.ws, "security.get", map[string]any{})
	if !strings.Contains(string(get.body), `"protocol":"saml"`) || !strings.Contains(string(get.body), "/api/auth/saml/") || !strings.Contains(string(get.body), `"email_attribute":"urn:custom:mail"`) {
		t.Fatalf("security.get: %s", get.body)
	}
	// the protocols are separate: this workspace is not an OIDC one
	var w struct{ Slug string }
	_ = json.Unmarshal(e.run(ck, owner.ws, "workspace.get", map[string]any{}).body, &w)
	// the shared sign-in entry sends a SAML workspace to its IdP
	if res := e.get("/api/auth/sso/start?workspace=" + w.Slug); !strings.HasPrefix(res.Header.Get("Location"), idp.ssoURL+"?SAMLRequest=") {
		t.Fatalf("sso start on a saml workspace: %q", res.Header.Get("Location"))
	}
	if res := e.get("/api/auth/saml/start?workspace=" + url.QueryEscape("nope-"+rnd())); !strings.Contains(loginError(res), "not found") {
		t.Fatalf("unknown workspace: %q", res.Header.Get("Location"))
	}
	// SP metadata for the IdP administrator
	md := hit(t, e.gosv.URL, "GET", "/api/auth/saml/"+w.Slug+"/metadata", nil, "")
	if md.status != 200 || !strings.Contains(md.header.Get("Content-Type"), "xml") || !strings.Contains(string(md.body), `/api/auth/saml/`+w.Slug+`/acs"`) ||
		!strings.Contains(string(md.body), `entityID="`) || !strings.Contains(string(md.body), `WantAssertionsSigned="true"`) {
		t.Fatalf("sp metadata: %d %s", md.status, md.body)
	}
	if md := hit(t, e.gosv.URL, "GET", "/api/auth/saml/Bad_Slug/metadata", nil, ""); md.status == 200 {
		t.Fatal("slug shape")
	}
	// policy changes stay owner-only and person-only
	admin := e.newAcct("SamlAdmin", false)
	if r := e.run(ck, owner.ws, "member.add", map[string]any{"email": admin.email, "role": "admin"}); r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if r := upd(e.session(admin), map[string]any{"enabled": false}); r.status != 403 {
		t.Fatalf("admin changing SSO: %d %s", r.status, r.body)
	}
}

func TestSAMLMetadataURLIsFetchedThroughTheGuard(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1") // the metadata server is on loopback
	e := sec(t)
	dns := fakeDNS(t)
	idp := newSAMLIdP(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metadata" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, idp.metadata())
	}))
	t.Cleanup(srv.Close)
	owner := e.newAcct("SamlUrl", true)
	ck := e.session(owner)
	e.verified(dns, ck, owner.ws, "corp.test")
	upd := func(m map[string]any) resp {
		m["enabled"], m["protocol"] = true, "saml"
		return e.run(ck, owner.ws, "security.update", map[string]any{"sso": m})
	}
	if r := upd(map[string]any{"saml_metadata_url": srv.URL + "/missing"}); r.status != 400 {
		t.Fatalf("404 metadata: %d %s", r.status, r.body)
	}
	if r := upd(map[string]any{"saml_metadata_url": srv.URL + "/metadata"}); r.status != 200 || !strings.Contains(string(r.body), idp.entityID) {
		t.Fatalf("fetch: %d %s", r.status, r.body)
	}
	if e.tcount(owner.ws, "select count(*) from core.workspace_security where saml_idp_metadata like '%' || $1 || '%'", idp.entityID) != 1 {
		t.Fatal("the fetched XML is stored")
	}
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "") // production: loopback is refused
	if r := upd(map[string]any{"saml_metadata_url": srv.URL + "/metadata"}); r.status != 400 || !strings.Contains(string(r.body), "refused") {
		t.Fatalf("private address: %d %s", r.status, r.body)
	}
}

// tcount runs a count query inside the workspace's tenant (RLS applies to the test pool too).
func (e *env) tcount(ws, q string, args ...any) int {
	e.t.Helper()
	tx, err := db.BeginTenant(context.Background(), e.pool, ws)
	if err != nil {
		e.t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var n int
	if err := tx.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// A valid IdP response is good only in the browser that started the sign-in (login CSRF): no cookie or somebody else's
// cookie is refused and does not burn the request, and the right one still completes.
func TestSAMLResponseBoundToStartingBrowser(t *testing.T) {
	e, idp, _, _, slug := samlWorkspace(t)
	rq, start := e.samlStart(slug)
	if rq.Nonce == "" {
		t.Fatal("no nonce cookie set")
	}
	var sc string
	for _, c := range start.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "cn_saml=") {
			sc = c
		}
	}
	for _, want := range []string{"HttpOnly", "Path=/api/auth/saml/" + slug + "/acs"} {
		if !strings.Contains(sc, want) {
			t.Errorf("cookie %q lacks %s", sc, want)
		}
	}
	other, _ := e.samlStart(slug) // another browser's sign-in has its own nonce
	resp := idp.response(t, rq, samlOpts{sign: "assertion", email: "bound-" + rnd() + "@corp.test"})
	for name, nonce := range map[string]string{"no cookie": "", "another browser's cookie": other.Nonce} {
		res := e.samlPostWith(rq, resp, nonce)
		if cookieOf(res, "cn_session") != "" || res.StatusCode == 303 && res.Header.Get("Location") == "/" {
			t.Fatalf("%s: signed in", name)
		}
	}
	samlOK(t, e.samlPost(rq, resp))
}
