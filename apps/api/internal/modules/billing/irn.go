package billing

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/cnote/api/internal/command"
)

// irnRequest is irn::IrnRequest; DocType is "INV" or "CRN".
type irnRequest struct {
	SupplierGSTIN, DocType, Number, FinancialYear string
	InvoiceDate                                   time.Time
	BuyerGSTIN                                    string
	TotalMinor                                    int64
}

type irnResponse struct {
	IRN, AckNo string
	AckDate    time.Time
	SignedQR   string
}

// GspClient is the provider abstraction (Rust `GspClient`). Rust never installs a production GSP, so the
// deterministic offline sandbox is what runs; SetGSP is the equivalent of `irn::set_gsp`.
type GspClient interface {
	GenerateIRN(ctx context.Context, r irnRequest) (irnResponse, error)
	CancelIRN(ctx context.Context, irn, reason string) error
}

var gsp GspClient = sandboxGSP{}

func SetGSP(c GspClient) { gsp = c }

func irnHash(r irnRequest) string {
	h := sha256.Sum256([]byte(r.SupplierGSTIN + r.DocType + r.Number + r.FinancialYear))
	return hex.EncodeToString(h[:])
}

type sandboxGSP struct{}

func (sandboxGSP) GenerateIRN(_ context.Context, r irnRequest) (irnResponse, error) {
	irn := irnHash(r)
	n, err := strconv.ParseUint(irn[:12], 16, 64)
	if err != nil {
		return irnResponse{}, err
	}
	// serde_json::json! objects serialize with sorted keys, which command.Marshal reproduces for maps.
	payload, err := command.Marshal(map[string]any{
		"SellerGstin": r.SupplierGSTIN, "BuyerGstin": r.BuyerGSTIN, "DocNo": r.Number, "DocTyp": r.DocType,
		"DocDt": r.InvoiceDate.Format("02/01/2006"), "TotInvVal": r.TotalMinor, "Irn": irn,
	})
	if err != nil {
		return irnResponse{}, err
	}
	b64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := sha256.Sum256([]byte("sandbox." + b64))
	return irnResponse{irn, strconv.FormatUint(n%1_000_000_000_000_000, 10), time.Now().UTC(), b64 + "." + hex.EncodeToString(sig[:])}, nil
}

func (sandboxGSP) CancelIRN(context.Context, string, string) error { return nil }

const (
	irnWarnDay  = 20
	irnBlockDay = 29
)

type window int

const (
	windowNotEnforced window = iota
	windowOpen
	windowWarn
	windowBlocked
)

// irnWindow: 30-day reporting window for taxpayers with AATO above Rs 10 crore. daysLeft is set for windowWarn.
func irnWindow(invoiceDate, today time.Time, enforced bool) (w window, daysLeft int64) {
	if !enforced {
		return windowNotEnforced, 0
	}
	d := daysBetween(today, invoiceDate)
	switch {
	case d >= irnBlockDay:
		return windowBlocked, 0
	case d >= irnWarnDay:
		return windowWarn, 30 - d
	}
	return windowOpen, 0
}

func warnText(daysLeft int64) string {
	return fmt.Sprintf("only %d day(s) left in the 30-day IRN reporting window", daysLeft)
}
