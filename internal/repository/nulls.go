package repository

import "database/sql"

// nullStr maps "" to SQL NULL.
func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

// nullID maps 0 to SQL NULL.
func nullID(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: v != 0} }
