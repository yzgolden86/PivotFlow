package app

import (
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func rrKeys(indices ...int) []*model.APIKey {
	keys := make([]*model.APIKey, 0, len(indices))
	for _, index := range indices {
		keys = append(keys, &model.APIKey{
			KeyIndex:    index,
			APIKey:      "sk-test",
			KeyStrategy: model.KeyStrategyRoundRobin,
		})
	}
	return keys
}

// The scope must be order-independent so the same candidate subset always maps
// to one cursor regardless of how the caller happened to sort the keys.
func TestNewRRCounterScopeIsOrderIndependent(t *testing.T) {
	a := newRRCounterScope(7, rrKeys(0, 1, 2))
	b := newRRCounterScope(7, rrKeys(2, 0, 1))
	if a != b {
		t.Fatalf("scope differs by input order: %+v vs %+v", a, b)
	}
}

// Different candidate subsets on one channel must get separate cursors: sharing
// one cursor lets a subset advance another subset's position, so each subset
// only ever reaches its own leading keys.
func TestNewRRCounterScopeSeparatesKeySubsets(t *testing.T) {
	full := newRRCounterScope(7, rrKeys(0, 1, 2))
	partial := newRRCounterScope(7, rrKeys(0, 1))
	if full == partial {
		t.Fatal("different key subsets must not share a cursor")
	}
}

func TestNewRRCounterScopeSeparatesChannels(t *testing.T) {
	if newRRCounterScope(7, rrKeys(0, 1)) == newRRCounterScope(8, rrKeys(0, 1)) {
		t.Fatal("different channels must not share a cursor")
	}
}

// Duplicate indices must collapse, otherwise a caller passing the same key twice
// would land on a different cursor than the deduplicated equivalent.
func TestNewRRCounterScopeDedupesIndices(t *testing.T) {
	if newRRCounterScope(7, rrKeys(0, 1, 1)) != newRRCounterScope(7, rrKeys(0, 1)) {
		t.Fatal("duplicate key indices must collapse to one scope")
	}
}

// Nil entries must be skipped rather than panicking or shifting the key set.
func TestNewRRCounterScopeSkipsNilKeys(t *testing.T) {
	keys := rrKeys(0, 1)
	withNil := []*model.APIKey{keys[0], nil, keys[1]}
	if newRRCounterScope(7, withNil) != newRRCounterScope(7, keys) {
		t.Fatal("nil keys must not affect the scope")
	}
}

// Regression for the shared-cursor bug: two model groups on one channel, each
// with its own key subset, must both rotate through all of their own keys.
// With a channel-only cursor the two groups advance each other and each one
// keeps landing on the same key.
func TestSelectAvailableKeyRotatesIndependentlyPerKeySubset(t *testing.T) {
	ks := NewKeySelector()

	groupA := rrKeys(0, 1)       // e.g. models served by keys 0 and 1
	groupB := rrKeys(10, 11, 12) // a different model group

	seenA := make(map[int]int)
	seenB := make(map[int]int)

	// Interleave the two groups, which is what concurrent traffic looks like.
	for range 60 {
		indexA, _, err := ks.SelectAvailableKey(7, groupA, nil)
		if err != nil {
			t.Fatalf("SelectAvailableKey(groupA) failed: %v", err)
		}
		seenA[indexA]++

		indexB, _, err := ks.SelectAvailableKey(7, groupB, nil)
		if err != nil {
			t.Fatalf("SelectAvailableKey(groupB) failed: %v", err)
		}
		seenB[indexB]++
	}

	for _, index := range []int{0, 1} {
		if seenA[index] == 0 {
			t.Errorf("group A never used key %d: %v", index, seenA)
		}
	}
	for _, index := range []int{10, 11, 12} {
		if seenB[index] == 0 {
			t.Errorf("group B never used key %d: %v", index, seenB)
		}
	}
	t.Logf("group A distribution: %v", seenA)
	t.Logf("group B distribution: %v", seenB)
}
