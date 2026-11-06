package platform

import "slices"

// Find is the row with id, for tests; production code looks rows up with slices.IndexFunc and
// handles an id it does not know (a state file from another makeit version).
func Find(id string) Prereq {
	i := slices.IndexFunc(Prereqs, func(r Prereq) bool { return r.ID == id })
	if i < 0 {
		panic("platform: no prerequisite " + id)
	}
	return Prereqs[i]
}
