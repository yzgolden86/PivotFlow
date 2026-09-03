package util

import (
	"testing"
	"time"
)

func TestClampUpstreamResetTime(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		until    time.Time
		now      time.Time
		want     time.Time
		wantSame bool
	}{
		{
			name:     "30 分钟内原样保留",
			until:    now.Add(30 * time.Minute),
			now:      now,
			wantSame: true,
		},
		{
			name:     "1 小时边界原样保留",
			until:    now.Add(time.Hour),
			now:      now,
			wantSame: true,
		},
		{
			name:  "2 小时钳制到 1 小时",
			until: now.Add(2 * time.Hour),
			now:   now,
			want:  now.Add(time.Hour),
		},
		{
			name:  "24 小时钳制到 1 小时",
			until: now.Add(24 * time.Hour),
			now:   now,
			want:  now.Add(time.Hour),
		},
		{
			name:     "零值原样返回",
			until:    time.Time{},
			now:      now,
			wantSame: true,
		},
		{
			name:     "已过期时间原样返回",
			until:    now.Add(-10 * time.Minute),
			now:      now,
			wantSame: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampUpstreamResetTime(tt.until, tt.now)
			if tt.wantSame {
				if !got.Equal(tt.until) {
					t.Errorf("ClampUpstreamResetTime() = %v, want %v (same as input)", got, tt.until)
				}
			} else {
				if !got.Equal(tt.want) {
					t.Errorf("ClampUpstreamResetTime() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestClampUpstreamResetTimePreservesShortDurations 确保短时冷却不被拉长。
func TestClampUpstreamResetTimePreservesShortDurations(t *testing.T) {
	now := time.Now()
	until := now.Add(5 * time.Minute)
	got := ClampUpstreamResetTime(until, now)
	if !got.Equal(until) {
		t.Errorf("5 分钟冷却被改成 %v，应原样保留", got.Sub(now))
	}
}
