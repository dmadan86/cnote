package auth

// SAML 2.0 service-provider sign-in per workspace (SP-initiated, HTTP-Redirect request, HTTP-POST response) as an
// alternative to OIDC. XML signatures, audience, destination/recipient, InResponseTo and time windows are checked by
// github.com/crewjam/saml (goxmldsig underneath, pinned to the IdP certificates in the stored metadata); on top of that
// this file requires exactly one assertion, one ID per element, a bound subject/conditions/audience, a single use of
// each request id and assertion id (both in the database) and a verified-domain email. A signature is always required:
// a signed Response or a signed Assertion. IdP-initiated sign-in and encrypted assertions are not supported.

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/crewjam/saml"
	"github.com/jackc/pgx/v5"
	xrv "github.com/mattermost/xml-roundtrip-validator"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/cnote/api/internal/netguard"
)

const samlRequestTTL = 10 * time.Minute

func init() { saml.MaxClockSkew = time.Minute }

// OnSAMLReject sees the private reason a response was refused (tests; the browser only gets a generic message).
var OnSAMLReject func(reason string)

func samlReject(public, private string) *command.Error {
	slog.Info("saml response refused", "reason", private)
	if OnSAMLReject != nil {
		OnSAMLReject(private)
	}
	return ssoErr(public)
}

// SAMLConfig is a workspace's SAML settings.
type SAMLConfig struct {
	WorkspaceID, EmailAttr string
	AllowedDomains         []string
	IDP                    *saml.EntityDescriptor
}

func (a *Auth) samlConfig(ctx context.Context, slug string) (SAMLConfig, error) {
	var c SAMLConfig
	var md string
	err := a.Pool.QueryRow(ctx, "select workspace_id::text, idp_metadata, email_attribute, allowed_domains from core.saml_by_slug($1)", slug).
		Scan(&c.WorkspaceID, &md, &c.EmailAttr, &c.AllowedDomains)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, command.ErrNotFound("single sign-on for this workspace")
	}
	if err != nil {
		return c, command.DB(err)
	}
	if c.IDP, err = ParseIDPMetadata([]byte(md)); err != nil {
		return c, ssoErr(err.Error())
	}
	return c, nil
}

// SAMLURLs are the service provider's identifiers for a workspace under the public base.
func SAMLURLs(base, slug string) (entityID, acs string) {
	p := base + "/api/auth/saml/" + url.PathEscape(slug)
	return p + "/metadata", p + "/acs"
}

func samlSP(base, slug string, idp *saml.EntityDescriptor) (*saml.ServiceProvider, error) {
	entity, acs := SAMLURLs(base, slug)
	m, err1 := url.Parse(entity)
	c, err2 := url.Parse(acs)
	if err1 != nil || err2 != nil {
		return nil, errors.New("bad public address")
	}
	return &saml.ServiceProvider{EntityID: entity, MetadataURL: *m, AcsURL: *c, IDPMetadata: idp, AuthnNameIDFormat: saml.UnspecifiedNameIDFormat}, nil
}

// SPMetadata is the metadata document the IdP administrator imports. It does not depend on the workspace's configuration.
func SPMetadata(base, slug string) ([]byte, error) {
	sp, err := samlSP(base, slug, nil)
	if err != nil {
		return nil, err
	}
	return xml.MarshalIndent(sp.Metadata(), "", "  ")
}

var certWS = regexp.MustCompile(`\s+`)

// ParseIDPMetadata reads an IdP's metadata (one EntityDescriptor, or the first IdP in an EntitiesDescriptor) and checks it
// is usable: an entity id, an HTTP-Redirect sign-on URL and at least one parseable signing certificate.
func ParseIDPMetadata(raw []byte) (*saml.EntityDescriptor, error) {
	bad := func(why string) (*saml.EntityDescriptor, error) {
		return nil, errors.New("the identity provider metadata is not usable: " + why)
	}
	if len(raw) == 0 || len(raw) > maxIDPBody {
		return bad("empty or too large")
	}
	if bytes.Contains(raw, []byte("<!DOCTYPE")) || bytes.Contains(raw, []byte("<!ENTITY")) {
		return bad("DTDs are not allowed")
	}
	if xrv.Validate(bytes.NewReader(raw)) != nil {
		return bad("not well-formed XML")
	}
	var ed saml.EntityDescriptor
	if err := xml.Unmarshal(raw, &ed); err != nil {
		var set saml.EntitiesDescriptor
		if xml.Unmarshal(raw, &set) != nil {
			return bad("not SAML metadata")
		}
		found := false
		for _, e := range set.EntityDescriptors {
			if len(e.IDPSSODescriptors) > 0 {
				ed, found = e, true
				break
			}
		}
		if !found {
			return bad("no identity provider in it")
		}
	}
	if ed.EntityID == "" || len(ed.IDPSSODescriptors) == 0 {
		return bad("no identity provider entity id or descriptor")
	}
	sp := saml.ServiceProvider{IDPMetadata: &ed}
	loc := sp.GetSSOBindingLocation(saml.HTTPRedirectBinding)
	u, err := url.Parse(loc)
	if loc == "" || err != nil || (u.Scheme != "https" && !(u.Scheme == "http" && netguard.PrivateAllowed())) {
		return bad("it needs an https HTTP-Redirect single sign-on URL")
	}
	certs := 0
	for _, d := range ed.IDPSSODescriptors {
		for _, k := range d.KeyDescriptors {
			if k.Use != "" && k.Use != "signing" {
				continue
			}
			for _, c := range k.KeyInfo.X509Data.X509Certificates {
				der, err := base64.StdEncoding.DecodeString(certWS.ReplaceAllString(c.Data, ""))
				if err != nil {
					return bad("a certificate is not valid base64")
				}
				if _, err := x509.ParseCertificate(der); err != nil {
					return bad("a certificate cannot be parsed")
				}
				certs++
			}
		}
	}
	if certs == 0 {
		return bad("no signing certificate")
	}
	return &ed, nil
}

// FetchIDPMetadata downloads metadata through the SSRF guard and validates it; the validated XML is what gets stored.
func FetchIDPMetadata(ctx context.Context, u string) (string, error) {
	buf, err := idpFetch(ctx, http.MethodGet, u, nil, "application/samlmetadata+xml, application/xml, text/xml")
	if err != nil {
		return "", err
	}
	if _, err := ParseIDPMetadata(buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// SAMLStart stores a request id and returns the IdP URL to send the browser to.
func (a *Auth) SAMLStart(ctx context.Context, slug, base string) (string, error) {
	cfg, err := a.samlConfig(ctx, slug)
	if err != nil {
		return "", err
	}
	sp, err := samlSP(base, slug, cfg.IDP)
	if err != nil {
		return "", ssoErr(err.Error())
	}
	req, err := sp.MakeAuthenticationRequest(sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return "", ssoErr("could not build the sign-in request")
	}
	to, err := req.Redirect("", sp)
	if err != nil {
		return "", ssoErr("could not build the sign-in request")
	}
	tx, err := db.BeginTenant(ctx, a.Pool, cfg.WorkspaceID)
	if err != nil {
		return "", command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "delete from core.saml_requests where expires_at < now()"); err != nil {
		return "", command.DB(err)
	}
	if _, err := tx.Exec(ctx, "insert into core.saml_requests (workspace_id, request_id, expires_at) values ($1::uuid, $2, now() + make_interval(secs => $3))",
		cfg.WorkspaceID, req.ID, samlRequestTTL.Seconds()); err != nil {
		return "", command.DB(err)
	}
	return to.String(), command.DB(tx.Commit(ctx))
}

// structure refuses what the library would tolerate: DTDs, repeated IDs (the usual signature-wrapping carrier) and anything
// but exactly one assertion in the whole document. It returns the response's InResponseTo.
func structure(doc []byte) (string, error) {
	if bytes.Contains(doc, []byte("<!DOCTYPE")) || bytes.Contains(doc, []byte("<!ENTITY")) {
		return "", errors.New("DTD in response")
	}
	dec := xml.NewDecoder(bytes.NewReader(doc))
	ids, assertions, depth, inResp := map[string]bool{}, 0, 0, ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			if _, end := tok.(xml.EndElement); end {
				depth--
			}
			continue
		}
		depth++
		if depth == 1 {
			if el.Name.Local != "Response" || el.Name.Space != "urn:oasis:names:tc:SAML:2.0:protocol" {
				return "", errors.New("root is not a SAML Response")
			}
			for _, at := range el.Attr {
				if at.Name.Local == "InResponseTo" {
					inResp = at.Value
				}
			}
		}
		if el.Name.Local == "Assertion" || el.Name.Local == "EncryptedAssertion" {
			if el.Name.Local == "EncryptedAssertion" || el.Name.Space != "urn:oasis:names:tc:SAML:2.0:assertion" {
				return "", errors.New("encrypted or foreign assertion")
			}
			assertions++
		}
		for _, at := range el.Attr {
			if strings.EqualFold(at.Name.Local, "id") {
				if ids[at.Value] {
					return "", errors.New("duplicate ID " + at.Value)
				}
				ids[at.Value] = true
			}
		}
	}
	if assertions != 1 {
		return "", fmt.Errorf("%d assertions in the response", assertions)
	}
	return inResp, nil
}

var emailAttrs = []string{"email", "mail", "emailaddress", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress", "urn:oid:0.9.2342.19200300.100.1.3"}
var nameAttrs = []string{"displayname", "name", "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name", "cn"}

func attr(as *saml.Assertion, names ...string) string {
	for _, n := range names {
		for _, st := range as.AttributeStatements {
			for _, at := range st.Attributes {
				if (strings.EqualFold(at.Name, n) || strings.EqualFold(at.FriendlyName, n)) && len(at.Values) > 0 {
					if v := strings.TrimSpace(at.Values[0].Value); v != "" {
						return v
					}
				}
			}
		}
	}
	return ""
}

// SAMLFinish completes the login from the POSTed SAMLResponse: it returns the user id, the workspace id (the session is
// pinned to it) and the session token.
func (a *Auth) SAMLFinish(ctx context.Context, slug, base, samlResponse string) (userID, ws, token string, err error) {
	const generic = "the identity provider's response could not be verified"
	cfg, err := a.samlConfig(ctx, slug)
	if err != nil {
		return "", "", "", ssoErr("single sign-on is not set up for this workspace")
	}
	doc, derr := base64.StdEncoding.DecodeString(certWS.ReplaceAllString(samlResponse, ""))
	if derr != nil || len(doc) == 0 || len(doc) > maxIDPBody {
		return "", "", "", samlReject(generic, "response is not base64 or too large")
	}
	inResp, serr := structure(doc)
	if serr != nil {
		return "", "", "", samlReject(generic, "structure: "+serr.Error())
	}
	sp, err := samlSP(base, slug, cfg.IDP)
	if err != nil {
		return "", "", "", ssoErr(err.Error())
	}
	tx, err := db.BeginTenant(ctx, a.Pool, cfg.WorkspaceID)
	if err != nil {
		return "", "", "", command.DB(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var pending bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from core.saml_requests where request_id = $1 and expires_at > now())", inResp).Scan(&pending); err != nil {
		return "", "", "", command.DB(err)
	}
	if inResp == "" || !pending {
		return "", "", "", samlReject("the sign-in link is incomplete or expired", "InResponseTo matches no pending request")
	}
	as, perr := parseSAML(sp, doc, inResp)
	if perr != nil {
		return "", "", "", samlReject(generic, perr.Error())
	}
	// the request and the assertion are each good once (delete/insert rows decide, atomically)
	if tag, err := tx.Exec(ctx, "delete from core.saml_requests where request_id = $1 and expires_at > now()", inResp); err != nil || tag.RowsAffected() != 1 {
		if err != nil {
			return "", "", "", command.DB(err)
		}
		return "", "", "", samlReject("the sign-in link is incomplete or expired", "request id already used")
	}
	until := as.Conditions.NotOnOrAfter.Add(saml.MaxClockSkew)
	if _, err := tx.Exec(ctx, "delete from core.saml_assertions where expires_at < now()"); err != nil {
		return "", "", "", command.DB(err)
	}
	tag, err := tx.Exec(ctx, "insert into core.saml_assertions (workspace_id, assertion_id, expires_at) values ($1::uuid, $2, $3) on conflict do nothing", cfg.WorkspaceID, as.ID, until)
	if err != nil {
		return "", "", "", command.DB(err)
	}
	if tag.RowsAffected() != 1 {
		return "", "", "", samlReject(generic, "assertion replayed")
	}
	if err := tx.Commit(ctx); err != nil {
		return "", "", "", command.DB(err)
	}
	email := NormEmail(attr(as, append([]string{cfg.EmailAttr}, emailAttrs...)...))
	if email == "" && strings.Contains(as.Subject.NameID.Value, "@") {
		email = NormEmail(as.Subject.NameID.Value)
	}
	_, dom, hasAt := strings.Cut(email, "@")
	switch {
	case !hasAt || dom == "":
		return "", "", "", ssoErr("the provider sent no email address")
	case !slices.Contains(cfg.AllowedDomains, dom):
		return "", "", "", ssoErr("your email domain is not allowed for this workspace")
	}
	// (entity id, NameID) is the identity; the prefix keeps it apart from an OIDC issuer of the same text
	userID, err = a.ssoMember(ctx, cfg.WorkspaceID, "saml:"+cfg.IDP.EntityID, email, attr(as, nameAttrs...), as.Subject.NameID.Value)
	if err != nil {
		return "", "", "", err
	}
	token, err = a.createSession(ctx, userID, "sso", cfg.WorkspaceID)
	return userID, cfg.WorkspaceID, token, err
}

// parseSAML runs the library's checks (signature against the pinned certificates, destination, InResponseTo, issuer,
// audience, recipient, time windows) and then insists on the parts it treats as optional.
func parseSAML(sp *saml.ServiceProvider, doc []byte, requestID string) (as *saml.Assertion, err error) {
	defer func() {
		if r := recover(); r != nil { // the library dereferences Subject and Conditions
			as, err = nil, fmt.Errorf("malformed assertion: %v", r)
		}
	}()
	as, err = sp.ParseXMLResponse(doc, []string{requestID}, sp.AcsURL)
	if err != nil {
		var ir *saml.InvalidResponseError
		if errors.As(err, &ir) && ir.PrivateErr != nil {
			return nil, ir.PrivateErr
		}
		return nil, err
	}
	switch {
	case as.Subject == nil || as.Subject.NameID == nil || strings.TrimSpace(as.Subject.NameID.Value) == "":
		return nil, errors.New("no NameID")
	case as.Subject.NameID.Format == string(saml.TransientNameIDFormat):
		return nil, errors.New("transient NameID cannot identify a person")
	case as.Conditions == nil || as.Conditions.NotOnOrAfter.IsZero() || len(as.Conditions.AudienceRestrictions) == 0:
		return nil, errors.New("assertion has no validity window or audience")
	case len(as.Subject.SubjectConfirmations) == 0:
		return nil, errors.New("no subject confirmation (recipient)")
	}
	for _, sc := range as.Subject.SubjectConfirmations {
		if sc.SubjectConfirmationData == nil || sc.SubjectConfirmationData.Recipient == "" || sc.SubjectConfirmationData.InResponseTo != requestID {
			return nil, errors.New("subject confirmation without recipient or InResponseTo")
		}
	}
	return as, nil
}

// IsSAML reports whether the workspace's single sign-on is SAML (the sign-in page has one entry for both protocols).
func (a *Auth) IsSAML(ctx context.Context, slug string) bool {
	var ok bool
	_ = a.Pool.QueryRow(ctx, "select exists (select 1 from core.saml_by_slug($1))", slug).Scan(&ok)
	return ok
}
