package database

import "strconv"

type Cluster struct {
	Name      string
	Namespace string
}

type Database struct {
	Name    string
	Owner   string
	Cluster Cluster
}

func (d Database) GetName() string {
	return d.Name
}

func (d Database) GetNamespace() string {
	return d.Cluster.Namespace + ":" + d.Cluster.Name
}

type User struct {
	Name     string
	Password string
	Cluster  Cluster
}

func (u User) GetName() string {
	return u.Name
}

func (u User) GetNamespace() string {
	return u.Cluster.Namespace + ":" + u.Cluster.Name
}

type Permission struct {
	User     string
	Database string
	Cluster  Cluster
}

func (u Permission) GetName() string {
	return u.Database + u.User
}

func (u Permission) GetNamespace() string {
	return u.Cluster.Namespace + ":" + u.Cluster.Name
}

type Migration struct {
	Cluster  Cluster
	Database string
	Index    int64
}

func (m Migration) GetName() string {
	return m.Database + strconv.FormatInt(m.Index, 10)
}

func (m Migration) GetNamespace() string {
	return m.Cluster.Namespace + ":" + m.Cluster.Name
}
