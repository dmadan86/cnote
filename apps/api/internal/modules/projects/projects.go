package projects

import "github.com/cnote/api/internal/command"

// Commands are the item, workflow and evidence commands of crates/cnote-projects (items.rs, wf.rs, evidence.rs).
func Commands() []command.Command {
	admin := func(name string, h command.Handler) command.Command {
		c := command.NewCommand(name, command.GroupAdmin, h)
		c.MinRole = command.Admin
		return c
	}
	return []command.Command{
		command.NewCommand("item.create", command.GroupWriteItems, itemCreate),
		command.NewCommand("item.get", command.GroupRead, itemGet),
		command.NewCommand("item.update", command.GroupWriteItems, itemUpdate),
		command.NewCommand("item.transition", command.GroupWriteItems, itemTransition),
		command.NewCommand("item.assign", command.GroupWriteItems, itemAssign),
		command.NewCommand("item.list", command.GroupRead, itemList),
		command.NewCommand("item.rank", command.GroupWriteItems, itemRank),
		command.NewCommand("triage.list", command.GroupRead, triageList),
		command.NewCommand("triage.accept", command.GroupWriteItems, triageAccept),
		command.NewCommand("workflow.get", command.GroupRead, workflowGet),
		admin("workflow.update", workflowUpdate),
		command.NewCommand("space.drift", command.GroupRead, spaceDrift),
		admin("space.realign", spaceRealign),
		command.NewCommand("evidence.record", command.GroupWriteItems, evidenceRecord),
	}
}
