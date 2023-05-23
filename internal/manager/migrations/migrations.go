package migrations

type DBMigrations struct {
	migrations map[string]map[string]migrations
}

type migrations struct {
	migrations map[int64]string
	nextId     int64
}

func New() DBMigrations {
	return DBMigrations{
		migrations: map[string]map[string]migrations{},
	}
}

func (d *DBMigrations) init(db string, database string) {
	if _, ok := d.migrations[db]; !ok {
		d.migrations[db] = map[string]migrations{}
	}

	if _, ok := d.migrations[db][database]; !ok {
		d.migrations[db][database] = migrations{
			migrations: map[int64]string{},
			nextId:     1,
		}
	}
}

func (d *DBMigrations) AddRequest(db string, database string, index int64, query string) {
	d.init(db, database)
	d.migrations[db][database].migrations[index] = query
}

func (d *DBMigrations) AddApplied(db string, database string, index int64) {
	d.init(db, database)

	migrations := d.migrations[db][database]

	if migrations.nextId <= index {
		migrations.nextId = index + 1
	}

	d.migrations[db][database] = migrations
}

func (d *DBMigrations) GetDBs() []string {
	dbs := []string{}

	for db := range d.migrations {
		dbs = append(dbs, db)
	}

	return dbs
}

func (d *DBMigrations) GetDatabases(db string) []string {
	databases := []string{}

	for database := range d.migrations[db] {
		databases = append(databases, database)
	}

	return databases
}

func (d *DBMigrations) Next(db string, database string) bool {
	migrations := d.migrations[db][database]

	_, ok := migrations.migrations[migrations.nextId]
	return ok
}

func (d *DBMigrations) GetNextMigration(db string, database string) (string, int64) {
	migrations := d.migrations[db][database]

	index := migrations.nextId
	migration := migrations.migrations[index]

	migrations.nextId += 1
	d.migrations[db][database] = migrations

	return migration, index
}
