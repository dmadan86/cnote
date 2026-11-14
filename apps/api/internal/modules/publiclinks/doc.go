package publiclinks

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/billing"
)

// Document is the ONLY shape the public API returns. It lists what is printed on a quotation or invoice; internal
// notes (the page body), cost, the workspace's other data, member and contact ids never appear. The golden test
// pins this key set.
type Document struct {
	Kind          string      `json:"kind"` // quotation | invoice | receipt
	Number        *string     `json:"number"`
	Status        string      `json:"status"`
	Currency      string      `json:"currency"`
	Date          *string     `json:"date"`
	ValidUntil    *string     `json:"valid_until,omitempty"`
	DueDate       *string     `json:"due_date,omitempty"`
	PlaceOfSupply *string     `json:"place_of_supply"`
	ReverseCharge bool        `json:"reverse_charge"`
	Seller        party       `json:"seller"`
	Buyer         party       `json:"buyer"`
	Lines         []line      `json:"lines"`
	Subtotal      int64       `json:"subtotal_minor"`
	CGST          int64       `json:"cgst_minor"`
	SGST          int64       `json:"sgst_minor"`
	IGST          int64       `json:"igst_minor"`
	Total         int64       `json:"total_minor"`
	CanDecide     *bool       `json:"can_decide,omitempty"`
	Decision      *decided    `json:"decision,omitempty"`
	Paid          *int64      `json:"paid_minor,omitempty"`
	AmountDue     *int64      `json:"amount_due_minor,omitempty"`
	IRN           *string     `json:"irn,omitempty"`
	SignedQR      *string     `json:"signed_qr,omitempty"`
	Payment       *paymentOpt `json:"payment,omitempty"`
}

type party struct {
	Name  string  `json:"name"`
	GSTIN *string `json:"gstin"`
}

type line struct {
	Description string  `json:"description"`
	HsnSac      *string `json:"hsn_sac"`
	Quantity    string  `json:"quantity"`
	UnitPrice   int64   `json:"unit_price_minor"`
	GSTRateBP   int32   `json:"gst_rate_bp"`
	Taxable     int64   `json:"taxable_minor"`
	CGST        int64   `json:"cgst_minor"`
	SGST        int64   `json:"sgst_minor"`
	IGST        int64   `json:"igst_minor"`
}

type decided struct {
	Status string      `json:"status"`
	ByName *string     `json:"by_name"`
	Via    *string     `json:"via"`
	At     *command.Ts `json:"at"`
}

// paymentOpt: how to pay. upi_uri is set only with a configured VPA, an INR invoice and money still due.
type paymentOpt struct {
	UPIURI      *string `json:"upi_uri,omitempty"`
	UPIPayee    *string `json:"upi_payee,omitempty"`
	BankDetails *string `json:"bank_details,omitempty"`
}

func loadPublic(ctx context.Context, tx pgx.Tx, l link) (Document, error) {
	var kind string
	if err := tx.QueryRow(ctx, `select case when exists(select 1 from billing.quotations where note_id = $1::uuid) then 'quotation'
		when exists(select 1 from billing.invoices where note_id = $1::uuid and kind = 'invoice') then 'invoice'
		when exists(select 1 from billing.receipts where note_id = $1::uuid and kind = 'receipt') then 'receipt' else '' end`, l.note).Scan(&kind); err != nil {
		return Document{}, err
	}
	var d Document
	var err error
	switch kind {
	case "quotation":
		d, err = loadQuote(ctx, tx, l.note)
	case "invoice":
		d, err = loadInvoice(ctx, tx, l.note)
	case "receipt":
		d, err = loadReceipt(ctx, tx, l.note)
	default:
		return d, pgx.ErrNoRows
	}
	if err != nil {
		return d, err
	}
	var wsName string
	var raw []byte
	if err := tx.QueryRow(ctx, "select name, settings from core.workspaces where id = $1::uuid", l.ws).Scan(&wsName, &raw); err != nil {
		return d, err
	}
	var s struct {
		LegalName   string `json:"legal_name"`
		VPA         string `json:"upi_vpa"`
		Payee       string `json:"upi_payee_name"`
		BankDetails string `json:"bank_details"`
	}
	_ = json.Unmarshal(raw, &s)
	d.Seller.Name = wsName
	if strings.TrimSpace(s.LegalName) != "" {
		d.Seller.Name = s.LegalName
	}
	if d.Kind == "invoice" && d.Status != "cancelled" && d.Status != "draft" {
		p := paymentOpt{}
		if bd := strings.TrimSpace(s.BankDetails); bd != "" && len(bd) <= 1000 {
			p.BankDetails = &bd
		}
		if d.Currency == "INR" && d.AmountDue != nil && *d.AmountDue > 0 && d.Number != nil && s.VPA != "" {
			var m map[string]json.RawMessage
			_ = json.Unmarshal(raw, &m)
			if vpa, payee, ok := billing.UPIConfig(m); ok {
				if payee == "" {
					payee = d.Seller.Name
				}
				if uri, err := billing.UPIURI(vpa, payee, *d.AmountDue, *d.Number); err == nil {
					p.UPIURI, p.UPIPayee = &uri, &payee
				}
			}
		}
		if p.UPIURI != nil || p.BankDetails != nil {
			d.Payment = &p
		}
	}
	return d, nil
}

func lines(ctx context.Context, tx pgx.Tx, table, col, id string) ([]line, error) {
	rows, err := tx.Query(ctx, `select description, hsn_sac, quantity::text, unit_price_minor, gst_rate_bp, taxable_minor,
		cgst_minor, sgst_minor, igst_minor from `+table+` where `+col+` = $1::uuid order by position`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []line{}
	for rows.Next() {
		var l line
		if err := rows.Scan(&l.Description, &l.HsnSac, &l.Quantity, &l.UnitPrice, &l.GSTRateBP, &l.Taxable, &l.CGST, &l.SGST, &l.IGST); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func loadQuote(ctx context.Context, tx pgx.Tx, id string) (Document, error) {
	d := Document{Kind: "quotation"}
	var supplier *string
	var decidedAt *time.Time
	var via, by *string
	var contact *string
	err := tx.QueryRow(ctx, `select q.number, q.status, q.currency::text, q.quote_date::text, q.valid_until::text, q.place_of_supply,
		q.reverse_charge, q.supplier_gstin, q.buyer_gstin, cn.title, q.subtotal_minor, q.cgst_minor, q.sgst_minor, q.igst_minor,
		q.total_minor, q.decided_at, q.accepted_via, q.accepted_by_name
		from billing.quotations q left join core.notes cn on cn.id = q.contact_id where q.note_id = $1::uuid`, id).
		Scan(&d.Number, &d.Status, &d.Currency, &d.Date, &d.ValidUntil, &d.PlaceOfSupply, &d.ReverseCharge, &supplier, &d.Buyer.GSTIN,
			&contact, &d.Subtotal, &d.CGST, &d.SGST, &d.IGST, &d.Total, &decidedAt, &via, &by)
	if err != nil {
		return d, err
	}
	d.Seller.GSTIN = supplier
	if contact != nil {
		d.Buyer.Name = *contact
	}
	if d.Status == "draft" {
		return d, pgx.ErrNoRows // a draft is never public, even if a link outlived a status change
	}
	if d.Status == "sent" && d.ValidUntil != nil && *d.ValidUntil < billing.TodayIST().Format("2006-01-02") {
		d.Status = "expired"
	}
	can := d.Status == "sent"
	d.CanDecide = &can
	if decidedAt != nil && via != nil && *via == "link" {
		d.Decision = &decided{Status: d.Status, ByName: by, Via: via, At: command.TsPtr(decidedAt)}
	}
	d.Lines, err = lines(ctx, tx, "billing.quotation_lines", "quotation_id", id)
	return d, err
}

func loadInvoice(ctx context.Context, tx pgx.Tx, id string) (Document, error) {
	d := Document{Kind: "invoice"}
	var supplier *string
	var contact *string
	var credited, off int64
	var cancelledIRN *time.Time
	var paid int64
	err := tx.QueryRow(ctx, `select i.number, i.status, i.currency::text, i.invoice_date::text, i.due_date::text, i.place_of_supply,
		i.reverse_charge, i.supplier_gstin, i.buyer_gstin, cn.title, i.subtotal_minor, i.cgst_minor, i.sgst_minor, i.igst_minor,
		i.total_minor, i.paid_minor, i.credited_minor, i.written_off_minor, i.irn, i.signed_qr, i.irn_cancelled_at
		from billing.invoices i left join core.notes cn on cn.id = i.contact_id where i.note_id = $1::uuid`, id).
		Scan(&d.Number, &d.Status, &d.Currency, &d.Date, &d.DueDate, &d.PlaceOfSupply, &d.ReverseCharge, &supplier, &d.Buyer.GSTIN,
			&contact, &d.Subtotal, &d.CGST, &d.SGST, &d.IGST, &d.Total, &paid, &credited, &off, &d.IRN, &d.SignedQR, &cancelledIRN)
	if err != nil {
		return d, err
	}
	if d.Status == "draft" {
		return d, pgx.ErrNoRows
	}
	d.Seller.GSTIN = supplier
	if contact != nil {
		d.Buyer.Name = *contact
	}
	if cancelledIRN != nil { // a cancelled IRN must not be printed
		d.IRN, d.SignedQR = nil, nil
	}
	due := d.Total - paid - credited - off
	d.Paid, d.AmountDue = &paid, &due
	d.Lines, err = lines(ctx, tx, "billing.invoice_lines", "invoice_id", id)
	return d, err
}

// loadReceipt: the Rule 50 voucher as one line (advance received, taxable value and tax split).
func loadReceipt(ctx context.Context, tx pgx.Tx, id string) (Document, error) {
	d := Document{Kind: "receipt", Currency: "INR"}
	var desc, contact *string
	var l line
	err := tx.QueryRow(ctx, `select r.number, r.status, r.received_at::text, r.place_of_supply, r.supplier_gstin, r.buyer_gstin, cn.title,
		r.description, r.gst_rate_bp, r.taxable_minor, r.cgst_minor, r.sgst_minor, r.igst_minor, r.amount_minor
		from billing.receipts r left join core.notes cn on cn.id = r.contact_id where r.note_id = $1::uuid and r.kind = 'receipt'`, id).
		Scan(&d.Number, &d.Status, &d.Date, &d.PlaceOfSupply, &d.Seller.GSTIN, &d.Buyer.GSTIN, &contact, &desc, &l.GSTRateBP,
			&d.Subtotal, &d.CGST, &d.SGST, &d.IGST, &d.Total)
	if err != nil {
		return d, err
	}
	if contact != nil {
		d.Buyer.Name = *contact
	}
	l.Description, l.Quantity, l.UnitPrice, l.Taxable, l.CGST, l.SGST, l.IGST = "Advance received", "1", d.Subtotal, d.Subtotal, d.CGST, d.SGST, d.IGST
	if desc != nil && *desc != "" {
		l.Description = *desc
	}
	d.Lines = []line{l}
	return d, nil
}
