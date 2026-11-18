package sessiondb

// NullString and NullInt64 have the exact layout of database/sql's types, so
// session metadata rows cross the core protocol through a plain conversion.
type NullString struct {
	String string
	Valid  bool
}
type NullInt64 struct {
	Int64 int64
	Valid bool
}
