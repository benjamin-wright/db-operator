package state

import (
	"github.com/benjamin-wright/db-operator/internal/state/bucket"
	"github.com/benjamin-wright/db-operator/internal/state/types"
)

func NewDemandTarget[T types.Nameable, U types.Nameable](parent T, target U) DemandTarget[T, U] {
	return DemandTarget[T, U]{Parent: parent, Target: target}
}

type DemandTarget[T types.Nameable, U types.Nameable] struct {
	Parent T
	Target U
}

func (d DemandTarget[T, U]) GetName() string {
	return d.Target.GetName()
}

func (d DemandTarget[T, U]) GetNamespace() string {
	return d.Target.GetNamespace()
}

type Demand[T types.Nameable, U types.Nameable] struct {
	ToAdd    bucket.Bucket[DemandTarget[T, U]]
	ToRemove bucket.Bucket[U]
}

func NewDemand[T types.Nameable, U types.Nameable]() Demand[T, U] {
	return Demand[T, U]{
		ToAdd:    bucket.NewBucket[DemandTarget[T, U]](),
		ToRemove: bucket.NewBucket[U](),
	}
}

func NewInitializedDemand[T types.Nameable, U types.Nameable](toAdd []DemandTarget[T, U], toRemove []U) Demand[T, U] {
	d := NewDemand[T, U]()

	for _, obj := range toAdd {
		d.ToAdd.Add(obj)
	}

	for _, obj := range toRemove {
		d.ToRemove.Add(obj)
	}

	return d
}

func GetOneForOne[
	T types.Nameable,
	U types.Nameable,
](request bucket.Bucket[T], existing bucket.Bucket[U], transform func(T) U) Demand[T, U] {
	toAdd := bucket.NewBucket[DemandTarget[T, U]]()
	toRemove := bucket.NewBucket[U]()

	for _, obj := range request.List() {
		if _, ok := existing.Get(obj.GetName(), obj.GetNamespace()); !ok {
			toAdd.Add(DemandTarget[T, U]{Parent: obj, Target: transform(obj)})
		}
	}

	for _, obj := range existing.List() {
		if _, ok := request.Get(obj.GetName(), obj.GetNamespace()); !ok {
			toRemove.Add(obj)
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
](demand bucket.Bucket[T], existing bucket.Bucket[U], equals func(T, U) bool) []U {
	toRemove := []U{}

	for _, obj := range existing.List() {
		missing := true

		for _, ref := range demand.List() {
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
	demand bucket.Bucket[T],
	existing bucket.Bucket[U],
	transform func(T) U,
) Demand[T, U] {
	toAdd := bucket.NewBucket[DemandTarget[T, U]]()
	toRemove := bucket.NewBucket[U]()

	for _, db := range demand.List() {
		if ss, ok := existing.Get(db.GetName(), db.GetNamespace()); !ok {
			toAdd.Add(DemandTarget[T, U]{Parent: db, Target: transform(db)})
		} else {
			if db.GetStorage() != ss.GetStorage() {
				toRemove.Add(transform(db))
				toAdd.Add(DemandTarget[T, U]{Parent: db, Target: transform(db)})
			}
		}
	}

	for _, db := range existing.List() {
		if _, ok := demand.Get(db.GetName(), db.GetNamespace()); !ok {
			toRemove.Add(db)
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

func GetServiceBound[T types.Targetable, U types.Nameable, V types.Readyable](
	demand bucket.Bucket[T],
	existing bucket.Bucket[U],
	servers bucket.Bucket[V],
	transform func(T) U,
) Demand[T, U] {
	d := Demand[T, U]{
		ToAdd:    bucket.NewBucket[DemandTarget[T, U]](),
		ToRemove: bucket.NewBucket[U](),
	}

	seen := bucket.NewBucket[U]()

	for _, client := range demand.List() {
		ss, hasSS := servers.Get(client.GetTarget(), client.GetTargetNamespace())

		if !hasSS || !ss.IsReady() {
			continue
		}

		desired := transform(client)
		seen.Add(desired)

		if _, ok := existing.Get(desired.GetName(), desired.GetNamespace()); !ok {
			d.ToAdd.Add(DemandTarget[T, U]{Parent: client, Target: desired})
		}
	}

	for _, e := range existing.List() {
		if _, ok := seen.Get(e.GetName(), e.GetNamespace()); !ok {
			d.ToRemove.Add(e)
		}
	}

	return d
}
