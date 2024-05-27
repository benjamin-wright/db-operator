package types

type Nameable interface {
	GetName() string
	GetNamespace() string
}

type HasStorage interface {
	Nameable
	GetStorage() string
}

type Readyable interface {
	Nameable
	IsReady() bool
}

type Targetable interface {
	Nameable
	GetTarget() string
	GetTargetNamespace() string
}
