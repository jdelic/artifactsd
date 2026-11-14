package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/jdelic/artifactsd/internal/audit"
	"github.com/jdelic/artifactsd/internal/blob"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/session"
	"github.com/jdelic/artifactsd/internal/store"
)

// mockIdP is a minimal OIDC provider: discovery, JWKS and a token endpoint
// that issues an ID token for whichever user the test set as `next`.
type mockIdP struct {
	srv  *httptest.Server
	key  *rsa.PrivateKey
	next string // email of the user the next code exchange logs in
}

func newMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	m := &mockIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": m.srv.URL, "jwks_uri": m.srv.URL + "/jwks",
			"authorization_endpoint": m.srv.URL + "/authorize", "token_endpoint": m.srv.URL + "/token",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		sig, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
			(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		raw, _ := jwt.Signed(sig).Claims(map[string]any{
			"iss": m.srv.URL, "aud": "artifactsd", "sub": "sub-" + m.next, "email": m.next,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}).Serialize()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": raw})
	})
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

type viewerEnv struct {
	s   *Server
	h   http.Handler
	idp *mockIdP
	st  *store.Store
}

// newViewerEnv boots a full server (middleware included) whose OIDC issuer is
// the mock IdP, with collection demo holding:
//
//	own.txt   — oidc entry for the server's issuer, allowed_subjects alice
//	other.txt — oidc entry for a different (allowlisted) issuer
func newViewerEnv(t *testing.T, extra map[string]string) *viewerEnv {
	t.Helper()
	idp := newMockIdP(t)
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, store.DBFilename))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := store.New(db)
	bs, err := blob.NewFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"ARTIFACTSD_BASE_URL":             "http://pub.example.com",
		"ARTIFACTSD_DATA_DIR":             dir,
		"ARTIFACTSD_OIDC_ISSUER":          idp.srv.URL,
		"ARTIFACTSD_OIDC_CLIENT_ID":       "artifactsd",
		"ARTIFACTSD_OIDC_CLIENT_SECRET":   "secret",
		"ARTIFACTSD_ADMIN_SUBJECTS":       "root@example.com",
		"ARTIFACTSD_OIDC_ALLOWED_ISSUERS": "https://other-idp.example.com",
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.LoadAndValidate(nil, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Config: cfg, Logger: audit.NewAppLogger(io.Discard, "error"),
		Access: audit.NewAccessLogger(io.Discard), DB: db, Store: st, Blob: bs,
	})

	ctx := context.Background()
	c := &store.Collection{Name: "demo", Visibility: "public", Metadata: []byte(`{}`)}
	if err := st.CreateCollection(ctx, c); err != nil {
		t.Fatal(err)
	}
	put := func(p, issuer string) {
		ref, size, sum, err := bs.Put(ctx, bytes.NewReader([]byte("secret "+p)))
		if err != nil {
			t.Fatal(err)
		}
		r := string(ref)
		md, _ := json.Marshal(map[string]any{"access": map[string]any{"visibility": "protected", "auth": []map[string]any{{
			"type": "oidc", "issuer": issuer, "audience": "artifactsd", "allowed_subjects": []string{"alice@example.com"},
		}}}})
		ct := "text/plain"
		n := &store.Node{
			CollectionID: c.ID, Path: p, Name: p, Kind: store.KindFile, BlobID: &r, Size: &size,
			Checksum: &sum, ContentType: &ct, Metadata: md,
		}
		if err := st.UpsertNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	put("own.txt", idp.srv.URL)
	put("other.txt", "https://other-idp.example.com")
	return &viewerEnv{s: s, h: s.Handler(), idp: idp, st: st}
}

// get sends a browser GET for target (path+query) on host with cookies.
func (e *viewerEnv) get(host, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://"+host+target, http.NoBody)
	r.Host = host
	r.Header.Set("Accept", "text/html")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, r)
	return rec
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

// login runs the viewer flow for target as user and returns the viewer cookie.
func (e *viewerEnv) login(t *testing.T, host, target, user string) *http.Cookie {
	t.Helper()
	first := e.get(host, target)
	if first.Code != http.StatusFound {
		t.Fatalf("GET %s without a session = %d, want 302", target, first.Code)
	}
	loc := first.Header().Get("Location")
	if !strings.HasPrefix(loc, "/auth/oidc/login?purpose=view&return_to=") {
		t.Fatalf("redirect = %q, want the viewer login", loc)
	}
	login := e.get(host, loc)
	if login.Code != http.StatusFound {
		t.Fatalf("login = %d (%s)", login.Code, login.Body)
	}
	authz, err := url.Parse(login.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(authz.String(), e.idp.srv.URL+"/authorize") {
		t.Fatalf("login redirect = %q, want the IdP", login.Header().Get("Location"))
	}
	if ru := authz.Query().Get("redirect_uri"); ru != "http://pub.example.com/auth/oidc/callback" {
		t.Errorf("redirect_uri = %q, want the public-host callback", ru)
	}
	flow := cookieNamed(login, flowCookieName)
	e.idp.next = user
	cb := e.get(host, "/auth/oidc/callback?code=c1&state="+url.QueryEscape(authz.Query().Get("state")), flow)
	if cb.Code != http.StatusFound || cb.Header().Get("Location") != target {
		t.Fatalf("callback = %d to %q (%s), want 302 back to %s", cb.Code, cb.Header().Get("Location"), cb.Body, target)
	}
	if cookieNamed(cb, session.DefaultCookieName) != nil {
		t.Error("viewer login set an admin session cookie")
	}
	vc := cookieNamed(cb, session.ViewerCookieName)
	if vc == nil {
		t.Fatal("no artifactsd_viewer cookie")
	}
	return vc
}

func TestViewerLogin_FlowServesObject(t *testing.T) {
	e := newViewerEnv(t, nil)
	vc := e.login(t, "pub.example.com", "/c/demo/own.txt", "alice@example.com")

	rec := e.get("pub.example.com", "/c/demo/own.txt", vc)
	if rec.Code != http.StatusOK || rec.Body.String() != "secret own.txt" {
		t.Fatalf("GET with viewer session = %d %q", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "private" || !strings.Contains(rec.Header().Get("Vary"), "Cookie") {
		t.Errorf("caching headers: Cache-Control=%q Vary=%q", rec.Header().Get("Cache-Control"), rec.Header().Get("Vary"))
	}
	logs, _ := e.st.ListAudit(context.Background(), 20)
	found := false
	for _, l := range logs {
		if l.Action == audit.ActionObjectAccess && l.Actor != nil && strings.Contains(*l.Actor, "alice@example.com") {
			found = true
		}
	}
	if !found {
		t.Error("no object.access audit entry for the viewer")
	}

	// The viewer cookie never admits to /admin.
	if rec := e.get("pub.example.com", "/admin/", vc); rec.Code == http.StatusOK {
		t.Error("viewer cookie admitted to the admin UI")
	}
	// An admin-signed value smuggled in as the viewer cookie is rejected.
	adminRec := httptest.NewRecorder()
	_ = e.s.session.SetCookie(adminRec, &session.Identity{Subject: "x", Email: "alice@example.com", Expiry: time.Now().Add(time.Hour)})
	forged := &http.Cookie{Name: session.ViewerCookieName, Value: adminRec.Result().Cookies()[0].Value}
	if rec := e.get("pub.example.com", "/c/demo/own.txt", forged); rec.Code == http.StatusOK {
		t.Error("admin cookie value accepted as a viewer session")
	}
}

func TestViewerLogin_NotPermittedGets403(t *testing.T) {
	e := newViewerEnv(t, nil)
	vc := e.login(t, "pub.example.com", "/c/demo/own.txt", "bob@example.com")
	rec := e.get("pub.example.com", "/c/demo/own.txt", vc)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "Signed in as bob@example.com") {
		t.Errorf("signed-in non-allowed viewer = %d %q, want 403 (no redirect loop)", rec.Code, rec.Body)
	}
}

func TestViewerLogin_ReturnToValidation(t *testing.T) {
	e := newViewerEnv(t, nil)
	for _, rt := range []string{"//evil.example", "/\\evil", "https://evil", "/admin/", "/c/x\r\nLocation: y", ""} {
		rec := e.get("pub.example.com", "/auth/oidc/login?purpose=view&return_to="+url.QueryEscape(rt))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("return_to %q = %d, want 400", rt, rec.Code)
		}
	}
}

func TestViewerLogin_OtherIssuerIsBearerOnly(t *testing.T) {
	e := newViewerEnv(t, nil)
	rec := e.get("pub.example.com", "/c/demo/other.txt")
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("other issuer = %d %q, want 401 Bearer without a redirect", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
}

func TestViewerLogin_AdminHostIsolation(t *testing.T) {
	e := newViewerEnv(t, map[string]string{"ARTIFACTSD_ADMIN_HOST": "admin.example.com"})
	// The view flow completes on the public host...
	vc := e.login(t, "pub.example.com", "/c/demo/own.txt", "alice@example.com")
	if rec := e.get("pub.example.com", "/c/demo/own.txt", vc); rec.Code != http.StatusOK {
		t.Errorf("served on public host = %d", rec.Code)
	}
	// ...and is refused on the admin host.
	if rec := e.get("admin.example.com", "/auth/oidc/login?purpose=view&return_to=%2Fc%2Fdemo%2Fown.txt"); rec.Code != http.StatusNotFound {
		t.Errorf("view login on the admin host = %d, want 404", rec.Code)
	}
	// The admin flow stays admin-host only.
	if rec := e.get("pub.example.com", "/auth/oidc/login"); rec.Code != http.StatusNotFound {
		t.Errorf("admin login on the public host = %d, want 404", rec.Code)
	}
	if rec := e.get("admin.example.com", "/auth/oidc/login"); rec.Code != http.StatusFound {
		t.Errorf("admin login on the admin host = %d, want 302", rec.Code)
	}
}
