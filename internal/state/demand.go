package state

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
	PT Nameable[T],
	PU Nameable[U],
](request Bucket[T, PT], existing Bucket[U, PU], transform func(T) U) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for name, obj := range request.state {
		if _, ok := existing.state[name]; !ok {
			toAdd = append(toAdd, DemandTarget[T, U]{Parent: obj, Target: transform(obj)})
		}
	}

	for name, obj := range existing.state {
		if _, ok := request.state[name]; !ok {
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
	PT Nameable[T],
	PU Nameable[U],
](current Bucket[T, PT], existing Bucket[U, PU], equals func(T, U) bool) []U {
	toRemove := []U{}

	for _, obj := range existing.state {
		missing := true

		for _, ref := range current.state {
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
	PT HasStorage[T],
	PU HasStorage[U],
](
	current Bucket[T, PT],
	existing Bucket[U, PU],
	transform func(T) U,
) Demand[T, U] {
	toAdd := []DemandTarget[T, U]{}
	toRemove := []DemandTarget[T, U]{}

	for name, db := range current.state {
		if ss, ok := existing.state[name]; !ok {
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

	for name, db := range existing.state {
		if _, ok := current.state[name]; !ok {
			toRemove = append(toRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return Demand[T, U]{
		ToAdd:    toAdd,
		ToRemove: toRemove,
	}
}

func GetServiceBound[T any, U any, V any, PT Targetable[T], PU Nameable[U], PV Readyable[V]](
	current Bucket[T, PT],
	existing Bucket[U, PU],
	servers Bucket[V, PV],
	transform func(T) U,
) Demand[T, U] {
	d := Demand[T, U]{
		ToAdd:    []DemandTarget[T, U]{},
		ToRemove: []DemandTarget[T, U]{},
	}

	seen := map[string]U{}

	for _, client := range current.state {
		clientPtr := PT(&client)
		target := clientPtr.GetTarget()

		ss, hasSS := servers.state[target]

		ssPtr := PV(&ss)

		if !hasSS || !ssPtr.IsReady() {
			continue
		}

		desired := transform(client)
		name := PU(&desired).GetName()
		seen[name] = desired

		if _, ok := existing.state[name]; !ok {
			d.ToAdd = append(d.ToAdd, DemandTarget[T, U]{Parent: client, Target: desired})
		}
	}

	for current, db := range existing.state {
		if _, ok := seen[current]; !ok {
			d.ToRemove = append(d.ToRemove, DemandTarget[T, U]{Target: db})
		}
	}

	return d
}
