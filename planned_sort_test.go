package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/samber/lo"
)

func Test_planSort(t *testing.T) {
	input := "a,b,c,d,e,f,g,h,i,j,k"

	// This test is based on randomness!
	// Not optimal since there is a tiny chance it passes when there is a problem
	list := lo.Shuffle(strings.Split(input, ","))

	type itemWithIndex struct {
		item  string
		index int
	}

	listWithI := lo.Map(list, func(item string, index int) itemWithIndex {
		return itemWithIndex{
			item:  item,
			index: index,
		}
	})

	slices.SortFunc(listWithI, func(a itemWithIndex, b itemWithIndex) int {
		if a.item < b.item {
			return -1
		} else if a.item == b.item {
			return 0
		} else if a.item > b.item {
			return 1
		}
		panic("nope")
	})

	actions := lo.Map(listWithI, func(item itemWithIndex, newIdx int) sortAction {
		return sortAction{
			from: item.index,
			to:   newIdx,
		}
	})

	planSort(actions)

	actions = lo.Filter(actions, func(a sortAction, _ int) bool {
		// Both of these cases would be a noop.
		return a.from != a.to-1 && a.from != a.to
	})

	fmt.Println("test case:", strings.Join(list, ","))

	listApplied := applyPlan(actions, list)

	got := strings.Join(listApplied, ",")
	if got != input {
		t.Fatal(got)
	}
}
