package migrations

type AppliedMigration struct {
	Index int
	Hash  string
}

type Migration struct {
	Index int
	Query string
}
