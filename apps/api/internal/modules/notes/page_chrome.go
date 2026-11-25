package notes

import (
	"bytes"
	"encoding/json"
	"unicode/utf8"

	"github.com/cnote/api/internal/command"
)

// pageChrome is the A1 part of note.update: status (null clears), icon (null or "" clears), wide.
type pageChrome struct {
	setStatus, setIcon bool
	status, icon       *string
	wide               *bool
}

func parsePageChrome(o obj) (pageChrome, error) {
	var p pageChrome
	if raw, ok := o["status"]; ok {
		p.setStatus = true
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var s string
			if json.Unmarshal(raw, &s) != nil || (s != "draft" && s != "in_review" && s != "final") {
				return p, command.ErrInvalid("status must be draft, in_review, final or null")
			}
			p.status = &s
		}
	}
	if raw, ok := o["icon"]; ok {
		p.setIcon = true
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var s string
			if json.Unmarshal(raw, &s) != nil || utf8.RuneCountInString(s) > 16 {
				return p, command.ErrInvalid("icon must be one emoji (at most 16 characters)")
			}
			if s != "" {
				p.icon = &s
			}
		}
	}
	if raw, ok := o.get("wide"); ok {
		var b bool
		if json.Unmarshal(raw, &b) != nil {
			return p, command.ErrInvalid("wide must be true or false")
		}
		p.wide = &b
	}
	return p, nil
}
