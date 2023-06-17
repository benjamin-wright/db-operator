package state

type DBMigrations struct {
	migrations map[string]map[string]map[string]migrations
}

type migrations struct {
	migrations map[int64]string
	nextId     int64
}

func NewMigrations() DBMigrations {
	return DBMigrations{
		migrations: map[string]map[string]map[string]migrations{},
	}
}

func (d *DBMigrations) init(namespace string, db string, database string) {
	if _, ok := d.migrations[namespace]; !ok {
		d.migrations[namespace] = map[string]map[string]migrations{}
	}

	if _, ok := d.migrations[namespace][db]; !ok {
		d.migrations[namespace][db] = map[string]migrations{}
	}

	if _, ok := d.migrations[namespace][db][database]; !ok {
		d.migrations[namespace][db][database] = migrations{
			migrations: map[int64]string{},
			nextId:     1,
		}
	}
}

func (d *DBMigrations) AddRequest(namespace string, db string, database string, index int64, query string) {
	d.init(namespace, db, database)
	d.migrations[namespace][db][database].migrations[index] = query
}

func (d *DBMigrations) AddApplied(namespace string, db string, database string, index int64) {
	d.init(namespace, db, database)

	migrations := d.migrations[namespace][db][database]

	if migrations.nextId <= index {
		migrations.nextId = index + 1
	}

	d.migrations[namespace][db][database] = migrations
}

func (d *DBMigrations) GetNamespaces() []string {
	namespaces := []string{}

	for namespace := range d.migrations {
		namespaces = append(namespaces, namespace)
	}

	return namespaces
}

func (d *DBMigrations) GetDBs(namespace string) []string {
	dbs := []string{}

	for db := range d.migrations[namespace] {
		dbs = append(dbs, db)
	}

	return dbs
}

func (d *DBMigrations) GetDatabases(namespace string, db string) []string {
	databases := []string{}

	for database := range d.migrations[namespace][db] {
		databases = append(databases, database)
	}

	return databases
}

func (d *DBMigrations) Next(namespace string, db string, database string) bool {
	migrations := d.migrations[namespace][db][database]

	_, ok := migrations.migrations[migrations.nextId]
	return ok
}

func (d *DBMigrations) GetNextMigration(namespace string, db string, database string) (string, int64) {
	migrations := d.migrations[namespace][db][database]

	index := migrations.nextId
	migration := migrations.migrations[index]

	migrations.nextId += 1
	d.migrations[namespace][db][database] = migrations

	return migration, index
}
