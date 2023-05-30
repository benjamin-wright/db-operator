package state

import (
	"go.uber.org/zap"
	"ponglehub.co.uk/db-operator/pkg/k8s_generic"
)

type Bucket[T any, PT Nameable[T]] struct {
	state map[string]T
}

func NewBucket[T any, PT Nameable[T]]() Bucket[T, PT] {
	return Bucket[T, PT]{
		state: map[string]T{},
	}
}

func (b *Bucket[T, PT]) Apply(update k8s_generic.Update[T]) {
	for _, toRemove := range update.ToRemove {
		zap.S().Infof("Removing %T %s", toRemove, PT(&toRemove).GetName())
		b.Remove(toRemove)
	}

	for _, toAdd := range update.ToAdd {
		zap.S().Infof("Adding %T %s", toAdd, PT(&toAdd).GetName())
		b.Add(toAdd)
	}
}

func (b *Bucket[T, PT]) Add(obj T) {
	ptr := PT(&obj)
	key := ptr.GetName()

	b.state[key] = obj
}

func (b *Bucket[T, PT]) Remove(obj T) {
	ptr := PT(&obj)
	key := ptr.GetName()

	delete(b.state, key)
}

func (b *Bucket[T, PT]) Clear() {
	b.state = map[string]T{}
}
