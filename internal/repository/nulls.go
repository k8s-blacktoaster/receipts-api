package repository

import "database/sql"

// nullStr maps "" to SQL NULL.
func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }
