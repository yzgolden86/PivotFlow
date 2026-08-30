package app

import "testing"

func TestKeySelector_RemoveChannelCounter(t *testing.T) {
	t.Parallel()

	ks := NewKeySelector()
	scope := rrCounterScope{channelID: 123, keySetID: "0,1,"}
	_ = ks.getOrCreateCounter(scope)

	ks.rrMutex.RLock()
	_, okBefore := ks.rrCounters[scope]
	ks.rrMutex.RUnlock()
	if !okBefore {
		t.Fatal("expected counter to exist before removal")
	}

	ks.RemoveChannelCounter(123)

	ks.rrMutex.RLock()
	_, okAfter := ks.rrCounters[scope]
	ks.rrMutex.RUnlock()
	if okAfter {
		t.Fatal("expected counter to be removed")
	}
}

// A channel can hold several cursors (one per candidate key subset); removing
// the channel must drop all of them, not just the one matching some key set.
func TestKeySelector_RemoveChannelCounterDropsEveryKeySet(t *testing.T) {
	t.Parallel()

	ks := NewKeySelector()
	first := rrCounterScope{channelID: 7, keySetID: "0,1,"}
	second := rrCounterScope{channelID: 7, keySetID: "0,1,2,"}
	other := rrCounterScope{channelID: 8, keySetID: "0,"}
	_ = ks.getOrCreateCounter(first)
	_ = ks.getOrCreateCounter(second)
	_ = ks.getOrCreateCounter(other)

	ks.RemoveChannelCounter(7)

	ks.rrMutex.RLock()
	defer ks.rrMutex.RUnlock()
	if _, exists := ks.rrCounters[first]; exists {
		t.Error("expected first key-set cursor of channel 7 to be removed")
	}
	if _, exists := ks.rrCounters[second]; exists {
		t.Error("expected second key-set cursor of channel 7 to be removed")
	}
	if _, exists := ks.rrCounters[other]; !exists {
		t.Error("expected channel 8 cursor to survive")
	}
}
