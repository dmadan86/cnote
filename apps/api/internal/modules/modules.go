// Package modules lists every module ported to Go. Porting a module = add its Commands() here
// (and delete nothing in Rust until the differential test is green and routing is flipped).
package modules

import (
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/admin"
	"github.com/cnote/api/internal/modules/notes"
	"github.com/cnote/api/internal/modules/notifications"
	"github.com/cnote/api/internal/modules/projects"
	"github.com/cnote/api/internal/modules/sharing"
)

func Commands() []command.Command {
	var cmds []command.Command
	cmds = append(cmds, notes.Commands()...)
	cmds = append(cmds, sharing.Commands()...)
	cmds = append(cmds, admin.Commands()...)
	cmds = append(cmds, notifications.Commands()...)
	cmds = append(cmds, projects.Commands()...)
	return cmds
}

func Registry() *command.Registry { return command.NewRegistry(Commands()...) }
