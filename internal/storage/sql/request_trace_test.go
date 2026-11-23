package sql

import (
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestRequestTraceInsertSQLUsesDialectConflictHandling(t *testing.T) {
	tests := []struct {
		driver     string
		wantPrefix string
		wantSuffix string
	}{
		{driver: "sqlite", wantPrefix: "INSERT INTO", wantSuffix: "ON CONFLICT(request_id) DO NOTHING"},
		{driver: "mysql", wantPrefix: "INSERT INTO", wantSuffix: "ON DUPLICATE KEY UPDATE request_id=request_id"},
		{driver: "postgres", wantPrefix: "INSERT INTO", wantSuffix: "ON CONFLICT (request_id) DO NOTHING"},
	}
	for _, test := range tests {
		t.Run(test.driver, func(t *testing.T) {
			store := &SQLStore{driverName: test.driver}
			got := store.requestTraceInsertSQL()
			if !strings.HasPrefix(got, test.wantPrefix) {
				t.Fatalf("insert SQL prefix = %q, want %q", got, test.wantPrefix)
			}
			if !strings.HasSuffix(got, test.wantSuffix) {
				t.Fatalf("insert SQL suffix = %q, want %q", got, test.wantSuffix)
			}
		})
	}
}

func TestRequestTraceInsertArgsEncodeBooleansAsIntegers(t *testing.T) {
	args := requestTraceInsertArgs(&model.RequestTrace{
		RequestID:   "pf_req_args",
		IsStreaming: true,
		Truncated:   true,
	}, "[]")

	if args[6] != 1 {
		t.Fatalf("is_streaming arg=%#v, want integer 1", args[6])
	}
	if args[14] != 1 {
		t.Fatalf("truncated arg=%#v, want integer 1", args[14])
	}
}
