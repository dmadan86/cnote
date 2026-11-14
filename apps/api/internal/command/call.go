package command

import "encoding/json"

// Call runs another command inside the same transaction with the full gate (role, scopes, rate limit, agent
// groups, usage), like Rust `ctx.call`. The result is the serialized response.
func (c *Ctx) Call(name string, input any) (json.RawMessage, error) {
	b, err := Marshal(input)
	if err != nil {
		return nil, ErrInvalid(err.Error())
	}
	out, err := c.reg.run(c, name, b, ExecOptions{})
	return out, err
}

// Bind attaches this registry to a context whose tenant transaction is owned by the caller.
func (r *Registry) Bind(c *Ctx) { c.reg = r }
