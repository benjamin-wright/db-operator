package state

import (
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type nameable[T any] interface {
	*T
	GetName() string
}

type bucket[T any, PT nameable[T]] struct {
	state map[string]T
}

func newBucket[T any, PT nameable[T]]() bucket[T, PT] {
	return bucket[T, PT]{
		state: map[string]T{},
	}
}

func (b *bucket[T, PT]) apply(update k8s_generic.Update[T]) {
	for _, toRemove := range update.ToRemove {
		zap.S().Infof("Removing %T %s", toRemove, PT(&toRemove).GetName())
		b.remove(toRemove)
	}

	for _, toAdd := range update.ToAdd {
		zap.S().Infof("Adding %T %s", toAdd, PT(&toAdd).GetName())
		b.add(toAdd)
	}
}

func (b *bucket[T, PT]) add(obj T) {
	ptr := PT(&obj)
	key := ptr.GetName()

	b.state[key] = obj
}

func (b *bucket[T, PT]) remove(obj T) {
	ptr := PT(&obj)
	key := ptr.GetName()

	delete(b.state, key)
}

func (b *bucket[T, PT]) clear() {
	b.state = map[string]T{}
}
