package jenkins

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fastRetryConfig returns a Config with retries enabled and negligible backoff so
// the retry-path tests stay fast.
func fastRetryConfig(retryMax int) *Config {
	return &Config{
		RetryMax:     retryMax,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: 5 * time.Millisecond,
	}
}

func TestNewHTTPClient_RetriesIdempotentUntilSuccess(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.WriteHeader(http.StatusBadGateway) // transient 502 on the first attempt
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := newHTTPClient(fastRetryConfig(3))
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("server hits = %d, want 2 (502 then 200)", got)
	}
}

func TestNewHTTPClient_PersistentFailureEnrichedDiagnostic(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service exploded"))
	}))
	defer srv.Close()

	client, err := newHTTPClient(fastRetryConfig(2))
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}

	resp, err := client.Get(srv.URL + "/api/json")
	if err == nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("expected an error after retries are exhausted, got nil")
	}

	msg := err.Error()
	// The diagnostic must carry method, path, status code, body excerpt, and count.
	for _, want := range []string{"GET", "/api/json", "503", "service exploded", "3 attempt"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 3 { // 1 initial + 2 retries
		t.Errorf("server hits = %d, want 3", got)
	}
}

func TestNewHTTPClient_DoesNotRetryPOST(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client, err := newHTTPClient(fastRetryConfig(3))
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}

	resp, err := client.Post(srv.URL, "text/plain", nil)
	if err != nil {
		t.Fatalf("Post: unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("StatusCode = %d, want 503", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("POST server hits = %d, want 1 (non-idempotent POST must not be retried)", got)
	}
}

func TestNewHTTPClient_DoesNotRetryClientError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client, err := newHTTPClient(fastRetryConfig(3))
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get: unexpected error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("server hits = %d, want 1 (4xx must not be retried)", got)
	}
}

func TestNewHTTPClient_RequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the client gives up (or a generous ceiling), so the
		// server goroutine returns promptly once the client times out.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	client, err := newHTTPClient(&Config{RequestTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}

	resp, err := client.Get(srv.URL)
	if err == nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Fatal("expected a timeout error, got nil")
	}
}

func TestResolveDuration(t *testing.T) {
	const envKey = "JENKINS_TEST_RESOLVE_DUR"

	if d, err := resolveDuration("2s", envKey, time.Minute); err != nil || d != 2*time.Second {
		t.Errorf("attr precedence: got %v, %v; want 2s", d, err)
	}
	if _, err := resolveDuration("nope", envKey, time.Minute); err == nil {
		t.Error("expected error for an invalid attribute duration")
	}

	t.Setenv(envKey, "3s")
	if d, err := resolveDuration("", envKey, time.Minute); err != nil || d != 3*time.Second {
		t.Errorf("env fallback: got %v, %v; want 3s", d, err)
	}

	t.Setenv(envKey, "bad")
	if _, err := resolveDuration("", envKey, time.Minute); err == nil {
		t.Error("expected error for an invalid env duration")
	}

	t.Setenv(envKey, "")
	if d, err := resolveDuration("", envKey, time.Minute); err != nil || d != time.Minute {
		t.Errorf("default: got %v, %v; want 1m", d, err)
	}
}

func TestResolveRetryMax(t *testing.T) {
	t.Setenv(envRetryMax, "") // isolate from any ambient value

	if n, err := resolveRetryMax(7, true); err != nil || n != 7 {
		t.Errorf("attr set: got %d, %v; want 7", n, err)
	}
	if n, err := resolveRetryMax(0, true); err != nil || n != 0 {
		t.Errorf("explicit zero must be honored: got %d, %v; want 0", n, err)
	}
	if _, err := resolveRetryMax(-1, true); err == nil {
		t.Error("expected error for a negative retry_max attribute")
	}
	if n, err := resolveRetryMax(0, false); err != nil || n != defaultRetryMax {
		t.Errorf("default: got %d, %v; want %d", n, err, defaultRetryMax)
	}

	t.Setenv(envRetryMax, "9")
	if n, err := resolveRetryMax(0, false); err != nil || n != 9 {
		t.Errorf("env fallback: got %d, %v; want 9", n, err)
	}

	t.Setenv(envRetryMax, "abc")
	if _, err := resolveRetryMax(0, false); err == nil {
		t.Error("expected error for a non-integer env retry_max")
	}

	t.Setenv(envRetryMax, "-3")
	if _, err := resolveRetryMax(0, false); err == nil {
		t.Error("expected error for a negative env retry_max")
	}
}
