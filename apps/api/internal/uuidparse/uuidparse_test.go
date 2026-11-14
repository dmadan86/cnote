package uuidparse

import "testing"

// Expected texts were read from the Rust server (uuid crate) and are compared live by TestUUIDErrorsMatchRust.
func TestParse(t *testing.T) {
	z := "00000000-0000-0000-0000-000000000000"
	for in, want := range map[string]string{z: z, "00000000000000000000000000000000": z, "{" + z + "}": z, "urn:uuid:" + z: z,
		"ABCDEF00-0000-0000-0000-000000000000": "abcdef00-0000-0000-0000-000000000000"} {
		if got, bad := Parse(in); bad != "" || got != want {
			t.Errorf("%q: %q %q", in, got, bad)
		}
	}
	for in, want := range map[string]string{
		"zz": "invalid character: found `z` at 0", "": "invalid length: found 0", "00000000-0000-0000-0000-00000000000": "invalid group length in group 4: expected 12, found 11",
		"00000000-000-00000-0000-000000000000": "invalid group length in group 1: expected 4, found 3", "00000000-0000-0000-0000": "invalid group count: expected 5, found 4",
		"0000000000000000000000000000000z": "invalid character: found `z` at 31", "é": "invalid character: found `é` at 0", "{" + z: "invalid character: found `{` at 0",
	} {
		if _, bad := Parse(in); bad != want {
			t.Errorf("%q: got %q want %q", in, bad, want)
		}
	}
}
