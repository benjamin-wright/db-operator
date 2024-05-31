package types

type HasID interface {
	GetID() string
}

type HasStorage interface {
	HasID
	GetStorage() string
}

type Readyable interface {
	HasID
	IsReady() bool
}

type Targetable interface {
	HasID
	GetTargetID() string
}
