package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Read the snapshot's public value instead of using the request/header ID.
// The baseline has no UserID value and must fail on the actual profile case.
func TestAccountSnapshotIdentityComesFromProfile(t *testing.T) {
	for _, platform := range []string{"new-api-family", "anyrouter", "veloera", "sub2api"} {
		for _, profileID := range []string{`42`, `"42"`, `null`, `42.5`, `-1`} {
			t.Run(platform+"/"+profileID, func(t *testing.T) {
				var calls atomic.Int64
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					if r.Method != http.MethodGet || r.URL.Path != "/api/user/self" && r.URL.Path != "/api/v1/auth/me" {
						http.NotFound(w, r)
						return
					}
					if platform == "sub2api" {
						_, _ = fmt.Fprintf(w, `{"code":0,"data":{"id":%s,"username":"actual-profile","balance":1}}`, profileID)
					} else {
						_, _ = fmt.Fprintf(w, `{"success":true,"data":{"id":%s,"username":"actual-profile","quota":500000}}`, profileID)
					}
				}))
				defer server.Close()
				factory := ClientFactory{AllowPrivate: true}
				var adapter SiteAdapter
				switch platform {
				case "new-api-family":
					adapter = NewNewAPI(factory)
				case "anyrouter":
					adapter = NewAnyRouter(factory)
				case "veloera":
					adapter = NewVeloera(factory)
				case "sub2api":
					adapter = NewSub2API(factory)
				}
				snapshot, err := adapter.RefreshAccount(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "synthetic-profile-token", UserID: 111}})
				valid := profileID == `42` || profileID == `"42"`
				if platform == "sub2api" && !valid {
					if ErrorCode(err) != CodeInvalidResponse {
						t.Fatal("Sub2API accepted a profile without a stable identity")
					}
					return
				}
				if err != nil || calls.Load() != 1 {
					t.Fatal("direct profile read failed or repeated")
				}
				raw, _ := json.Marshal(snapshot)
				var identity struct{ UserID int64 }
				if json.Unmarshal(raw, &identity) != nil || valid && identity.UserID != 42 || !valid && identity.UserID != 0 {
					t.Fatal("snapshot identity did not match the actual profile response")
				}
			})
		}
	}
}
