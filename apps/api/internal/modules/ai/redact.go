package ai

import "regexp"

// PII redaction before text leaves for an external model (workspace setting ai_redact_pii).
// shortcut: Go's \b and \d are ASCII-only (Rust's are Unicode), so identifiers glued to non-ASCII letters or
// digits can differ; upgrade with a hand-written boundary check if that ever matters.
var rules = []struct {
	re  *regexp.Regexp
	rep string
}{
	{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}`), "[EMAIL]"},
	{regexp.MustCompile(`\b\d{2}[A-Z]{5}\d{4}[A-Z][1-9A-Z]Z[0-9A-Z]\b`), "[GSTIN]"},
	{regexp.MustCompile(`\b[A-Z]{5}\d{4}[A-Z]\b`), "[PAN]"},
	{regexp.MustCompile(`\b[2-9]\d{3}[ -]?\d{4}[ -]?\d{4}\b`), "[AADHAAR]"},
	{regexp.MustCompile(`(?:\+91[ -]?)?\b[6-9]\d{4}[ -]?\d{5}\b`), "[PHONE]"},
	{regexp.MustCompile(`\+\d{1,3}[ -]?\d{6,12}\b`), "[PHONE]"},
}

func Redact(s string) string {
	for _, r := range rules {
		s = r.re.ReplaceAllLiteralString(s, r.rep)
	}
	return s
}
