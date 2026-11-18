package reminders

import (
	"encoding/json"
	"fmt"

	"github.com/cnote/api/internal/command"
)

// Step is one reminder. There are always five, in this order; the owner can switch each off and change its days.
type Step struct {
	Kind    string `json:"kind"` // before | on | after
	Days    int    `json:"days"`
	Event   string `json:"event"` // the email_template event the message uses
	Enabled bool   `json:"enabled"`
}

type Settings struct {
	Enabled bool   `json:"enabled"`
	Steps   []Step `json:"steps"`
}

var fixed = [5]struct{ kind, event string }{
	{"before", "invoice.reminder_due_soon"}, {"on", "invoice.reminder_due_today"},
	{"after", "invoice.reminder_1"}, {"after", "invoice.reminder_2"}, {"after", "invoice.reminder_3"},
}

// Defaults: off until the owner turns it on (customers get email), 3 days before, on the day, 3/7 days after, 15 off.
func Defaults() Settings {
	days := [5]int{3, 0, 3, 7, 15}
	s := Settings{Steps: []Step{}}
	for i, f := range fixed {
		s.Steps = append(s.Steps, Step{f.kind, days[i], f.event, i < 4})
	}
	return s
}

func (s Settings) validate() error {
	if len(s.Steps) != 5 {
		return command.ErrInvalid("steps must list the five reminders")
	}
	prev := 0
	for i, st := range s.Steps {
		if st.Kind != fixed[i].kind || st.Event != fixed[i].event {
			return command.ErrInvalid(fmt.Sprintf("step %d must be kind %s with event %s", i+1, fixed[i].kind, fixed[i].event))
		}
		switch st.Kind {
		case "on":
			if st.Days != 0 {
				return command.ErrInvalid("the due-date step has days 0")
			}
		case "before":
			if st.Days < 1 || st.Days > 30 {
				return command.ErrInvalid("days before due must be 1 to 30")
			}
		default:
			if st.Days < 1 || st.Days > 120 || st.Days <= prev {
				return command.ErrInvalid("days overdue must be 1 to 120 and increase from one reminder to the next")
			}
			prev = st.Days
		}
	}
	return nil
}

// offset is the step's day relative to the due date: negative before, 0 on, positive after.
func (st Step) offset() int {
	if st.Kind == "before" {
		return -st.Days
	}
	return st.Days
}

func (st Step) label() string {
	switch {
	case st.Kind == "on":
		return "On the due date"
	case st.Kind == "before" && st.Days == 1:
		return "1 day before due"
	case st.Kind == "before":
		return fmt.Sprintf("%d days before due", st.Days)
	case st.Days == 1:
		return "1 day overdue"
	}
	return fmt.Sprintf("%d days overdue", st.Days)
}

// loadSettings reads workspaces.settings.payment_reminders; a missing or invalid value is the defaults.
func loadSettings(c *command.Ctx) (Settings, error) {
	var raw []byte
	if err := c.Tx.QueryRow(c.Context, `select coalesce(settings->'payment_reminders','null'::jsonb) from core.workspaces where id=$1::uuid`, c.WorkspaceID()).Scan(&raw); err != nil {
		return Settings{}, command.DB(err)
	}
	var s Settings
	if json.Unmarshal(raw, &s) != nil || s.validate() != nil {
		return Defaults(), nil
	}
	return s, nil
}

func settingsGet(c *command.Ctx, _ json.RawMessage) (any, error) { return loadSettings(c) }

func settingsSet(c *command.Ctx, b json.RawMessage) (any, error) {
	var s Settings
	if json.Unmarshal(b, &s) != nil {
		return nil, command.ErrInvalid("input must be {enabled, steps:[{kind, days, event, enabled}]}")
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(s)
	if _, err := c.Tx.Exec(c.Context, `update core.workspaces set settings = settings || jsonb_build_object('payment_reminders', $2::jsonb) where id=$1::uuid`, c.WorkspaceID(), out); err != nil {
		return nil, command.DB(err)
	}
	if _, err := c.Emit("reminder.settings_updated", nil, map[string]any{"enabled": s.Enabled}); err != nil {
		return nil, err
	}
	return s, nil
}
