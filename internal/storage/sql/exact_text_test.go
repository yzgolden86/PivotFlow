package sql

import (
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestExactHistoryPredicatesUseRequestedDatabaseDialect(t *testing.T) {
	for _, dialect := range []struct{ name, required, forbidden string }{
		{"sqlite", " AS BLOB)", " AS BINARY)"},
		{"mysql", " AS BINARY)", " AS BLOB)"},
		{"postgres", "convert_to(", " AS BLOB)"},
	} {
		t.Run(dialect.name, func(t *testing.T) {
			store := NewSQLStore(nil, dialect.name)
			qb := NewQueryBuilder("SELECT model FROM request_traces")
			store.applyRequestTraceFilter(qb, &model.RequestTraceFilter{Model: "Model-A", ClientProtocol: "openai"})
			query, args := qb.Build()
			if strings.Count(query, dialect.required) < 2 || strings.Contains(query, dialect.forbidden) || len(args) != 2 {
				t.Fatalf("trace dialect=%s query=%s args=%v", dialect.name, query, args)
			}
			wb := NewWhereBuilder().ApplyLogFilter(&model.LogFilter{Model: "Model-A", ClientProtocol: "openai"}, dialect.name)
			query, args = wb.Build()
			if strings.Count(query, dialect.required) < 2 || strings.Contains(query, dialect.forbidden) || len(args) != 3 {
				t.Fatalf("log dialect=%s query=%s args=%v", dialect.name, query, args)
			}
		})
	}
}
