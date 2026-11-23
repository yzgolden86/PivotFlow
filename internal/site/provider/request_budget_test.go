package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type budgetTestTransport func(*http.Request) (*http.Response, error)

func (f budgetTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func budgetTestRequest(ctx context.Context, target string) *http.Request {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		panic(err)
	}
	return r
}

func TestRequestBudgetSerializesUntilBodyReleasedAndSpacesStarts(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(map[bool]string{false: "close", true: "eof"}[eof], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				budget := NewRequestBudget()
				starts := make(chan time.Time, 3)
				transport := budget.wrap(budgetTestTransport(func(*http.Request) (*http.Response, error) {
					starts <- time.Now()
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
				}))
				first, err := transport.RoundTrip(budgetTestRequest(context.Background(), "https://shared.example/a"))
				if err != nil {
					t.Fatal(err)
				}
				start := <-starts
				done := make(chan *http.Response, 1)
				secondClosed := make(chan struct{})
				go func() {
					r, e := transport.RoundTrip(budgetTestRequest(context.Background(), "https://SHARED.example:443/b"))
					if e != nil {
						t.Error(e)
					}
					done <- r
					<-secondClosed
					_ = r.Body.Close()
				}()
				synctest.Wait()
				select {
				case <-starts:
					t.Fatal("second request started while first response body was in use")
				default:
				}
				if eof {
					if _, err := io.ReadAll(first.Body); err != nil {
						t.Fatal(err)
					}
				} else if err := first.Body.Close(); err != nil {
					t.Fatal(err)
				}
				second := <-done
				if elapsed := (<-starts).Sub(start); elapsed < 250*time.Millisecond {
					t.Fatalf("start spacing=%v", elapsed)
				}
				_ = first.Body.Close() // EOF followed by Close must not release a later request's slot.
				if got := budget.Snapshot("https://shared.example").Active; got != 1 {
					t.Fatalf("active=%d, want 1", got)
				}
				_ = second.Body.Close()
				close(secondClosed)
				if got := budget.Snapshot("https://shared.example").Active; got != 0 {
					t.Fatalf("active after close=%d", got)
				}
			})
		})
	}
}

func TestRequestBudgetCancellationDoesNotBlockOtherOrigins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budget := NewRequestBudget()
		transport := budget.wrap(budgetTestTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
		}))
		first, _ := transport.RoundTrip(budgetTestRequest(context.Background(), "https://one.example"))
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			response, err := transport.RoundTrip(budgetTestRequest(ctx, "https://one.example/queued"))
			if response != nil {
				_ = response.Body.Close()
			}
			done <- err
		}()
		synctest.Wait()
		other, err := transport.RoundTrip(budgetTestRequest(context.Background(), "https://two.example"))
		if err != nil {
			t.Fatal(err)
		}
		_ = other.Body.Close()
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("wait error=%v", err)
		}
		_ = first.Body.Close()
		if snapshot := budget.Snapshot("https://one.example"); snapshot.Active != 0 || snapshot.Waiting != 0 {
			t.Fatalf("leaked slots: %+v", snapshot)
		}
	})
}

func TestRequestBudgetSharesRateLimitWithoutReplaying(t *testing.T) {
	for _, mode := range []string{"seconds", "date", "invalid", "past", "overflow", "503"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				budget := NewRequestBudget()
				start := time.Now()
				header, duration, status := "2", 2*time.Second, 429
				switch mode {
				case "date":
					header = start.Add(2 * time.Second).UTC().Format(http.TimeFormat)
				case "invalid":
					header, duration = "bad", 30*time.Second
				case "past":
					header, duration = start.Add(-time.Hour).UTC().Format(http.TimeFormat), 30*time.Second
				case "overflow":
					header, duration = "9223372036854775807", 30*time.Second
				case "503":
					status = 503
				}
				calls := 0
				transport := budget.wrap(budgetTestTransport(func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{header}}, Body: http.NoBody}, nil
				}))
				response, err := transport.RoundTrip(budgetTestRequest(context.Background(), "https://limited.example/a"))
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				blocked, blockedErr := transport.RoundTrip(budgetTestRequest(context.Background(), "https://limited.example/b"))
				if blocked != nil {
					_ = blocked.Body.Close()
				}
				err = blockedErr
				if ErrorCode(err) != CodeRateLimited {
					t.Fatalf("cooldown error=%v", err)
				}
				if calls != 1 {
					t.Fatalf("upstream calls=%d during cooldown, want 1", calls)
				}
				if got := budget.Snapshot("https://limited.example").CooldownUntil; got != start.Add(duration).UnixMilli() {
					t.Fatalf("cooldown=%d, want %d", got, start.Add(duration).UnixMilli())
				}
				time.Sleep(duration)
				response, err = transport.RoundTrip(budgetTestRequest(context.Background(), "https://limited.example/c"))
				if err != nil {
					t.Fatal(err)
				}
				_ = response.Body.Close()
				if calls != 2 {
					t.Fatalf("calls after cooldown=%d", calls)
				}
			})
		})
	}
}

func TestRequestBudgetGlobalLimitAndTransportFailureRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budget := NewRequestBudget()
		starts := make(chan string, 8)
		transport := budget.wrap(budgetTestTransport(func(r *http.Request) (*http.Response, error) {
			starts <- r.URL.Host
			if r.URL.Path == "/fail" {
				return nil, errors.New("network failed")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
		}))
		responses := make(chan *http.Response, 8)
		allClosed := make(chan struct{})
		var wg sync.WaitGroup
		for _, host := range []string{"a", "b", "c", "d", "e"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := transport.RoundTrip(budgetTestRequest(context.Background(), "https://"+host+".example"))
				if err != nil {
					t.Error(err)
				}
				responses <- r
				<-allClosed
				_ = r.Body.Close()
			}()
		}
		synctest.Wait()
		if len(starts) != 4 {
			t.Fatalf("started=%d, want 4", len(starts))
		}
		_ = (<-responses).Body.Close()
		synctest.Wait()
		if len(starts) != 5 {
			t.Fatalf("started after release=%d, want 5", len(starts))
		}
		for len(responses) > 0 {
			_ = (<-responses).Body.Close()
		}
		close(allClosed)
		wg.Wait()
		failed, err := transport.RoundTrip(budgetTestRequest(context.Background(), "https://fail.example/fail"))
		if failed != nil {
			_ = failed.Body.Close()
		}
		if err == nil {
			t.Fatal("network failure lost")
		}
		response, err := transport.RoundTrip(budgetTestRequest(context.Background(), "https://fail.example/ok"))
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	})
}

func TestRequestBudgetCancellationWaitsForTransportAndClosesResponse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		budget := NewRequestBudget()
		entered, release := make(chan struct{}), make(chan struct{})
		second := make(chan *http.Response, 1)
		var calls int
		transport := budget.wrap(budgetTestTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path == "/first" {
				close(entered)
				<-release
				return nil, r.Context().Err()
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
		}))
		ctx, cancel := context.WithCancel(context.Background())
		firstDone := make(chan error, 1)
		go func() {
			response, err := transport.RoundTrip(budgetTestRequest(ctx, "https://cancel.example/first"))
			if response != nil {
				_ = response.Body.Close()
			}
			firstDone <- err
		}()
		<-entered
		secondClosed := make(chan struct{})
		go func() {
			r, err := transport.RoundTrip(budgetTestRequest(context.Background(), "https://cancel.example/second"))
			if err != nil {
				t.Error(err)
			}
			second <- r
			<-secondClosed
			_ = r.Body.Close()
		}()
		cancel()
		time.Sleep(time.Second)
		synctest.Wait()
		if calls != 1 {
			t.Errorf("cancellation released slot before HTTP transport returned: %d", calls)
		}
		close(release)
		if err := <-firstDone; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		_ = (<-second).Body.Close()
		close(secondClosed)
		body := &budgetTrackedBody{Reader: strings.NewReader("ok")}
		transport = budget.wrap(budgetTestTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		}))
		ctx, cancel = context.WithCancel(context.Background())
		response, err := transport.RoundTrip(budgetTestRequest(ctx, "https://response.example"))
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		synctest.Wait()
		if !body.closed {
			t.Fatal("cancelled response retained its upstream body")
		}
		_ = response.Body.Close()
	})
}

type budgetTrackedBody struct {
	*strings.Reader
	closed bool
}

func (b *budgetTrackedBody) Close() error { b.closed = true; return nil }

func TestRequestBudgetFactoryPreservesCooldownAcrossAdapters(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"success":false,"message":"limited"}`))
	}))
	defer server.Close()
	budget := NewRequestBudget()
	factory := ClientFactory{AllowPrivate: true, Budget: budget}
	_, err := NewNewAPI(factory).Detect(context.Background(), server.URL)
	if ErrorCode(err) != CodeRateLimited {
		t.Fatalf("first limit=%v", err)
	}
	for _, adapter := range []SiteAdapter{NewNewAPI(factory), NewSub2API(factory), NewOpenAICompatible(factory)} {
		_, err := adapter.Detect(context.Background(), server.URL)
		if ErrorCode(err) != CodeRateLimited {
			t.Errorf("%s lost shared cooldown error: %v", adapter.ID(), err)
		}
	}
	if calls != 1 {
		t.Fatalf("cooldown sent %d HTTP requests", calls)
	}
}
