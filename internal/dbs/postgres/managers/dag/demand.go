package dag

type identifiable interface {
	GetName() string
	GetNamespace() string
}

type demand[T identifiable] struct {
	required T
	actual   T
	exists   bool
}

func (d demand[T]) GetName() string {
	return d.required.GetName()
}

func (d demand[T]) GetNamespace() string {
	return d.required.GetNamespace()
}
