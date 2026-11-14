// Package modules lists every module ported to Go. Porting a module = add its Commands() here
// (and delete nothing in Rust until the differential test is green and routing is flipped).
package modules

import (
	"strings"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/admin"
	"github.com/cnote/api/internal/modules/approvals"
	"github.com/cnote/api/internal/modules/audit"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/modules/company"
	"github.com/cnote/api/internal/modules/crm"
	"github.com/cnote/api/internal/modules/docs"
	"github.com/cnote/api/internal/modules/expenses"
	"github.com/cnote/api/internal/modules/home"
	"github.com/cnote/api/internal/modules/leads"
	"github.com/cnote/api/internal/modules/marketplace"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/cnote/api/internal/modules/notifications"
	"github.com/cnote/api/internal/modules/planning"
	"github.com/cnote/api/internal/modules/projects"
	"github.com/cnote/api/internal/modules/publiclinks"
	"github.com/cnote/api/internal/modules/purchases"
	"github.com/cnote/api/internal/modules/quotes"
	"github.com/cnote/api/internal/modules/receipts"
	"github.com/cnote/api/internal/modules/recurring"
	"github.com/cnote/api/internal/modules/sharing"
	"github.com/cnote/api/internal/modules/timelog"
)

func Commands() []command.Command {
	var cmds []command.Command
	cmds = append(cmds, marketplace.Commands()...)
	cmds = append(cmds, notes.Commands()...)
	cmds = append(cmds, crm.Commands()...)
	cmds = append(cmds, sharing.Commands()...)
	cmds = append(cmds, admin.Commands()...)
	cmds = append(cmds, notifications.Commands()...)
	cmds = append(cmds, billing.Commands()...)
	cmds = append(cmds, docs.Commands()...)
	cmds = append(cmds, projects.Commands()...)
	cmds = append(cmds, planning.Commands()...)
	cmds = append(cmds, quotes.Commands()...)
	cmds = append(cmds, purchases.Commands()...)
	cmds = append(cmds, recurring.Commands()...)
	cmds = append(cmds, receipts.Commands()...)
	cmds = append(cmds, home.Commands()...)
	cmds = append(cmds, leads.Commands()...)
	cmds = append(cmds, publiclinks.Commands()...)
	cmds = append(cmds, approvals.Commands()...)
	cmds = append(cmds, audit.Commands()...)
	cmds = append(cmds, company.Commands()...)
	cmds = append(cmds, expenses.Commands()...)
	cmds = append(cmds, timelog.Commands()...)
	return withMeta(markFinance(approvals.Wire(cmds)))
}

func Registry() *command.Registry { return command.NewRegistry(Commands()...) }

// Commands that read or move money. A member whose finance_access is off cannot run them (admins and owners always can).
// Quotes are sales documents and stay open; notes that happen to be invoices are governed by page access instead.
var financePrefixes = []string{"invoice.", "credit_note.", "payment.", "receipt.", "recurring.", "bill.", "vendor.", "report.receivables", "report.gst", "report.payables", "report.itc", "export.tally", "ledger.", "tds.", "report.tds", "report.profit_loss", "expense.approve", "expense.reimburse", "expense.record_direct", "time.invoice", "milestone.", "project.billing", "report.project_money"}

func markFinance(cmds []command.Command) []command.Command {
	for i, c := range cmds {
		for _, p := range financePrefixes {
			if strings.HasPrefix(c.Name, p) {
				cmds[i].Finance = true
			}
		}
	}
	return cmds
}
