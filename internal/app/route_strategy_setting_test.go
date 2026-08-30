package app

import "testing"

func TestValidateRouteStrategySetting(t *testing.T) {
	valid := []string{RouteStrategyBalanced, RouteStrategySticky}
	for _, value := range valid {
		if err := validateSettingValue(routeStrategySettingKey, "string", value); err != nil {
			t.Errorf("value %q rejected: %v", value, err)
		}
	}

	invalid := []string{"", "STICKY", "round_robin", "balanced ", "least_conn"}
	for _, value := range invalid {
		if err := validateSettingValue(routeStrategySettingKey, "string", value); err == nil {
			t.Errorf("value %q accepted, want rejected", value)
		}
	}
}

// 没有 runtime effect 的设置项在控制台里不可编辑，会变成“只存不用的空壳”。
func TestRouteStrategyHasRuntimeEffect(t *testing.T) {
	if effect := systemSettingRuntimeEffect(routeStrategySettingKey); effect == "" {
		t.Fatal("route_strategy 缺少 runtime effect，控制台会禁用该项")
	}
}

// 策略取启动快照（ConfigService 缓存不随更新刷新），因此必须标记为需重启，
// 否则界面会告诉用户“已生效”，而实际要重启才生效。
func TestRouteStrategyRequiresRestart(t *testing.T) {
	if !systemSettingRequiresRestart(routeStrategySettingKey) {
		t.Error("route_strategy 必须标记为需重启：它读的是启动时快照")
	}
}
