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
	T any,
	U any,
	PT types.Nameable[T],
	PU types.Nameable[U],
](request bucket.Bucket[T, PT], existing bucket.Bucket[U, PU], transform func(T) U) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for _, obj := range request.List() {
		ptr := PT(&obj)
		if _, ok := existing.Get(ptr.GetName(), ptr.GetNamespace()); !ok {
			toAdd = append(toAdd, DemandTarget[T, U]{Parent: obj, Target: transform(obj)})
		}
	}

	for _, obj := range existing.List() {
		ptr := PU(&obj)
		if _, ok := request.Get(ptr.GetName(), ptr.GetNamespace()); !ok {
			toRemove = append(toRemove, DemandTarget[T, U]{Target: obj})
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

func GetOrphaned[
	T any,
	U any,
	PT types.Nameable[T],
	PU types.Nameable[U],
](current bucket.Bucket[T, PT], existing bucket.Bucket[U, PU], equals func(T, U) bool) []U {
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
	T any,
	U any,
	PT types.HasStorage[T],
	PU types.HasStorage[U],
](
	current bucket.Bucket[T, PT],
	existing bucket.Bucket[U, PU],
	transform func(T) U,
) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for _, db := range current.List() {
		ptr := PT(&db)
		if ss, ok := existing.Get(ptr.GetName(), ptr.GetNamespace()); !ok {
			toAdd = append(toAdd, DemandTarget[T, U]{Parent: db, Target: transform(db)})
		} else {
			dbPtr := PT(&db)
			ssPtr := PU(&ss)

			if dbPtr.GetStorage() != ssPtr.GetStorage() {
				toRemove = append(toRemove, DemandTarget[T, U]{Parent: db, Target: transform(db)})
				toAdd = append(toAdd, DemandTarget[T, U]{Parent: db, Target: transform(db)})
			}
		}
	}

	for _, db := range existing.List() {
		ptr := PU(&db)
		if _, ok := current.Get(ptr.GetName(), ptr.GetNamespace()); !ok {
			toRemove = append(toRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

func GetServiceBound[T any, U any, V any, PT types.Targetable[T], PU types.Nameable[U], PV types.Readyable[V]](
	current bucket.Bucket[T, PT],
	existing bucket.Bucket[U, PU],
	servers bucket.Bucket[V, PV],
	transform func(T) U,
) Demand[T, U] {
	d := Demand[T, U]{
		ToAdd:    []DemandTarget[T, U]{},
		ToRemove: []DemandTarget[T, U]{},
	}

	seen := bucket.NewBucket[U, PU]()

	for _, client := range current.List() {
		clientPtr := PT(&client)

		ss, hasSS := servers.Get(clientPtr.GetTarget(), clientPtr.GetTargetNamespace())
		ssPtr := PV(&ss)

		if !hasSS || !ssPtr.IsReady() {
			continue
		}

		desired := transform(client)
		seen.Add(desired)

		desiredPtr := PU(&desired)
		if _, ok := existing.Get(desiredPtr.GetName(), desiredPtr.GetNamespace()); !ok {
			d.ToAdd = append(d.ToAdd, DemandTarget[T, U]{Parent: client, Target: desired})
		}
	}

	for _, db := range existing.List() {
		ptr := PU(&db)
		if _, ok := seen.Get(ptr.GetName(), ptr.GetNamespace()); !ok {
			d.ToRemove = append(d.ToRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return d
}
