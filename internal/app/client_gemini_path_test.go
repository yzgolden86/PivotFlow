package app

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestClientConnectionGeminiCompleteModelIdentity(t *testing.T) {
	for _, modelName := range []string{"group/模型:variant", "group/模型:%2F?key=value#fragment"} {
		t.Run(modelName, func(t *testing.T) {
			var calls atomic.Int32
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/v1beta/models/"+modelName+":generateContent" || r.URL.RawQuery != "" || r.URL.Fragment != "" {
					t.Errorf("model identity changed at upstream: path=%q query=%q fragment=%q", r.URL.Path, r.URL.RawQuery, r.URL.Fragment)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"OK"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`)
			}))
			env := setupProxyTestEnv(t, []testChannel{{name: "synthetic-full-model", upstreamProtocol: "gemini", models: modelName + ",group"}}, map[int]string{0: upstream.URL})
			hash := model.HashToken("test-api-key")
			setAllowed := func(names ...string) {
				env.server.authService.authTokensMux.Lock()
				env.server.authService.authTokenModels[hash] = names
				env.server.authService.authTokensMux.Unlock()
			}
			escaped := url.PathEscape(modelName)
			paths := []string{
				"/v1beta/models/" + escaped + ":generateContent",
				"/v1beta/models/" + strings.ReplaceAll(escaped, ":", "%3A") + ":generateContent",
			}
			body := map[string]any{"contents": []any{map[string]any{"parts": []any{map[string]any{"text": "OK"}}}}}
			setAllowed(modelName)
			for index, path := range paths {
				response := doProxyRequest(t, env.engine, path, body, nil)
				if response.Code != http.StatusOK || calls.Load() != int32(index+1) || !strings.Contains(response.Body.String(), "OK") {
					t.Fatalf("full model failed: status=%d calls=%d response=%s", response.Code, calls.Load(), response.Body.String())
				}
				trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
				if trace.Model != modelName || trace.Outcome != "success" {
					t.Fatalf("history lost model identity: model=%q outcome=%q", trace.Model, trace.Outcome)
				}
			}

			// An allowed prefix, even when supplied in the JSON body, must never
			// authorize a different Gemini model selected by the URL.
			setAllowed("group")
			for _, path := range paths {
				for _, bodyModel := range []string{"", "group"} {
					body["model"] = bodyModel
					denied := doProxyRequest(t, env.engine, path, body, nil)
					if denied.Code != http.StatusForbidden || calls.Load() != int32(len(paths)) {
						t.Fatalf("prefix/body bypass: model=%q status=%d calls=%d response=%s", bodyModel, denied.Code, calls.Load(), denied.Body.String())
					}
				}
			}
		})
	}
}

func TestClientConnectionGeminiModelRedirectPreservesPathEncoding(t *testing.T) {
	original := "alias/模型:preview%2F"
	actual := "provider/模型:actual?literal#suffix"
	path := "/v1beta/models/" + url.PathEscape(original) + ":streamGenerateContent"
	want := "/v1beta/models/" + url.PathEscape(actual) + ":streamGenerateContent"
	if got := replaceModelInPath(path, original, actual); got != want {
		t.Fatalf("redirect=%q want=%q", got, want)
	}
	if got := buildGeminiGeneratePath(actual, false); got != "/v1beta/models/"+url.PathEscape(actual)+":generateContent" {
		t.Fatalf("generated path=%q", got)
	}
}
