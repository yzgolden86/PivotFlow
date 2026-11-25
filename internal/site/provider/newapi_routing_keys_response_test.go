package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewAPIListRoutingKeysResponseCollections(t *testing.T) {
	const rows = `[{"id":7,"name":"active","key":"route-active","status":1},{"id":8,"name":"paused","key":"route-paused","status":2},{"id":9,"name":"disabled","key":"route-disabled","status":"disabled"}]`
	for _, tc := range []struct {
		name string
		body string
		want []RoutingKeySnapshot
	}{
		{"direct_empty", `[]`, []RoutingKeySnapshot{}},
		{"envelope_empty", `{"success":true,"data":[]}`, []RoutingKeySnapshot{}},
		{"items_empty", `{"success":true,"data":{"items":[],"total":0,"page":1,"page_size":100}}`, []RoutingKeySnapshot{}},
		{"top_level_total_empty", `{"success":true,"data":[],"total":0}`, []RoutingKeySnapshot{}},
		{"zero_based_page_empty", `{"success":true,"data":{"items":[],"p":0,"offset":0,"has_more":false}}`, []RoutingKeySnapshot{}},
		{"direct_rows", rows, nil},
		{"envelope_rows", `{"success":true,"data":` + rows + `}`, nil},
		{"items_rows", `{"success":true,"data":{"items":` + rows + `,"total":3,"page":1,"page_size":100}}`, nil},
		{"list_rows", `{"success":true,"data":{"list":` + rows + `}}`, nil},
		{"nested_data_rows", `{"success":true,"data":{"data":` + rows + `}}`, nil},
		{"records_rows", `{"success":true,"data":{"records":` + rows + `}}`, nil},
		{"rows_rows", `{"success":true,"data":{"rows":` + rows + `}}`, nil},
		{"string_total", `{"success":true,"data":{"items":` + rows + `,"total":"3"}}`, nil},
		{"top_level_total", `{"success":true,"data":` + rows + `,"total":3}`, nil},
		{"complete_capped_page", `{"success":true,"data":{"items":` + rows + `,"total":3,"page_size":3}}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, requests, err := routingKeyResponse(t, tc.body)
			want := tc.want
			if want == nil {
				want = []RoutingKeySnapshot{
					{ID: "7", Name: "active", Key: "route-active", Models: []string{}, Enabled: true},
					{ID: "8", Name: "paused", Key: "route-paused", Models: []string{}, Enabled: false},
					{ID: "9", Name: "disabled", Key: "route-disabled", Models: []string{}, Enabled: false},
				}
			}
			if err != nil || !reflect.DeepEqual(keys, want) || requests != 1 {
				t.Fatalf("keys=%+v err=%v requests=%d, want %+v in one request", keys, err, requests, want)
			}
		})
	}
}

func TestNewAPIListRoutingKeysRejectsMalformedResponses(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"null_response", `null`},
		{"empty_object", `{}`},
		{"string_success", `{"success":"true","data":[]}`},
		{"missing_data", `{"success":true}`},
		{"null_data", `{"success":true,"data":null}`},
		{"empty_data_object", `{"success":true,"data":{}}`},
		{"string_data", `{"success":true,"data":"[]"}`},
		{"number_data", `{"success":true,"data":3}`},
		{"boolean_data", `{"success":true,"data":false}`},
		{"null_items", `{"success":true,"data":{"items":null}}`},
		{"object_items", `{"success":true,"data":{"items":{}}}`},
		{"invalid_items_with_valid_alias", `{"success":true,"data":{"items":null,"list":[]}}`},
		{"nonobject_item", `{"success":true,"data":[17]}`},
		{"null_item", `{"success":true,"data":[null]}`},
		{"missing_key", `{"success":true,"data":[{"id":1,"name":"missing"}]}`},
		{"empty_key", `{"success":true,"data":[{"key":"  "}]}`},
		{"null_key", `{"success":true,"data":[{"key":null}]}`},
		{"number_key", `{"success":true,"data":[{"key":123}]}`},
		{"object_key", `{"success":true,"data":[{"key":{"value":"route"}}]}`},
		{"boolean_key", `{"success":true,"data":[{"key":true}]}`},
		{"array_key", `{"success":true,"data":[{"key":["route"]}]}`},
		{"mixed_valid_invalid", `{"success":true,"data":[{"key":"route-valid"},{"name":"missing"}]}`},
		{"masked_before_invalid", `{"success":true,"data":[{"id":7,"key":"abc***xyz"},null]}`},
		{"bad_total_type", `{"success":true,"data":{"items":[],"total":null}}`},
		{"fractional_total", `{"success":true,"data":{"items":[],"total":0.5}}`},
		{"negative_total", `{"success":true,"data":{"items":[],"total":-1}}`},
		{"overflow_total", `{"success":true,"data":{"items":[],"total":1e100}}`},
		{"conflicting_totals", `{"success":true,"total":0,"data":{"items":[],"total":1}}`},
		{"ambiguous_collections", `{"success":true,"data":{"items":[],"list":[{"key":"route"}]}}`},
		{"invalid_has_more", `{"success":true,"data":{"items":[],"has_more":null}}`},
		{"invalid_page_size", `{"success":true,"data":{"items":[],"page_size":0}}`},
		{"inconsistent_total", `{"success":true,"data":{"items":[{"key":"route-valid"}],"total":0}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, requests, err := routingKeyResponse(t, tc.body)
			if err == nil || ErrorCode(err) != CodeInvalidResponse || keys != nil || requests != 1 {
				t.Fatalf("keys=%+v err=%v code=%q requests=%d, want invalid response with no partial keys or reveal", keys, err, ErrorCode(err), requests)
			}
		})
	}
}

func TestNewAPIListRoutingKeysRejectsIncompleteResponses(t *testing.T) {
	const row = `{"id":7,"key":"abc***xyz"}`
	fullPage := `[` + strings.TrimSuffix(strings.Repeat(`{"key":"route-valid"},`, 100), ",") + `]`
	for _, tc := range []struct{ name, body string }{
		{"items_total_truncated", `{"success":true,"data":{"items":[` + row + `],"total":2}}`},
		{"top_level_total_truncated", `{"success":true,"data":[` + row + `],"total":2}`},
		{"empty_but_total_nonzero", `{"success":true,"data":{"items":[],"total":1}}`},
		{"has_more", `{"success":true,"data":{"items":[` + row + `],"has_more":true}}`},
		{"later_page", `{"success":true,"data":{"items":[],"page":2,"total":0}}`},
		{"nonzero_offset", `{"success":true,"data":{"items":[],"offset":100}}`},
		{"multiple_pages", `{"success":true,"data":{"items":[],"total_pages":2}}`},
		{"later_zero_based_page", `{"success":true,"data":{"items":[],"p":1}}`},
		{"full_page_no_total", `{"success":true,"data":` + fullPage + `}`},
		{"server_capped_page", `{"success":true,"data":{"items":[` + row + `],"page_size":1}}`},
		{"above_bound_with_total", `{"success":true,"data":{"items":` + strings.TrimSuffix(fullPage, "]") + `,{"key":"route-extra"}],"total":101}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			keys, requests, err := routingKeyResponse(t, tc.body)
			if err == nil || ErrorCode(err) != CodeInvalidResponse || keys != nil || requests != 1 {
				t.Fatalf("keys=%d err=%v code=%q requests=%d, want bounded failure before key reveal", len(keys), err, ErrorCode(err), requests)
			}
		})
	}

	t.Run("full_page_with_complete_total", func(t *testing.T) {
		keys, requests, err := routingKeyResponse(t, `{"success":true,"data":{"items":`+fullPage+`,"total":100}}`)
		if err != nil || len(keys) != 100 || requests != 1 {
			t.Fatalf("keys=%d err=%v requests=%d, want complete 100-key response", len(keys), err, requests)
		}
	})
}

func TestNewAPIListRoutingKeysRejectsFailedEnvelope(t *testing.T) {
	for _, body := range []string{
		`{"success":false,"message":"rejected","data":[]}`,
		`{"success":true,"error":{"message":"rejected"},"data":[]}`,
	} {
		t.Run(body, func(t *testing.T) {
			keys, requests, err := routingKeyResponse(t, body)
			if err == nil || keys != nil || requests != 1 {
				t.Fatalf("keys=%+v err=%v requests=%d, want rejection", keys, err, requests)
			}
		})
	}
}

func TestNewAPIListRoutingKeysFailedRevealReturnsNoPartialList(t *testing.T) {
	keys, requests, err := routingKeyResponse(t, `{"success":true,"data":[{"key":"route-valid"},{"id":7,"key":"abc***xyz","status":2}]}`)
	if err == nil || keys != nil || requests != 4 {
		t.Fatalf("keys=%+v err=%v requests=%d, want no partial result after failed batch and individual reveals", keys, err, requests)
	}
}

func routingKeyResponse(t *testing.T, body string) ([]RoutingKeySnapshot, int32, error) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/api/token/" || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("p") != "0" || r.URL.Query().Get("size") != "100" {
			t.Errorf("unexpected discovery query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	keys, err := NewNewAPI(ClientFactory{AllowPrivate: true}).ListRoutingKeys(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "management-session"},
	})
	return keys, requests.Load(), err
}
