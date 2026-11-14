package billing

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/cnote/api/internal/access"
	"github.com/cnote/api/internal/command"
)

// UPI intent URI for a static QR / "Pay by UPI" button (NPCI linking spec): the customer's app prefills the payee,
// amount and note, then the payment is a normal UPI payment with zero platform fee. Nothing reconciles it
// automatically: a human records the payment.
var vpaRe = regexp.MustCompile(`^[A-Za-z0-9._-]{2,64}@[A-Za-z][A-Za-z0-9]{1,31}$`)

// UPIURI builds upi://pay?pa=&pn=&am=&cu=INR&tn=. The amount keeps its paise; spaces encode as %20 (some UPI apps
// misread '+').
func UPIURI(vpa, payee string, amountMinor int64, invoiceNo string) (string, error) {
	if !vpaRe.MatchString(vpa) {
		return "", command.ErrInvalid("upi_vpa in workspace settings is not a valid UPI id (name@bank)")
	}
	if amountMinor <= 0 {
		return "", command.ErrConflict("nothing is outstanding on this invoice")
	}
	payee = strings.TrimSpace(payee)
	if payee == "" || utf8.RuneCountInString(payee) > 99 {
		return "", command.ErrInvalid("upi_payee_name in workspace settings is required (at most 99 characters)")
	}
	esc := func(v string) string { return strings.ReplaceAll(url.QueryEscape(v), "+", "%20") }
	return fmt.Sprintf("upi://pay?pa=%s&pn=%s&am=%d.%02d&cu=INR&tn=%s", esc(vpa), esc(payee), amountMinor/100, amountMinor%100, esc(invoiceNo)), nil
}

// UPIConfig reads the payee settings; ok is false when no VPA is configured (the QR is then simply not shown).
func UPIConfig(settings map[string]json.RawMessage) (vpa, payee string, ok bool) {
	s := settingsMap(settings)
	vpa, ok = s.str("upi_vpa")
	if !ok || strings.TrimSpace(vpa) == "" {
		return "", "", false
	}
	payee, _ = s.str("upi_payee_name")
	return strings.TrimSpace(vpa), payee, true
}

// invoiceUPIQR: invoice.upi_qr {id} -> {upi_uri}. INR invoices that are issued and still owe money only.
func invoiceUPIQR(c *command.Ctx, input json.RawMessage) (any, error) {
	id, err := byID(input)
	if err != nil {
		return nil, err
	}
	if _, err := access.AssertNoteAccess(c, id, false); err != nil {
		return nil, err
	}
	inv, err := loadInvoice(c, id, false)
	if err != nil {
		return nil, err
	}
	if inv.Currency != "INR" || inv.Kind != "invoice" || inv.Number == nil || inv.Status == "draft" || inv.Status == "cancelled" {
		return nil, command.ErrConflict("a UPI QR needs an issued, uncancelled invoice in INR")
	}
	m, err := workspaceSettings(c)
	if err != nil {
		return nil, err
	}
	vpa, payee, ok := UPIConfig(m)
	if !ok {
		return nil, command.ErrInvalid("set upi_vpa and upi_payee_name in workspace settings first")
	}
	uri, err := UPIURI(vpa, payee, inv.outstanding(), *inv.Number)
	if err != nil {
		return nil, err
	}
	return map[string]any{"upi_uri": uri}, nil
}
