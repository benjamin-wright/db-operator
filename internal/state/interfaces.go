package state

type Nameable[T any] interface {
	*T
	GetName() string
}

type HasStorage[T any] interface {
	Nameable[T]
	GetStorage() string
}

type Readyable[T any] interface {
	Nameable[T]
	IsReady() bool
}

type Targetable[T any] interface {
	Nameable[T]
	GetTarget() string
}
