package main

import (
	"github.com/samber/lo"
)

type sortAction struct {
	from int
	to   int
}

func intervalWhenMoveDown(from, to int) func(int) bool {
	// from is smaller than to so it's ]from, to] ordered
	return func(from2 int) bool {
		return from2 > from && from2 <= to
	}
}

func intervalWhenMoveUp(from, to int) func(int) bool {
	// to is smaller than from so it's [to, from[ ordered
	return func(from2 int) bool {
		return from2 >= to && from2 < from
	}
}

// Since spotify does not to bulk re order I need to simulate moving stuff around
// because the From index used in the API call keeps changing when other stuff is moved.
// This function simulates this move and makes sure the From index when the API call is made
// takes into account all the moves that came before it.
func planSort(actions []sortAction) []sortAction {
	for i, a := range actions {
		modifier := 0 // do not move
		inInterval := func(int) bool { return false }

		if a.from > a.to { // this moved up
			modifier = 1 // others move down
			inInterval = intervalWhenMoveUp(a.from, a.to)
		} else if a.from < a.to { // this moved down
			modifier = -1 // others move up
			inInterval = intervalWhenMoveDown(a.from, a.to)
		}

		// Move all items in the interval ]from, to]
		// To is inclusive: it is also affected by our move action.
		// The item that was in the to position will be pushed away.
		for i2, a2 := range actions {
			if i2 > i && inInterval(a2.from) {
				a2.from += modifier
				actions[i2] = a2
			}
		}
	}
	return actions
}

func applyPlan[T any](actions []sortAction, list []T) []T {
	for _, a := range actions {
		//fmt.Printf("%d -> %d\n", a.from, a.to)
		list = lo.DropByIndex(lo.Splice(list, a.to, list[a.from]), a.from+lo.Ternary(a.from > a.to, +1, 0))
	}
	return list
}
