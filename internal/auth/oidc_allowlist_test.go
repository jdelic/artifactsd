package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingIssuer answers every request with status and counts hits.
func countingIssuer(t *testing.T, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(20 * time.Millisecond) // widen the race window for the singleflight test
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestOIDCCache_RefusesUnlistedIssuerWithoutIO(t *testing.T) {
	srv, hits := countingIssuer(t, http.StatusOK)
	cache := NewOIDCProviderCache(srv.Client()).WithAllowedIssuers([]string{"https://idp.example.com"})
	if cache.IssuerAllowed(srv.URL) {
		t.Fatal("unlisted issuer reported allowed")
	}
	if _, err := cache.provider(context.Background(), srv.URL); err == nil {
		t.Fatal("provider() for an unlisted issuer succeeded")
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("unlisted issuer caused %d outbound requests, want 0", n)
	}
	// A cache without an allowlist allows nothing.
	if NewOIDCProviderCache(nil).IssuerAllowed("https://idp.example.com") {
		t.Error("empty allowlist allowed an issuer")
	}
}

func TestOIDCCache_IssuerMatchIgnoresOneTrailingSlash(t *testing.T) {
	cache := NewOIDCProviderCache(nil).WithAllowedIssuers([]string{"https://idp.example.com/"})
	if !cache.IssuerAllowed("https://idp.example.com") || !cache.IssuerAllowed("https://idp.example.com/") {
		t.Error("trailing slash not normalized")
	}
	if cache.IssuerAllowed("https://idp.example.com/other") {
		t.Error("prefix match allowed")
	}
}

func TestOIDCCache_NegativeCacheAndSingleflight(t *testing.T) {
	srv, hits := countingIssuer(t, http.StatusInternalServerError)
	cache := NewOIDCProviderCache(srv.Client()).WithAllowedIssuers([]string{srv.URL})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cache.provider(context.Background(), srv.URL); err == nil {
				t.Error("failing discovery succeeded")
			}
		}()
	}
	wg.Wait()
	for range 5 {
		_, _ = cache.provider(context.Background(), srv.URL)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("failing issuer discovered %d times within the negative-cache window, want 1", n)
	}

	// After the window a new attempt is made.
	cache.now = func() time.Time { return time.Now().Add(2 * oidcNegativeTTL) }
	_, _ = cache.provider(context.Background(), srv.URL)
	if n := hits.Load(); n != 2 {
		t.Errorf("after the window: %d discoveries, want 2", n)
	}
}

func TestOIDCCache_RefusesCrossHostRedirect(t *testing.T) {
	target, targetHits := countingIssuer(t, http.StatusOK)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	// The target listens on another port, i.e. another host:port.
	cache := NewOIDCProviderCache(nil).WithAllowedIssuers([]string{redirector.URL})
	if _, err := cache.provider(context.Background(), redirector.URL); err == nil {
		t.Fatal("discovery via cross-host redirect succeeded")
	}
	if n := targetHits.Load(); n != 0 {
		t.Errorf("redirect target was contacted %d times, want 0", n)
	}
}
