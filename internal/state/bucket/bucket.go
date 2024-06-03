package bucket

import (
	"github.com/benjamin-wright/db-operator/v2/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type HasID interface {
	GetID() string
}

type Bucket[T HasID] struct {
	state map[string]T
}

func NewBucket[T HasID]() Bucket[T] {
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
	b.state[obj.GetID()] = obj
}

func (b *Bucket[T]) Remove(obj T) {
	delete(b.state, obj.GetID())
}

func (b *Bucket[T]) Get(id string) (T, bool) {
	value, ok := b.state[id]
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
