package state

type DemandTarget[T any, U any] struct {
	Parent T
	Target U
}

type Demand[T any, U any] struct {
	ToAdd    []DemandTarget[T, U]
	ToRemove []DemandTarget[T, U]
}

func getOneForOneDemand[T any, U any](state map[string]T, existing map[string]U, transform func(T) U) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for name, obj := range state {
		if _, ok := existing[name]; !ok {
			toAdd = append(toAdd, DemandTarget[T, U]{Parent: obj, Target: transform(obj)})
		}
	}

	for name, obj := range existing {
		if _, ok := state[name]; !ok {
			toRemove = append(toRemove, DemandTarget[T, U]{Target: obj})
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

func getOrphanedDemand[T any, U any](state map[string]T, existing map[string]U, equals func(T, U) bool) []U {
	toRemove := []U{}

	for _, obj := range existing {
		missing := true

		for _, ref := range state {
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

type hasStorage[T any] interface {
	*T
	GetStorage() string
}

func getStorageBoundDemand[
	T any,
	U any,
	TP hasStorage[T],
	UP hasStorage[U],
](
	state map[string]T,
	existing map[string]U,
	transform func(T) U,
) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for name, db := range state {
		if ss, ok := existing[name]; !ok {
			toAdd = append(toAdd, DemandTarget[T, U]{Parent: db, Target: transform(db)})
		} else {
			dbPtr := TP(&db)
			ssPtr := UP(&ss)

			if dbPtr.GetStorage() != ssPtr.GetStorage() {
				toRemove = append(toRemove, DemandTarget[T, U]{Parent: db, Target: transform(db)})
				toAdd = append(toAdd, DemandTarget[T, U]{Parent: db, Target: transform(db)})
			}
		}
	}

	for name, db := range existing {
		if _, ok := state[name]; !ok {
			toRemove = append(toRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

type readyable[T any] interface {
	*T
	IsReady() bool
}

type targetable[T any] interface {
	nameable[T]
	GetTarget() string
}

func getServiceBoundDemand[T any, U any, V any, W any, PT targetable[T], PU nameable[U], PV readyable[V]](
	state map[string]T,
	existing map[string]U,
	servers map[string]V,
	services map[string]W,
	transform func(T) U,
) Demand[T, U] {
	d := Demand[T, U]{
		ToAdd:    []DemandTarget[T, U]{},
		ToRemove: []DemandTarget[T, U]{},
	}

	seen := map[string]U{}

	for _, client := range state {
		clientPtr := PT(&client)
		target := clientPtr.GetTarget()

		ss, hasSS := servers[target]
		_, hasSvc := services[target]

		ssPtr := PV(&ss)

		if !hasSS || !hasSvc || !ssPtr.IsReady() {
			continue
		}

		desired := transform(client)
		name := PU(&desired).GetName()
		seen[name] = desired

		if _, ok := existing[name]; !ok {
			d.ToAdd = append(d.ToAdd, DemandTarget[T, U]{Parent: client, Target: desired})
		}
	}

	for current, db := range existing {
		if _, ok := seen[current]; !ok {
			d.ToRemove = append(d.ToRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return d
}
