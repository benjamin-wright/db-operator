package bucket

import (
	"github.com/benjamin-wright/db-operator/internal/state/types"
	"github.com/benjamin-wright/db-operator/pkg/k8s_generic"
	"github.com/rs/zerolog/log"
)

type Bucket[T any, PT types.Nameable[T]] struct {
	state map[string]T
}

func NewBucket[T any, PT types.Nameable[T]]() Bucket[T, PT] {
	return Bucket[T, PT]{
		state: map[string]T{},
	}
}

func (b *Bucket[T, PT]) Apply(update k8s_generic.Update[T]) {
	for _, toRemove := range update.ToRemove {
		log.Info().Interface("toRemove", toRemove).Msg("Removing")
		b.Remove(toRemove)
	}

	for _, toAdd := range update.ToAdd {
		log.Info().Interface("toAdd", toAdd).Msg("Adding")
		b.Add(toAdd)
	}
}

func (b *Bucket[T, PT]) Add(obj T) {
	ptr := PT(&obj)
	key := ptr.GetName() + ":" + ptr.GetNamespace()

	b.state[key] = obj
}

func (b *Bucket[T, PT]) Remove(obj T) {
	ptr := PT(&obj)
	key := ptr.GetName() + ":" + ptr.GetNamespace()

	delete(b.state, key)
}

func (b *Bucket[T, PT]) Get(name string, namespace string) (T, bool) {
	value, ok := b.state[name+":"+namespace]
	return value, ok
}

func (b *Bucket[T, PT]) Clear() {
	b.state = map[string]T{}
}

func (b *Bucket[T, PT]) List() []T {
	result := []T{}

	for _, v := range b.state {
		result = append(result, v)
	}

	return result
}
