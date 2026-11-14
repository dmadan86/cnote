// Package modules lists every module ported to Go. Porting a module = add its Commands() here
// (and delete nothing in Rust until the differential test is green and routing is flipped).
package modules

import (
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/admin"
	"github.com/cnote/api/internal/modules/approvals"
	"github.com/cnote/api/internal/modules/billing"
	"github.com/cnote/api/internal/modules/crm"
	"github.com/cnote/api/internal/modules/docs"
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
	"github.com/cnote/api/internal/modules/recurring"
	"github.com/cnote/api/internal/modules/sharing"
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
	cmds = append(cmds, home.Commands()...)
	cmds = append(cmds, leads.Commands()...)
	cmds = append(cmds, publiclinks.Commands()...)
	cmds = append(cmds, approvals.Commands()...)
	return withMeta(approvals.Wire(cmds))
}

func Registry() *command.Registry { return command.NewRegistry(Commands()...) }
