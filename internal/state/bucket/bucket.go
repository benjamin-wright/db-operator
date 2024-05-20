package bucket

import (
	"github.com/benjamin-wright/db-operator/internal/state/types"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type Bucket[T types.Nameable] struct {
	state map[string]T
}

func NewBucket[T types.Nameable]() Bucket[T] {
	return Bucket[T]{
		state: map[string]T{},
	}
}

func (b *Bucket[T]) Apply(update k8s_generic.Update[T]) {
	for _, toRemove := range update.ToRemove {
		log.Debug().Interface("toRemove", toRemove).Msg("Removing")
		b.Remove(toRemove)
	}

	for _, toAdd := range update.ToAdd {
		log.Debug().Interface("toAdd", toAdd).Msg("Adding")
		b.Add(toAdd)
	}
}

func (b *Bucket[T]) Add(obj T) {
	key := obj.GetName() + ":" + obj.GetNamespace()

	b.state[key] = obj
}

func (b *Bucket[T]) Remove(obj T) {
	key := obj.GetName() + ":" + obj.GetNamespace()

	delete(b.state, key)
}

func (b *Bucket[T]) Get(name string, namespace string) (T, bool) {
	value, ok := b.state[name+":"+namespace]
	return value, ok
}

func (b *Bucket[T]) Clear() {
	b.state = map[string]T{}
}

func (b *Bucket[T]) List() []T {
	result := []T{}

	for _, v := range b.state {
		result = append(result, v)
	}

	return result
}
