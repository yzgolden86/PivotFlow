package app

import (
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestFilterAvailableChannelsAt(t *testing.T) {
	now := time.Date(2026, 8, 22, 23, 30, 0, 0, time.Local)
	channels := []*model.Config{
		{ID: 1},
		{ID: 2, AvailableTimeStart: "09:00", AvailableTimeEnd: "17:00"},
		{ID: 3, AvailableTimeStart: "22:00", AvailableTimeEnd: "08:00"},
		nil,
	}

	got := filterAvailableChannelsAt(channels, now)
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
		t.Fatalf("filterAvailableChannelsAt() IDs = %v, want [1 3]", channelIDs(got))
	}
}

func TestFilterAvailableChannelsAt_AllOutsideReturnsNoFallbackCandidate(t *testing.T) {
	now := time.Date(2026, 8, 22, 20, 0, 0, 0, time.Local)
	channels := []*model.Config{{ID: 1, AvailableTimeStart: "09:00", AvailableTimeEnd: "17:00"}}
	if got := filterAvailableChannelsAt(channels, now); len(got) != 0 {
		t.Fatalf("filterAvailableChannelsAt() = %v, want no candidates", channelIDs(got))
	}
}

func channelIDs(channels []*model.Config) []int64 {
	ids := make([]int64, 0, len(channels))
	for _, cfg := range channels {
		ids = append(ids, cfg.ID)
	}
	return ids
}
