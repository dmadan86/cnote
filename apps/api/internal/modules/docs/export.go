package docs

import "time"

// Exported wrappers so other modules (customer messages) format money and dates exactly like document templates.

// Money formats minor units: Indian grouping with the rupee sign for INR, otherwise "USD 1,234.50".
func Money(minor int64, currency string) string { return money(minor, currency) }

// Date formats a date as "30 Nov 2026".
func Date(t time.Time) string { return date(t) }

// OptDate is Date, or "" when nil.
func OptDate(t *time.Time) string { return optDate(t) }

// TodayIST is the current instant shifted to India time (read its date parts as UTC).
func TodayIST() time.Time { return todayIST() }
