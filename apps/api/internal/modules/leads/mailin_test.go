package leads

import "testing"

func TestStripQuoted(t *testing.T) {
	for in, want := range map[string]string{
		"Thanks\n\nOn Mon, 3 Nov 2026 at 10:00, A <a@b.test> wrote:\n> old":       "Thanks",
		"Thanks\n\nOn Mon, 3 Nov 2026 at 10:00 Someone\n<a@b.test> wrote:\n> old": "Thanks",
		"Yes\n-----Original Message-----\nFrom: x":                                "Yes",
		"Yes\n> quoted": "Yes",
		"> only quoted": "> only quoted", // nothing above it: keep the text
		"plain":         "plain",
	} {
		if got := stripQuoted(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
