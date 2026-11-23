package app

import (
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
)

func testRequestCache() *requestCache {
	return newRequestCache(requestCacheConfig{Enabled: true, TTLSeconds: 10, MaxEntries: 2, MaxBytes: 100, MaxEntryBytes: 60})
}

func cacheUnitTicket(cache *requestCache, key string) *requestCacheTicket {
	return cache.openTicket(sha256.Sum256([]byte(key)), "use", "")
}

func TestRequestCacheExpiryLRUAndBudgetBoundRetainedResponses(t *testing.T) {
	cache := testRequestCache()
	channel := &model.Config{ID: 1, Name: "synthetic", Enabled: true}
	channels := []*model.Config{channel}
	now := time.Unix(100, 0)
	a, b, c := cacheUnitTicket(cache, "a"), cacheUnitTicket(cache, "b"), cacheUnitTicket(cache, "c")
	if !cache.put(a, channel, "application/json", []byte("first"), now) || !cache.put(b, channel, "application/json", []byte("second"), now) {
		t.Fatal("could not store bounded entries")
	}
	reply, reason := cache.lookup(a, channels, now.Add(time.Second))
	if reply == nil || string(reply.body) != "first" || reason != "reused" {
		t.Fatalf("initial lookup=%+v reason=%s", reply, reason)
	}
	reply.body[0] = 'X'
	if again, _ := cache.lookup(a, channels, now.Add(2*time.Second)); string(again.body) != "first" {
		t.Fatal("lookup exposed mutable retained response bytes")
	}
	if !cache.put(c, channel, "application/json", []byte("third"), now.Add(3*time.Second)) {
		t.Fatal("LRU put failed")
	}
	if got, _ := cache.lookup(b, channels, now.Add(4*time.Second)); got != nil {
		t.Fatal("least recently used entry survived the capacity bound")
	}
	if got, reason := cache.lookup(a, channels, now.Add(10*time.Second)); got != nil || reason != "expired" {
		t.Fatalf("TTL boundary got=%+v reason=%s", got, reason)
	}
	tooBig := make([]byte, 61)
	if cache.put(cacheUnitTicket(cache, "large"), channel, "application/json", tooBig, now) {
		t.Fatal("single response exceeded its byte bound")
	}
	stats := cache.stats(now)
	if stats.Entries > 2 || stats.Bytes > 100 || stats.Evicted != 1 || stats.Expired != 1 {
		t.Fatalf("unbounded or wrong eviction stats: %+v", stats)
	}
}

func TestRequestCacheClearAndConfigChangeCannotRefillOldGeneration(t *testing.T) {
	cache := testRequestCache()
	channel := &model.Config{ID: 1, Name: "before", Enabled: true}
	now := time.Unix(100, 0)
	old := cacheUnitTicket(cache, "key")
	if !cache.put(old, channel, "application/json", []byte("before"), now) {
		t.Fatal("initial put failed")
	}
	changed := channel.Clone()
	changed.Name = "after"
	if got, reason := cache.lookup(old, []*model.Config{changed}, now); got != nil || reason != "channel_changed" {
		t.Fatalf("changed configuration replayed response: %+v %s", got, reason)
	}
	cache.invalidate("manual_clear", now)
	if cache.put(old, channel, "application/json", []byte("late response"), now) {
		t.Fatal("in-flight response refilled a manually cleared generation")
	}
	if got, reason := cache.lookup(old, []*model.Config{channel}, now); got != nil || reason != "invalidated" {
		t.Fatalf("old generation lookup: %+v %s", got, reason)
	}
	fresh := cacheUnitTicket(cache, "key")
	if !cache.put(fresh, channel, "application/json", []byte("new response"), now) {
		t.Fatal("new generation could not fill")
	}
	if got := cache.stats(now).Invalidations["manual_clear"]; got != 1 {
		t.Fatalf("manual clear observation=%d", got)
	}
}

func TestRequestCacheRuntimeMetadataDoesNotInvalidateSemanticResponse(t *testing.T) {
	cache := testRequestCache()
	channel := &model.Config{ID: 1, Name: "synthetic", Enabled: true,
		URLs:         model.ChannelURLs{{URL: "https://synthetic.example", Protocols: []string{"openai"}}},
		ModelEntries: []model.ModelEntry{{Model: "model"}}, KeyCount: 1,
		CreatedAt: model.JSONTime{Time: time.Unix(10, 0)}, UpdatedAt: model.JSONTime{Time: time.Unix(20, 0)}}
	now := time.Unix(100, 0)
	ticket := cacheUnitTicket(cache, "same request")
	defer cache.closeTicket(ticket)
	if !cache.put(ticket, channel, "application/json", []byte("same reply"), now) {
		t.Fatal("warmup failed")
	}
	refreshed := channel.Clone()
	refreshed.CooldownUntil, refreshed.CooldownDurationMs, refreshed.KeyCount = 200, 5000, 2
	refreshed.CreatedAt, refreshed.UpdatedAt = model.JSONTime{Time: time.Unix(15, 0)}, model.JSONTime{Time: now}
	if reply, reason := cache.lookup(ticket, []*model.Config{refreshed}, now); reply == nil || reason != "reused" {
		t.Fatalf("runtime observation changed response semantics: reply=%+v reason=%s", reply, reason)
	}
	refreshed.ModelEntries[0].RedirectModel = "different-upstream-model"
	if reply, reason := cache.lookup(ticket, []*model.Config{refreshed}, now); reply != nil || reason != "channel_changed" {
		t.Fatalf("actual model transformation change retained the cache: reply=%+v reason=%s", reply, reason)
	}
	if channel.CooldownUntil != 0 || channel.ModelEntries[0].RedirectModel != "" || channel.KeyCount != 1 {
		t.Fatal("fingerprinting changed the borrowed immutable channel snapshot")
	}
}

func TestRequestCacheRefreshKeyEpochRejectsOlderPublish(t *testing.T) {
	cache := testRequestCache()
	channel := &model.Config{ID: 1, Name: "synthetic", Enabled: true}
	key := sha256.Sum256([]byte("same exact request"))
	old := cache.openTicket(key, "use", "")
	defer cache.closeTicket(old)
	refresh := cache.openTicket(key, "refresh", "client_refresh")
	defer cache.closeTicket(refresh)
	now := time.Unix(100, 0)
	if !cache.put(refresh, channel, "application/json", []byte("new answer"), now) {
		t.Fatal("refresh could not publish")
	}
	if cache.put(old, channel, "application/json", []byte("old answer"), now) {
		t.Fatal("an older fill overwrote the newer refresh")
	}
	reader := cache.openTicket(key, "use", "")
	defer cache.closeTicket(reader)
	if reply, _ := cache.lookup(reader, []*model.Config{channel}, now); reply == nil || string(reply.body) != "new answer" {
		t.Fatalf("newer response was lost: %+v", reply)
	}
}

func TestRequestCacheTotalBodyBudgetEvictsEvenBelowEntryLimit(t *testing.T) {
	cache := newRequestCache(requestCacheConfig{Enabled: true, TTLSeconds: 10, MaxEntries: 4, MaxBytes: 100, MaxEntryBytes: 60})
	channel := &model.Config{ID: 1, Enabled: true}
	now := time.Now()
	first, second := cacheUnitTicket(cache, "first"), cacheUnitTicket(cache, "second")
	if !cache.put(first, channel, "application/json", make([]byte, 60), now) || !cache.put(second, channel, "application/json", make([]byte, 60), now) {
		t.Fatal("bounded response could not be stored")
	}
	if stats := cache.stats(now); stats.Bytes != 60 || stats.Entries != 1 || stats.Evicted != 1 {
		t.Fatalf("byte budget did not evict independently of entry count: %+v", stats)
	}
}

func TestRequestCacheActiveKeyBudgetReleasesCompletedLeases(t *testing.T) {
	cache := testRequestCache()
	tickets := make([]*requestCacheTicket, 0, 4096)
	for index := range 4096 {
		ticket := cacheUnitTicket(cache, strconv.Itoa(index))
		if ticket == nil {
			t.Fatalf("bounded active key %d rejected early", index)
		}
		tickets = append(tickets, ticket)
	}
	if ticket := cacheUnitTicket(cache, "over budget"); ticket != nil {
		t.Fatal("active-key state can grow beyond its hard bound")
	}
	for _, ticket := range tickets {
		cache.closeTicket(ticket)
		cache.closeTicket(ticket) // completion is idempotent
	}
	if ticket := cacheUnitTicket(cache, "after completion"); ticket == nil {
		t.Fatal("completed requests leaked the active-key budget")
	} else {
		cache.closeTicket(ticket)
	}
	if len(cache.flights) != 0 {
		t.Fatal("completed key leases remained retained")
	}
}

func TestRequestCacheExactKeySeparatesTokensBodiesAndSemanticHeaders(t *testing.T) {
	request := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-PivotFlow-Cache", "use")
	base := requestCacheKey(request, []byte(`{"x":1}`), protocol.OpenAI, "owner-a", 1)
	if base == requestCacheKey(request, []byte(`{"x":1}`), protocol.OpenAI, "owner-b", 2) ||
		base == requestCacheKey(request, []byte(`{ "x":1}`), protocol.OpenAI, "owner-a", 1) {
		t.Fatal("cache crossed token or exact body isolation")
	}
	request.Header.Set("Anthropic-Beta", "synthetic-beta")
	if base == requestCacheKey(request, []byte(`{"x":1}`), protocol.OpenAI, "owner-a", 1) {
		t.Fatal("a semantic header did not isolate responses")
	}
	request.Header.Del("Anthropic-Beta")
	request.Header.Set("X-PivotFlow-Cache", "refresh")
	request.Header.Set("Cache-Control", "no-cache")
	if base != requestCacheKey(request, []byte(`{"x":1}`), protocol.OpenAI, "owner-a", 1) {
		t.Fatal("refresh control would store into a different semantic cache key")
	}
}

func TestRequestCacheCaptureBudgetDoesNotInterruptForwardingAndReleases(t *testing.T) {
	cache := testRequestCache()
	first := newRequestCacheCapture(httptest.NewRecorder(), cache, cacheUnitTicket(cache, "a"))
	secondRecorder := httptest.NewRecorder()
	second := newRequestCacheCapture(secondRecorder, cache, cacheUnitTicket(cache, "b"))
	thirdRecorder := httptest.NewRecorder()
	third := newRequestCacheCapture(thirdRecorder, cache, cacheUnitTicket(cache, "c"))
	defer first.close()
	defer second.close()
	defer third.close()
	if _, err := first.Write(make([]byte, 60)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write(make([]byte, 40)); err != nil {
		t.Fatal(err)
	}
	if n, err := third.Write([]byte("still forwarded")); err != nil || n != 15 || thirdRecorder.Body.String() != "still forwarded" {
		t.Fatalf("capture pressure interrupted original forwarding n=%d err=%v", n, err)
	}
	if third.reason != "capture_budget" || cache.stats(time.Now()).CaptureBytes != 100 {
		t.Fatalf("capture budget not enforced: reason=%s stats=%+v", third.reason, cache.stats(time.Now()))
	}
	first.close()
	second.close()
	third.close()
	if stats := cache.stats(time.Now()); stats.Captures != 0 || stats.CaptureBytes != 0 {
		t.Fatalf("capture resource leak: %+v", stats)
	}
}

func TestRequestCacheConcurrentLookupClearAndFillStayBounded(t *testing.T) {
	cache := testRequestCache()
	channel := &model.Config{ID: 1, Name: "synthetic", Enabled: true}
	var workers sync.WaitGroup
	for index := range 8 {
		workers.Go(func() {
			for range 50 {
				ticket := cacheUnitTicket(cache, string(rune('a'+index)))
				cache.put(ticket, channel, "application/json", []byte("small text"), time.Now())
				cache.lookup(ticket, []*model.Config{channel}, time.Now())
				cache.invalidate("manual_clear", time.Now())
				cache.closeTicket(ticket)
			}
		})
	}
	workers.Wait()
	if stats := cache.stats(time.Now()); stats.Entries > 2 || stats.Bytes > 100 || stats.CaptureBytes != 0 {
		t.Fatalf("concurrent cache bounds violated: %+v", stats)
	}
}

type cacheFailingWriter struct{ *httptest.ResponseRecorder }

func (w cacheFailingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestRequestCacheCaptureFailedWriteCannotBecomeStoredSuccess(t *testing.T) {
	cache := testRequestCache()
	capture := newRequestCacheCapture(cacheFailingWriter{httptest.NewRecorder()}, cache, cacheUnitTicket(cache, "failed"))
	defer capture.close()
	if _, err := capture.Write([]byte("answer")); err == nil || capture.reason != "write_failed" || len(capture.body) != 0 {
		t.Fatalf("failed downstream write retained body: reason=%s bytes=%d err=%v", capture.reason, len(capture.body), err)
	}
	if got := cache.stats(time.Now()).CaptureBytes; got != 0 {
		t.Fatalf("failed downstream write retained %d bytes", got)
	}
}

var _ http.ResponseWriter = cacheFailingWriter{}
