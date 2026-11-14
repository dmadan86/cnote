package billing

import (
	"fmt"

	"github.com/cnote/api/internal/command"
)

const (
	acctReceivables = "1100"
	acctBank        = "1200"
	acctCGST        = "2201"
	acctSGST        = "2202"
	acctIGST        = "2203"
	acctRevenue     = "4000"
	acctBadDebt     = "5100"
)

// leg is (account code, debit, credit) in paise.
type leg struct {
	code          string
	debit, credit int64
}

// post writes one balanced journal entry; zero legs are dropped.
func post(c *command.Ctx, sourceType, sourceID, memo string, legs []leg) error {
	var d, cr int64
	for _, l := range legs {
		d += l.debit
		cr += l.credit
	}
	if d != cr {
		return command.NewError(command.Internal, fmt.Sprintf("unbalanced entry: debits %d credits %d", d, cr))
	}
	var entry string
	if err := c.Tx.QueryRow(c.Context,
		"insert into billing.journal_entries (workspace_id, source_type, source_id, memo) values ($1::uuid,$2,$3::uuid,$4) returning id::text",
		c.WorkspaceID(), sourceType, sourceID, memo).Scan(&entry); err != nil {
		return command.DB(err)
	}
	for _, l := range legs {
		if l.debit == 0 && l.credit == 0 {
			continue
		}
		if _, err := c.Tx.Exec(c.Context,
			"insert into billing.journal_lines (workspace_id, entry_id, account_code, debit_minor, credit_minor) values ($1::uuid,$2::uuid,$3,$4,$5)",
			c.WorkspaceID(), entry, l.code, l.debit, l.credit); err != nil {
			return command.DB(err)
		}
	}
	return nil
}

// saleLegs: receivable against revenue and output taxes (or, with reverse, the credit-note reversal).
func saleLegs(subtotal, cgst, sgst, igst int64, reverse bool) []leg {
	total := subtotal + cgst + sgst + igst
	side := func(code string, amt int64) leg {
		if reverse {
			return leg{code, amt, 0}
		}
		return leg{code, 0, amt}
	}
	recv := leg{acctReceivables, total, 0}
	if reverse {
		recv = leg{acctReceivables, 0, total}
	}
	return []leg{recv, side(acctRevenue, subtotal), side(acctCGST, cgst), side(acctSGST, sgst), side(acctIGST, igst)}
}
