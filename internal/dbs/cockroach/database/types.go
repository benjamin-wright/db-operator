package database

import "strconv"

type DBRef struct {
	Name      string
	Namespace string
}

type Database struct {
	Name string
	DB   DBRef
}

func (d *Database) GetName() string {
	return d.Name
}

func (d *Database) GetNamespace() string {
	return d.DB.Namespace + ":" + d.DB.Name
}

type User struct {
	Name string
	DB   DBRef
}

func (u *User) GetName() string {
	return u.Name
}

func (u *User) GetNamespace() string {
	return u.DB.Namespace + ":" + u.DB.Name
}

type Permission struct {
	User     string
	Database string
	DB       DBRef
}

func (u *Permission) GetName() string {
	return u.Database + u.User
}

func (u *Permission) GetNamespace() string {
	return u.DB.Namespace + ":" + u.DB.Name
}

type Migration struct {
	DB       DBRef
	Database string
	Index    int64
}

func (m *Migration) GetName() string {
	return m.Database + strconv.FormatInt(m.Index, 10)
}

func (m *Migration) GetNamespace() string {
	return m.DB.Namespace + ":" + m.DB.Name
}
