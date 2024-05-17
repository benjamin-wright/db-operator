package state

import (
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/internal/state/types"
)

type DemandTarget[T any, U any] struct {
	Parent T
	Target U
}

type Demand[T any, U any] struct {
	ToAdd    []DemandTarget[T, U]
	ToRemove []DemandTarget[T, U]
}

func GetOneForOne[
	T types.Nameable,
	U types.Nameable,
](request bucket.Bucket[T], existing bucket.Bucket[U], transform func(T) U) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for _, obj := range request.List() {
		if _, ok := existing.Get(obj.GetName(), obj.GetNamespace()); !ok {
			toAdd = append(toAdd, DemandTarget[T, U]{Parent: obj, Target: transform(obj)})
		}
	}

	for _, obj := range existing.List() {
		if _, ok := request.Get(obj.GetName(), obj.GetNamespace()); !ok {
			toRemove = append(toRemove, DemandTarget[T, U]{Target: obj})
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

func GetOrphaned[
	T types.Nameable,
	U types.Nameable,
](current bucket.Bucket[T], existing bucket.Bucket[U], equals func(T, U) bool) []U {
	toRemove := []U{}

	for _, obj := range existing.List() {
		missing := true

		for _, ref := range current.List() {
			if equals(ref, obj) {
				missing = false
				break
			}
		}

		if missing {
			toRemove = append(toRemove, obj)
		}
	}

	return toRemove
}

func GetStorageBound[
	T types.HasStorage,
	U types.HasStorage,
](
	current bucket.Bucket[T],
	existing bucket.Bucket[U],
	transform func(T) U,
) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for _, db := range current.List() {
		if ss, ok := existing.Get(db.GetName(), db.GetNamespace()); !ok {
			toAdd = append(toAdd, DemandTarget[T, U]{Parent: db, Target: transform(db)})
		} else {
			if db.GetStorage() != ss.GetStorage() {
				toRemove = append(toRemove, DemandTarget[T, U]{Parent: db, Target: transform(db)})
				toAdd = append(toAdd, DemandTarget[T, U]{Parent: db, Target: transform(db)})
			}
		}
	}

	for _, db := range existing.List() {
		if _, ok := current.Get(db.GetName(), db.GetNamespace()); !ok {
			toRemove = append(toRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

func GetServiceBound[T types.Targetable, U types.Nameable, V types.Readyable](
	current bucket.Bucket[T],
	existing bucket.Bucket[U],
	servers bucket.Bucket[V],
	transform func(T) U,
) Demand[T, U] {
	d := Demand[T, U]{
		ToAdd:    []DemandTarget[T, U]{},
		ToRemove: []DemandTarget[T, U]{},
	}

	seen := bucket.NewBucket[U]()

	for _, client := range current.List() {
		ss, hasSS := servers.Get(client.GetTarget(), client.GetTargetNamespace())

		if !hasSS || !ss.IsReady() {
			continue
		}

		desired := transform(client)
		seen.Add(desired)

		if _, ok := existing.Get(desired.GetName(), desired.GetNamespace()); !ok {
			d.ToAdd = append(d.ToAdd, DemandTarget[T, U]{Parent: client, Target: desired})
		}
	}

	for _, db := range existing.List() {
		if _, ok := seen.Get(db.GetName(), db.GetNamespace()); !ok {
			d.ToRemove = append(d.ToRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return d
}
