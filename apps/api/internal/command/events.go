package command

import "encoding/json"

// Emit appends an event attributed to the caller and enqueues its dispatch in the same transaction
// (transactional outbox, events.rs). Ported handlers that mutate must call it exactly like Rust does.
func (c *Ctx) Emit(kind string, noteID *string, payload any) (int64, error) {
	p := c.Principal
	body, err := Marshal(payload)
	if err != nil {
		return 0, ErrInvalid(err.Error())
	}
	actorType, actorID, onBehalf := p.Actor()
	var id int64
	if err := c.Tx.QueryRow(c.Context,
		`insert into core.events (workspace_id, note_id, kind, actor_type, actor_id, on_behalf_of, payload)
		 values ($1::uuid,$2::uuid,$3,$4,$5,$6::uuid,$7) returning id`,
		p.WorkspaceID, noteID, kind, actorType, actorID, onBehalf, json.RawMessage(body)).Scan(&id); err != nil {
		return 0, DB(err)
	}
	job, _ := Marshal(map[string]int64{"event_id": id})
	_, err = c.Tx.Exec(c.Context,
		"insert into core.jobs (workspace_id, kind, payload, run_at) values ($1::uuid,$2,$3,now())",
		p.WorkspaceID, "event.dispatch", json.RawMessage(job))
	return id, DB(err)
}
