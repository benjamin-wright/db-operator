package types

type Nameable[T any] interface {
	*T
	GetName() string
	GetNamespace() string
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
	GetTargetNamespace() string
}
