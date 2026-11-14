package command

// Role order matters: Guest < Member < Admin < Owner (principal.rs).
type Role int

const (
	Guest Role = iota
	Member
	Admin
	Owner
)

func ParseRole(s string) Role {
	switch s {
	case "owner":
		return Owner
	case "admin":
		return Admin
	case "member":
		return Member
	}
	return Guest
}

func (r Role) String() string { return [...]string{"guest", "member", "admin", "owner"}[r] }

type Kind string

// Values are the Rust Debug names; they appear in the core.idempotency `actor` column.
const (
	KindUser   Kind = "User"
	KindAgent  Kind = "Agent"
	KindAPIKey Kind = "ApiKey"
	KindSystem Kind = "System"
)

const NilUUID = "00000000-0000-0000-0000-000000000000"

type Principal struct {
	Kind        Kind
	UserID      *string
	AgentID     *string
	WorkspaceID string
	Role        Role
	Scopes      []string // nil = every group the role allows
}

func (p Principal) IsMachine() bool { return p.Kind == KindAgent || p.Kind == KindAPIKey }

func orNil(s *string) string {
	if s == nil {
		return NilUUID
	}
	return *s
}

// Actor is (actor_type, actor_id, on_behalf_of) for the event log.
func (p Principal) Actor() (string, *string, *string) {
	switch p.Kind {
	case KindAgent:
		return "agent", p.AgentID, p.UserID
	case KindSystem:
		return "system", nil, p.UserID
	}
	return "user", p.UserID, nil
}
