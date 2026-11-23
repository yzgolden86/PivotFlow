package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/monitoring"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestRuntimeHealthQueueStates(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	for _, tc := range []struct {
		name   string
		stats  queueRuntimeStats
		state  string
		reason string
	}{
		{"disabled", queueRuntimeStats{}, "disabled", "not_enabled"},
		{"no traffic", queueRuntimeStats{Enabled: true, Capacity: 10}, "idle", "awaiting_work"},
		{"worker holds batch", queueRuntimeStats{Enabled: true, Capacity: 10, Buffered: 1}, "working", "processing"},
		{"write failed", queueRuntimeStats{Enabled: true, Operation: monitoring.OperationStats{Failing: true, Failed: 2}}, "error", "write_failed"},
		{"queue pressure", queueRuntimeStats{Enabled: true, Queued: 8, Capacity: 10}, "warning", "queue_pressure"},
		{"recent loss", queueRuntimeStats{Enabled: true, Dropped: 3, LastDroppedAt: now.Add(-59 * time.Second).UnixMilli()}, "warning", "recent_drop"},
		{"recovered with historical loss", queueRuntimeStats{Enabled: true, Dropped: 3, WriteFailed: 2, LastDroppedAt: now.Add(-61 * time.Second).UnixMilli(), Operation: monitoring.OperationStats{Succeeded: 1, Failed: 2, RecoveredAt: now.Add(-time.Minute).UnixMilli()}}, "healthy", "ready"},
		{"cancelled without a storage fault", queueRuntimeStats{Enabled: true, Operation: monitoring.OperationStats{Cancelled: 1, LastResult: "cancelled"}}, "idle", "awaiting_work"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := queueComponentState("logs", tc.stats, now)
			if got.State != tc.state || got.Reason != tc.reason {
				t.Fatalf("state=%s/%s, want %s/%s", got.State, got.Reason, tc.state, tc.reason)
			}
		})
	}
}

func TestRuntimeHealthBillingStates(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	for _, tc := range []struct {
		name   string
		stats  billingRuntimeStats
		state  string
		reason string
	}{
		{"not enabled", billingRuntimeStats{}, "disabled", "not_enabled"},
		{"journal paused", billingRuntimeStats{Enabled: true, Blocked: true}, "blocked", "billing_blocked"},
		{"shutting down", billingRuntimeStats{Enabled: true, Blocked: true, Closing: true}, "blocked", "shutting_down"},
		{"memory only", billingRuntimeStats{Enabled: true, Unpersisted: 1}, "error", "billing_unpersisted"},
		{"settlement fault", billingRuntimeStats{Enabled: true, Pending: 1, Settlement: monitoring.OperationStats{Failing: true}}, "error", "settlement_failed"},
		{"old backlog", billingRuntimeStats{Enabled: true, Pending: 2, OldestPendingAt: now.Add(-31 * time.Second).UnixMilli()}, "warning", "billing_backlog"},
		{"normal backlog", billingRuntimeStats{Enabled: true, Pending: 1, OldestPendingAt: now.Add(-time.Second).UnixMilli()}, "working", "pending_settlement"},
		{"no settlement yet", billingRuntimeStats{Enabled: true}, "idle", "awaiting_work"},
		{"recovered", billingRuntimeStats{Enabled: true, StoreFailures: 2, JournalFailures: 1, Settled: 3, Settlement: monitoring.OperationStats{Succeeded: 3, Failed: 2}}, "healthy", "ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := billingComponentState(tc.stats, now)
			if got.State != tc.state || got.Reason != tc.reason {
				t.Fatalf("state=%s/%s, want %s/%s", got.State, got.Reason, tc.state, tc.reason)
			}
		})
	}
}

func TestRuntimeHealthRecoveryCacheAndResources(t *testing.T) {
	if got := startupRecoveryComponentState(model.StorageRuntimeStats{Mode: "sqlite"}); got.State != "disabled" {
		t.Fatalf("pure SQL is not a failed recovery: %+v", got)
	}
	if got := startupRecoveryComponentState(model.StorageRuntimeStats{Mode: "hybrid"}); got.State != "unknown" {
		t.Fatalf("missing startup observations reported recovery: %+v", got)
	}
	if got := startupRecoveryComponentState(model.StorageRuntimeStats{Mode: "hybrid", StartupRecovery: &model.StartupRecoveryStats{
		Enabled: true, HistoryEnabled: true, Configurations: monitoring.OperationStats{Succeeded: 1}, Logs: monitoring.OperationStats{Failing: true},
	}}); got.State != "warning" || got.Reason != "history_recovery_failed" {
		t.Fatalf("startup success concealed partial history failure: %+v", got)
	}
	if got := cacheComponentState(model.StorageRuntimeStats{Mode: "hybrid", CacheMayBeStale: true, CacheSync: monitoring.OperationStats{Succeeded: 1, Failed: 1, RecoveredAt: 1}}); got.State != "warning" || got.Reason != "cache_needs_reconcile" {
		t.Fatalf("an unrelated success cleared unverified replica differences: %+v", got)
	}
	if got := websocketComponentState(true, responsesExecutionSessionStoreStats{CapacityRejected: 10, BudgetRejected: 2, UpstreamHeartbeatFailures: 5}); got.State != "idle" {
		t.Fatalf("historical WS counters became current faults: %+v", got)
	}
	if got := websocketComponentState(true, responsesExecutionSessionStoreStats{DownstreamConnections: 8, MaxDownstreamConnections: 10}); got.State != "warning" {
		t.Fatalf("current WS capacity pressure invisible: %+v", got)
	}
}

func TestRuntimeHealthOverallPriorityAndDisabledComponents(t *testing.T) {
	for _, tc := range []struct {
		states []string
		want   string
	}{
		{[]string{"disabled", "disabled"}, "idle"},
		{[]string{"healthy", "idle", "disabled"}, "healthy"},
		{[]string{"healthy", "unknown"}, "unknown"},
		{[]string{"healthy", "working"}, "working"},
		{[]string{"unknown", "warning"}, "warning"},
		{[]string{"warning", "error"}, "error"},
		{[]string{"error", "blocked"}, "blocked"},
	} {
		components := make([]runtimeHealthComponent, len(tc.states))
		for i, state := range tc.states {
			components[i] = runtimeHealthComponent{State: state}
		}
		if got := overallRuntimeHealth(components); got != tc.want {
			t.Fatalf("overall(%v)=%s, want %s", tc.states, got, tc.want)
		}
	}
}

// All unimplemented Store calls panic. Only the memory-only metrics capability
// is supplied, proving the admin handler can still work during a DB outage.
type runtimeMetricsOnlyStore struct {
	storage.Store
}

func (*runtimeMetricsOnlyStore) StorageRuntimeStats() model.StorageRuntimeStats {
	return model.StorageRuntimeStats{Mode: "private-dsn", Primary: "private-host", CacheSync: monitoring.OperationStats{LastErrorCode: "private-error-text"}}
}

func TestRuntimeHealthAPIIsReadOnlyAndPreservesMetrics(t *testing.T) {
	s := &Server{store: &runtimeMetricsOnlyStore{}, runtimeStartedAt: 1_799_999_000_000,
		billing: &billingService{blocked: true, unpersisted: make(map[string]model.TokenSettlement)},
	}
	s.logService = newRuntimeHealthLogService(&runtimeMetricsOnlyStore{}, 3)
	s.logService.logOperation.Begin()(errors.New("private-error-text"))
	s.billing.pending.Store(3)
	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/runtime-metrics", nil))
	s.HandleRuntimeMetrics(c)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics required healthy storage: %d %s", w.Code, w.Body.String())
	}
	data := mustParseAPIResponse[map[string]any](t, w.Body.Bytes()).Data
	for _, key := range []string{"responses_websocket", "billing", "request_traces", "token_failure_stats", "logs", "cleanup", "storage", "started_at", "observed_at", "health"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("metrics contract omitted %s", key)
		}
	}
	if data["billing"].(map[string]any)["pending"] != float64(3) || data["health"].(map[string]any)["state"] != "blocked" || data["health"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("legacy metrics or operational state changed: %+v", data)
	}
	if strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("metrics leaked unsafe source/error text: %s", w.Body.String())
	}
}

func TestRuntimeHealthMissingStorageMetricsStayUnknown(t *testing.T) {
	// The embedded Store supplies no optional metrics interface. Any database
	// method would panic, so this also verifies unavailable metrics stay read-only.
	s := &Server{store: &runtimeCleanupStore{}}
	snapshot := s.collectRuntimeMetrics(time.Now())
	for _, id := range []string{"log_replication", "trace_replication", "cache_sync", "startup_recovery"} {
		found := false
		for _, component := range snapshot.Health.Components {
			if component.ID == id {
				found = true
				if component.State != "unknown" {
					t.Errorf("missing %s observations were reported as %s", id, component.State)
				}
			}
		}
		if !found {
			t.Errorf("missing component %s", id)
		}
	}
	if snapshot.Health.State != "unknown" {
		t.Fatalf("missing storage capability cannot confirm health: %+v", snapshot.Health)
	}
}

func TestRuntimeHealthAPIAdminOnly(t *testing.T) {
	auth := newTestAuthService(t)
	injectAdminToken(auth, "runtime-admin-session", time.Now().Add(time.Hour))
	injectAPIToken(auth, "runtime-backing-token", 0, 9)
	auth.validTokens[model.HashToken("runtime-token-session")] = model.WebSession{
		TokenHash: model.HashToken("runtime-token-session"), Role: model.WebRoleAPIToken,
		AuthTokenID: 9, ExpiresAt: time.Now().Add(time.Hour),
	}
	s := &Server{store: &runtimeMetricsOnlyStore{}, authService: auth}
	router := gin.New()
	s.SetupRoutes(router)
	for _, tc := range []struct {
		token string
		code  int
	}{
		{"", http.StatusUnauthorized},
		{"runtime-token-session", http.StatusForbidden},
		{"runtime-admin-session", http.StatusOK},
	} {
		req := httptest.NewRequest(http.MethodGet, "/admin/runtime-metrics", nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("metrics session=%q status=%d, want %d", tc.token, w.Code, tc.code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/runtime-metrics", nil)
	req.Header.Set("Authorization", "Bearer runtime-token-session")
	if w := serveHTTP(t, router, req); w.Code != http.StatusNotFound {
		t.Fatalf("runtime metrics were exposed to token-scoped dashboards: %d", w.Code)
	}
}
