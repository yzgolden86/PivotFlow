package app

import (
	"testing"

	modelpkg "github.com/yzgolden86/PivotFlow/internal/model"
)

// equalChannels mirrors the reported AgentRouter setup: same priority, one key
// each, so smooth weighted round-robin must spread traffic evenly.
func equalChannels(ids ...int64) []*modelpkg.Config {
	channels := make([]*modelpkg.Config, 0, len(ids))
	for _, id := range ids {
		channels = append(channels, &modelpkg.Config{
			ID:       id,
			Name:     "agentrouter",
			Priority: 0,
			KeyCount: 1,
			Enabled:  true,
		})
	}
	return channels
}

// Cooldown churn must not skew distribution. The RR scope stays pinned to the
// stable universe so a channel entering cooldown does not migrate the whole
// group to a fresh state.
func TestSmoothWeightedRR_CooldownChurnDoesNotStarveEqualChannels(t *testing.T) {
	universeChannels := equalChannels(41, 44, 45, 46, 47)
	universe := rrUniverseKey(universeChannels)

	rr := NewSmoothWeightedRR()
	picks := make(map[int64]int)

	const rounds = 500
	for round := range rounds {
		// Every third round channel 41 is cooled out of the surviving set,
		// exactly the churn that used to reset the shared cursor.
		survivors := equalChannels(41, 44, 45, 46, 47)
		if round%3 == 0 {
			survivors = equalChannels(44, 45, 46, 47)
		}

		weights := make([]int, len(survivors))
		for i := range survivors {
			weights[i] = 1
		}
		ordered := rr.selectByWeight(survivors, weights, rrScope{universe: universe})
		picks[ordered[0].ID]++
	}

	// With the bug, 44 (lowest surviving ID during churn) plus 41 absorb nearly
	// everything and 45/46/47 sit near zero. Assert every channel gets real
	// traffic; the exact split varies because 41 is absent a third of the time.
	for _, id := range []int64{41, 44, 45, 46, 47} {
		if picks[id] == 0 {
			t.Fatalf("channel %d was never selected across %d rounds: %v", id, rounds, picks)
		}
	}
	// The four always-present channels should be within a sane band of each
	// other. A starved channel shows up as an order-of-magnitude gap.
	for _, id := range []int64{44, 45, 46, 47} {
		if picks[id] < rounds/12 {
			t.Errorf("channel %d starved: got %d of %d rounds: %v", id, picks[id], rounds, picks)
		}
	}
	t.Logf("distribution across churn: %v", picks)
}

// Without churn the algorithm must still be near-perfectly even for equal weights.
func TestSmoothWeightedRR_EqualChannelsSpreadEvenly(t *testing.T) {
	channels := equalChannels(41, 44, 45, 46, 47)
	universe := rrUniverseKey(channels)
	rr := NewSmoothWeightedRR()

	picks := make(map[int64]int)
	const rounds = 500
	for range rounds {
		survivors := equalChannels(41, 44, 45, 46, 47)
		weights := []int{1, 1, 1, 1, 1}
		ordered := rr.selectByWeight(survivors, weights, rrScope{universe: universe})
		picks[ordered[0].ID]++
	}

	expected := rounds / 5
	for id, count := range picks {
		if count < expected-2 || count > expected+2 {
			t.Errorf("channel %d got %d picks, want ~%d: %v", id, count, expected, picks)
		}
	}
}

// Different priority tiers within one universe must not share a cursor.
func TestSmoothWeightedRR_ScopeSeparatesPriorityTiers(t *testing.T) {
	channels := equalChannels(1, 2, 3, 4)
	universe := rrUniverseKey(channels)
	rr := NewSmoothWeightedRR()

	high := equalChannels(1, 2)
	low := equalChannels(3, 4)

	// Drive the high tier a few times; the low tier must start fresh rather than
	// inherit an advanced cursor.
	for range 3 {
		rr.selectByWeight(equalChannels(1, 2), []int{1, 1}, rrScope{universe: universe, tier: 10})
	}
	first := rr.selectByWeight(low, []int{1, 1}, rrScope{universe: universe, tier: 0})
	if first[0].ID != 3 {
		t.Fatalf("low tier first pick=%d, want 3 (fresh cursor, lowest ID)", first[0].ID)
	}

	if len(high) != 2 {
		t.Fatalf("unexpected high tier length %d", len(high))
	}
}

// This is the real regression for the reported starvation.
//
// InvalidateChannelListCache used to call ResetAll() unconditionally, and it is
// reachable from site-projection sync and OAuth credential refresh, not just
// admin edits. When its rate approaches the request rate every selection becomes
// a cold start and collapses onto the lowest channel ID. Measured before the fix:
//
//	ResetAll every request   -> {41:1000}                     (one channel takes all)
//	ResetAll every 2 requests -> {41:500, 44:500}              (two channels take all)
//	ResetAll never            -> {41:200,44:200,45:200,46:200,47:200}
//
// The middle case reproduces the reported symptom exactly: two channels absorb
// everything while the rest are starved.
func TestSmoothWeightedRR_FrequentResetCollapsesDistribution(t *testing.T) {
	all := []int64{41, 44, 45, 46, 47}
	universe := rrUniverseKey(equalChannels(all...))

	distribution := func(resetEvery int) map[int64]int {
		rr := NewSmoothWeightedRR()
		picks := make(map[int64]int)
		for round := range 1000 {
			if resetEvery > 0 && round%resetEvery == 0 {
				rr.ResetAll()
			}
			ordered := rr.selectByWeight(equalChannels(all...), []int{1, 1, 1, 1, 1}, rrScope{universe: universe})
			picks[ordered[0].ID]++
		}
		return picks
	}

	// Guard the property that matters: with no resets every channel gets an
	// equal share. This is what the production change restores.
	even := distribution(0)
	for _, id := range all {
		if even[id] != 200 {
			t.Errorf("without resets channel %d got %d picks, want 200: %v", id, even[id], even)
		}
	}

	// Document the failure mode so nobody reintroduces a hot-path ResetAll.
	collapsed := distribution(1)
	if len(collapsed) != 1 {
		t.Errorf("resetting every request should collapse onto one channel, got %v", collapsed)
	}
}

// Guards the production wiring: cache invalidation must not wipe rotation state.
// If someone reintroduces ResetAll into InvalidateChannelListCache, the cursor
// resets and this test fails.
func TestInvalidateChannelListCachePreservesRotationState(t *testing.T) {
	server := &Server{channelBalancer: NewSmoothWeightedRR()}
	all := []int64{41, 44, 45, 46, 47}
	universe := rrUniverseKey(equalChannels(all...))

	picks := make(map[int64]int)
	for range 1000 {
		// Invalidate on every request, mimicking a busy projection-sync or
		// credential-refresh loop.
		server.InvalidateChannelListCache()
		ordered := server.channelBalancer.selectByWeight(
			equalChannels(all...), []int{1, 1, 1, 1, 1}, rrScope{universe: universe})
		picks[ordered[0].ID]++
	}

	for _, id := range all {
		if picks[id] == 0 {
			t.Fatalf("channel %d starved: cache invalidation is still resetting rotation state: %v", id, picks)
		}
	}
	for _, id := range all {
		if picks[id] != 200 {
			t.Errorf("channel %d got %d picks, want 200 even under constant invalidation: %v", id, picks[id], picks)
		}
	}
}

// A genuine channel-set change (channel added/removed from config) should still
// produce a distinct scope, so stale weights do not leak across real topologies.
func TestRRUniverseKeyDistinguishesRealTopologyChanges(t *testing.T) {
	base := rrUniverseKey(equalChannels(1, 2, 3))
	added := rrUniverseKey(equalChannels(1, 2, 3, 4))
	removed := rrUniverseKey(equalChannels(1, 2))

	if base == added {
		t.Error("adding a channel must change the universe key")
	}
	if base == removed {
		t.Error("removing a channel must change the universe key")
	}
	// Order must not matter.
	if base != rrUniverseKey(equalChannels(3, 1, 2)) {
		t.Error("universe key must be order-independent")
	}
}
