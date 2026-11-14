// Package modules lists every module ported to Go. Porting a module = add its Commands() here
// (and delete nothing in Rust until the differential test is green and routing is flipped).
package modules

import (
	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/modules/notes"
)

func Commands() []command.Command {
	var cmds []command.Command
	cmds = append(cmds, notes.Commands()...)
	return cmds
}

func Registry() *command.Registry { return command.NewRegistry(Commands()...) }
