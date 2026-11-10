//! PII redaction applied before text leaves for an external model (workspace setting `ai_redact_pii`).
//! Pattern-based, so it is best effort: it catches well-formed identifiers, not free-text names.

use regex::Regex;
use std::sync::LazyLock;

// Order matters: GSTIN embeds a PAN, and a 12-digit Aadhaar would otherwise match as a phone number.
static RULES: LazyLock<Vec<(Regex, &'static str)>> = LazyLock::new(|| {
    [
        (
            r"[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}",
            "[EMAIL]",
        ),
        (r"\b\d{2}[A-Z]{5}\d{4}[A-Z][1-9A-Z]Z[0-9A-Z]\b", "[GSTIN]"),
        (r"\b[A-Z]{5}\d{4}[A-Z]\b", "[PAN]"),
        (r"\b[2-9]\d{3}[ -]?\d{4}[ -]?\d{4}\b", "[AADHAAR]"),
        (r"(?:\+91[ -]?)?\b[6-9]\d{4}[ -]?\d{5}\b", "[PHONE]"),
        (r"\+\d{1,3}[ -]?\d{6,12}\b", "[PHONE]"),
    ]
    .into_iter()
    .map(|(p, r)| (Regex::new(p).expect("static regex"), r))
    .collect()
});

pub fn redact(text: &str) -> String {
    RULES.iter().fold(text.to_string(), |acc, (re, rep)| {
        re.replace_all(&acc, *rep).into_owned()
    })
}

#[cfg(test)]
mod tests {
    use super::redact;

    #[test]
    fn redacts_indian_identifiers() {
        let s = "Mail asha.k+bills@example.co.in, call +91 98765 43210 or 9876543210. \
                 GSTIN 29ABCDE1234F1Z5, PAN ABCDE1234F, Aadhaar 2345 6789 0123 and 234567890123.";
        let out = redact(s);
        assert_eq!(
            out,
            "Mail [EMAIL], call [PHONE] or [PHONE]. GSTIN [GSTIN], PAN [PAN], Aadhaar [AADHAAR] and [AADHAAR]."
        );
    }

    #[test]
    fn leaves_ordinary_text_alone() {
        let s = "Invoice INV-2026-27/0042 totals 1,18,000 for the Q3 launch (v2.4.1).";
        assert_eq!(redact(s), s);
    }

    #[test]
    fn international_numbers() {
        assert_eq!(redact("ring +44 2079460958 now"), "ring [PHONE] now");
    }
}
