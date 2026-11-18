// Package dataimport is the pure parsing and validation layer for the importers: CSV reading, column auto-mapping,
// typed row validation (contacts, items, opening documents) and the TallyPrime/ERP 9 masters XML parser.
// It does no I/O beyond the bytes it is given and imports no other C Note package.
package dataimport

// Kind names a CSV import.
type Kind string

const (
	KindContacts    Kind = "contacts"
	KindItems       Kind = "items"
	KindOpeningDocs Kind = "opening_docs"
)

// Limits shared by CSV and XML input.
const (
	MaxBytes = 10 << 20
	MaxRows  = 5000
)
