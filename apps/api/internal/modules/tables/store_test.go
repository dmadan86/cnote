package tables

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"

	"github.com/cnote/api/internal/command"
	"github.com/cnote/api/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// baseRole against the real database: rows are seeded with DATABASE_ADMIN_URL (RLS does not apply to it) and the
// resolver runs as cnote_app in a tenant transaction, exactly like a command.
type fixture struct {
	t                         *testing.T
	app                       *pgxpool.Pool
	ws                        string
	openBase, privBase, trash string
	users                     map[string]string
}

func seed(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	adminURL, appURL := os.Getenv("DATABASE_ADMIN_URL"), os.Getenv("DATABASE_URL")
	if adminURL == "" || appURL == "" {
		t.Fatal("DATABASE_URL and DATABASE_ADMIN_URL are required (load the repo .env)")
	}
	admin, err := db.Connect(ctx, adminURL, 2)
	if err != nil {
		t.Fatal(err)
	}
	app, err := db.Connect(ctx, appURL, 2)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, app: app, ws: newID(), users: map[string]string{}}
	x := func(q string, a ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, q, a...); err != nil {
			t.Fatal(err)
		}
	}
	x(`insert into core.workspaces(id,name,slug) values ($1,'t',$2)`, f.ws, "tables-"+f.ws)
	// name -> workspace role
	for name, role := range map[string]string{"owner": "owner", "admin": "admin", "editor": "member", "viewer": "member",
		"spaceadmin": "member", "outsider": "member", "guestin": "guest", "guestout": "guest"} {
		id := newID()
		f.users[name] = id
		x(`insert into core.users(id,email,name) values ($1,$2,$3)`, id, name+"-"+id+"@example.test", name)
		x(`insert into core.memberships(workspace_id,user_id,role) values ($1,$2,$3)`, f.ws, id, role)
	}
	open, priv := newID(), newID()
	x(`insert into core.spaces(id,workspace_id,key,name,visibility) values ($1,$3,'OPEN','open','open'),($2,$3,'PRIV','priv','private')`, open, priv, f.ws)
	member := func(space, user, role string) {
		x(`insert into core.space_members(workspace_id,space_id,user_id,role) values ($1,$2,$3,$4)`, f.ws, space, f.users[user], role)
	}
	member(open, "viewer", "viewer")
	member(open, "spaceadmin", "admin")
	member(open, "guestin", "editor") // a guest keeps read even with an editor seat
	member(priv, "editor", "editor")
	member(priv, "guestin", "viewer")
	f.openBase, f.privBase, f.trash = newID(), newID(), newID()
	x(`insert into tables.bases(id,workspace_id,space_id,name) values ($1,$3,$4,'o'),($2,$3,$5,'p')`, f.openBase, f.privBase, f.ws, open, priv)
	x(`insert into tables.bases(id,workspace_id,space_id,name,deleted_at) values ($1,$2,$3,'d',now())`, f.trash, f.ws, open)
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `delete from core.workspaces where id=$1`, f.ws) // cascades to everything tenant-scoped
		for _, u := range f.users {
			_, _ = admin.Exec(ctx, `delete from core.users where id=$1`, u)
		}
		admin.Close()
		app.Close()
	})
	return f
}

// as runs fn as a person (or, with user "", as a machine with no person) in a tenant transaction.
func (f *fixture) as(user string, role command.Role, fn func(*command.Ctx)) {
	f.t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTenant(ctx, f.app, f.ws)
	if err != nil {
		f.t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	p := command.Principal{Kind: command.KindUser, WorkspaceID: f.ws, Role: role}
	if user != "" {
		id := f.users[user]
		p.UserID = &id
	} else {
		p.Kind = command.KindAPIKey
	}
	fn(&command.Ctx{Context: ctx, Tx: tx, Pool: f.app, Principal: p})
}

func TestBaseRole(t *testing.T) {
	f := seed(t)
	cases := []struct {
		name, user string
		role       command.Role
		open, priv Role
	}{
		{"workspace owner", "owner", command.Owner, RoleManage, RoleManage},
		{"workspace admin", "admin", command.Admin, RoleManage, RoleManage},
		{"space admin", "spaceadmin", command.Member, RoleManage, RoleNone},
		{"space viewer", "viewer", command.Member, RoleRead, RoleNone},
		{"member of an open space", "outsider", command.Member, RoleWrite, RoleNone},
		{"private space editor", "editor", command.Member, RoleWrite, RoleWrite},
		{"guest who is a member", "guestin", command.Guest, RoleRead, RoleRead},
		{"guest outside the space", "guestout", command.Guest, RoleNone, RoleNone},
		{"api key with no person", "", command.Member, RoleWrite, RoleNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.as(tc.user, tc.role, func(c *command.Ctx) {
				for base, want := range map[string]Role{f.openBase: tc.open, f.privBase: tc.priv} {
					if got, err := baseRole(c, base); err != nil || got != want {
						t.Errorf("base %s: got %q err %v, want %q", base, got, err, want)
					}
				}
			})
		})
	}
	f.as("owner", command.Owner, func(c *command.Ctx) {
		if _, err := baseRole(c, f.trash); err == nil {
			t.Error("a trashed base resolved")
		}
	})
}

func TestAssertBasesRejectsMixedIDs(t *testing.T) {
	f := seed(t)
	missing := newID()
	code := func(err error) command.Code {
		if ce, ok := err.(*command.Error); ok {
			return ce.Code
		}
		return ""
	}
	f.as("outsider", command.Member, func(c *command.Ctx) { // sees the open base, not the private one
		if err := assertBases(c, []string{f.openBase, f.openBase}, RoleWrite); err != nil {
			t.Errorf("own base twice: %v", err)
		}
		if err := assertBases(c, []string{f.openBase, f.privBase}, RoleRead); code(err) != command.NotFound {
			t.Errorf("private base mixed in: %v", err)
		}
		if err := assertBases(c, []string{f.openBase, missing}, RoleRead); code(err) != command.NotFound {
			t.Errorf("missing base mixed in: %v", err)
		}
		if err := assertBases(c, []string{f.openBase}, RoleManage); code(err) != command.Forbidden {
			t.Errorf("write only, manage asked: %v", err)
		}
	})
	f.as("viewer", command.Member, func(c *command.Ctx) {
		if err := assertBases(c, []string{f.openBase}, RoleWrite); code(err) != command.Forbidden {
			t.Errorf("viewer writing: %v", err)
		}
		if err := assertBases(c, []string{f.openBase}, RoleRead); err != nil {
			t.Errorf("viewer reading: %v", err)
		}
	})
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
