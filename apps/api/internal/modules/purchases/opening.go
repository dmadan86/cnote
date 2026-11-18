package purchases

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

// Import-only commands for the data importers: an opening bill is an unpaid vendor bill carried over from another
// system. It is inserted recorded with one "Opening balance" line at 0% GST and never posts to the ledger (the opening
// entry holds Cr 2100 for all of them). Tax reports leave `opening` rows out; cancel refuses them.

func adminOnly(name string, h command.Handler) command.Command {
	c := command.NewCommand(name, command.GroupAdmin, h)
	c.MinRole = command.Admin
	return c
}

func openingBill() command.Command { return adminOnly("bill.import_opening", importOpening) }
func openingBillSummary() command.Command {
	c := adminOnly("bill.import_opening_summary", func(c *command.Ctx, _ json.RawMessage) (any, error) {
		var n, total int64
		if err := c.Tx.QueryRow(c.Context, "select count(*), coalesce(sum(total_minor), 0)::bigint from billing.purchase_bills where opening and status <> 'cancelled'").Scan(&n, &total); err != nil {
			return nil, command.DB(err)
		}
		return map[string]any{"count": n, "total_minor": total}, nil
	})
	c.Group = command.GroupRead
	return c
}
func openingBillCancel() command.Command {
	return adminOnly("bill.import_opening_cancel", importOpeningCancel)
}

func validDay(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil && len(s) == 10
}

// bill.import_opening {space_id, vendor_id, vendor_bill_number, bill_date, due_date?, amount_minor} -> the bill
func importOpening(c *command.Ctx, input json.RawMessage) (any, error) {
	var v struct {
		SpaceID          string  `json:"space_id"`
		VendorID         string  `json:"vendor_id"`
		VendorBillNumber string  `json:"vendor_bill_number"`
		BillDate         string  `json:"bill_date"`
		DueDate          *string `json:"due_date"`
		AmountMinor      int64   `json:"amount_minor"`
	}
	if json.Unmarshal(input, &v) != nil {
		return nil, command.ErrInvalid("input does not match the command's fields")
	}
	v.VendorBillNumber = strings.TrimSpace(v.VendorBillNumber)
	if v.SpaceID == "" || v.VendorID == "" || v.VendorBillNumber == "" || len(v.VendorBillNumber) > 64 {
		return nil, command.ErrInvalid("space_id, vendor_id and a vendor_bill_number of up to 64 characters are required")
	}
	if v.AmountMinor <= 0 || v.AmountMinor > 1_000_000_000_000 {
		return nil, command.ErrInvalid("amount_minor must be positive")
	}
	if !validDay(v.BillDate) || (v.DueDate != nil && !validDay(*v.DueDate)) {
		return nil, command.ErrInvalid("dates must be YYYY-MM-DD")
	}
	ven, err := loadVendor(c, v.VendorID)
	if err != nil {
		return nil, err
	}
	if err := checkDuplicate(c, v.VendorID, v.VendorBillNumber, ""); err != nil {
		return nil, err
	}
	note, err := notes.InsertNote(c, v.SpaceID, "Opening bill "+v.VendorBillNumber+" from "+ven.name, nil, nil, []string{"purchase_bill"})
	if err != nil {
		return nil, err
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.purchase_bills (note_id, workspace_id, vendor_id, vendor_bill_number, bill_date, due_date,
		   supplier_gstin, supplier_state, status, recorded_at, subtotal_minor, total_minor, opening)
		 values ($1::uuid,$2::uuid,$3::uuid,$4,$5::date,coalesce($6::date, $5::date + 30),$7,$8,'recorded',now(),$9,$9,true)`,
		note.ID, c.WorkspaceID(), v.VendorID, v.VendorBillNumber, v.BillDate, nilStr(v.DueDate), nilIf(ven.gstin), nilIf(ven.state), v.AmountMinor); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Tx.Exec(c.Context,
		`insert into billing.purchase_bill_lines (workspace_id, bill_id, position, description, quantity, unit_price_minor, gst_rate_bp, taxable_minor)
		 values ($1::uuid,$2::uuid,1,'Opening balance',1,$3,0,$3)`, c.WorkspaceID(), note.ID, v.AmountMinor); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bill.opening_imported", &note.ID, map[string]any{"vendor_bill_number": v.VendorBillNumber, "total_minor": v.AmountMinor, "vendor_id": v.VendorID}); err != nil {
		return nil, err
	}
	return detailOf(c, note.ID)
}

// bill.import_opening_cancel {id}: undo of an import; refused once a payment is recorded.
func importOpeningCancel(c *command.Ctx, input json.RawMessage) (any, error) {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(input, &v) != nil || v.ID == "" {
		return nil, command.ErrInvalid("id is required")
	}
	var opening bool
	var status string
	var paid int64
	if err := c.Tx.QueryRow(c.Context, "select opening, status, paid_minor from billing.purchase_bills where note_id = $1::uuid for update", v.ID).Scan(&opening, &status, &paid); err != nil {
		return nil, command.ErrNotFound("bill")
	}
	if !opening {
		return nil, command.ErrConflict("not an opening balance bill")
	}
	if status == "cancelled" {
		return map[string]any{"id": v.ID, "status": "cancelled"}, nil
	}
	if paid > 0 {
		return nil, command.ErrConflict("a payment is recorded on this opening bill")
	}
	if _, err := c.Tx.Exec(c.Context, "update billing.purchase_bills set status = 'cancelled', cancelled_at = now() where note_id = $1::uuid", v.ID); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("bill.opening_cancelled", &v.ID, map[string]any{}); err != nil {
		return nil, err
	}
	return map[string]any{"id": v.ID, "status": "cancelled"}, nil
}
