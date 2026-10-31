package db

import "testing"

// Reference lookups must fail closed: an unknown table or a query error cannot be
// interpreted as "the referenced row was deleted".
func TestRowExistsRejectsUnknownReferenceTable(t *testing.T) {
	if _, err := rowExists(nil, "users", 1); err == nil {
		t.Fatal("unsupported reference table must be an error, not false")
	}
}
