// Package mcp implements the MCP adapter exposing artifactsd tools over
// Streamable HTTP — the single committed transport (design/11 §1, decisions
// E4/N1). The tools (SPEC §16) call the management API through pkg/apiclient,
// the same client the CLI uses; MCP is an adapter, not the canonical storage
// layer.
//
// The adapter holds no credentials (artifactsd-m9e.18). Every MCP client must
// send its own artifactsd API token as "Authorization: Bearer <token>"; the
// adapter forwards it, so each agent acts with exactly that token's scopes.
// The adapter does not validate tokens itself — the API does, and an invalid
// token surfaces as the API's 401 in the tool result.
package mcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	gosdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jdelic/artifactsd/internal/buildinfo"
	"github.com/jdelic/artifactsd/pkg/apiclient"
)

// wwwAuthenticate is the challenge on a missing or malformed Authorization
// header. It deliberately carries no resource_metadata parameter, and the
// adapter serves no OAuth discovery documents: some MCP clients ignore a
// configured Authorization header when the server advertises OAuth.
const wwwAuthenticate = `Bearer realm="artifactsd-mcp"`

// sessionTimeout closes idle MCP sessions (and forgets their token binding).
const sessionTimeout = time.Hour

// maxSessions bounds the session→token binding map.
const maxSessions = 10_000

// Config configures the adapter.
type Config struct {
	// BaseURL is the artifactsd base URL: the API the tools call, and the
	// public origin used to compose /c/... and /s/... URLs locally.
	BaseURL string
	// HTTPClient overrides the HTTP client used to call the API (tests).
	HTTPClient *http.Client
	// Logger receives adapter diagnostics; nil disables logging. It never
	// sees Authorization headers or tokens.
	Logger *slog.Logger
}

// Server is one MCP server instance bound to one client token: its tool
// handlers call the management API through Client.
type Server struct {
	// MCP is the underlying SDK server.
	MCP *gosdk.Server
	// Client is the apiclient tool handlers use to call the management API.
	Client *apiclient.Client
	// BaseURL is the artifactsd public base URL (see Config.BaseURL).
	BaseURL string
}

// newServer builds an MCP server with all tools registered, calling the API
// through client.
func newServer(client *apiclient.Client, baseURL string) *Server {
	s := &Server{
		MCP: gosdk.NewServer(&gosdk.Implementation{
			Name:    "artifactsd",
			Version: buildinfo.Version,
		}, nil),
		Client:  client,
		BaseURL: baseURL,
	}
	registerTools(s)
	return s
}

// Adapter serves MCP over Streamable HTTP for any number of clients, each
// authenticated by its own API token.
type Adapter struct {
	cfg Config
	sdk http.Handler

	mu       sync.Mutex
	sessions map[string]sessionBinding // Mcp-Session-Id → token
}

type sessionBinding struct {
	tokenHash string
	lastSeen  time.Time
}

// tokenKey is the request-context key for the caller's token.
type tokenKey struct{}

// New builds the adapter.
func New(cfg Config) *Adapter {
	a := &Adapter{cfg: cfg, sessions: map[string]sessionBinding{}}
	a.sdk = gosdk.NewStreamableHTTPHandler(a.serverFor, &gosdk.StreamableHTTPOptions{
		SessionTimeout: sessionTimeout,
	})
	return a
}

// serverFor builds the MCP server for a new session, bound to the token of
// the request that initializes it.
func (a *Adapter) serverFor(r *http.Request) *gosdk.Server {
	token, _ := r.Context().Value(tokenKey{}).(string)
	client := apiclient.New(apiclient.Config{BaseURL: a.cfg.BaseURL, Token: token, HTTP: a.cfg.HTTPClient})
	return newServer(client, a.cfg.BaseURL).MCP
}

// Handler returns the adapter's HTTP handler: OAuth discovery paths answer
// 404, every other request must carry a Bearer token, and an existing session
// only accepts the token it was initialized with.
func (a *Adapter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/.well-known/", http.NotFoundHandler())
	mux.HandleFunc("/", a.serveMCP)
	return mux
}

func (a *Adapter) serveMCP(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", wwwAuthenticate)
		http.Error(w, "an artifactsd API token is required: send Authorization: Bearer <token>", http.StatusUnauthorized)
		return
	}
	th := hashToken(token)

	if sid := r.Header.Get("Mcp-Session-Id"); sid != "" {
		if !a.sessionAllows(sid, th) {
			w.Header().Set("WWW-Authenticate", wwwAuthenticate)
			http.Error(w, "this MCP session belongs to a different token", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodDelete {
			defer a.forget(sid)
		}
	}

	rec := &sessionRecorder{ResponseWriter: w}
	a.sdk.ServeHTTP(rec, r.WithContext(contextWithToken(r, token)))
	if sid := rec.newSessionID(r); sid != "" {
		a.bind(sid, th)
	}
}

// sessionAllows reports whether the session may be used with token hash th.
// Unknown sessions pass through (the SDK answers them itself).
func (a *Adapter) sessionAllows(sid, th string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	b, ok := a.sessions[sid]
	if !ok {
		return true
	}
	if subtle.ConstantTimeCompare([]byte(b.tokenHash), []byte(th)) != 1 {
		return false
	}
	b.lastSeen = time.Now()
	a.sessions[sid] = b
	return true
}

func (a *Adapter) bind(sid, th string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if len(a.sessions) >= maxSessions {
		for k, b := range a.sessions {
			if now.Sub(b.lastSeen) > sessionTimeout {
				delete(a.sessions, k)
			}
		}
	}
	if len(a.sessions) >= maxSessions {
		// Still full of live sessions: drop the least recently used.
		var oldest string
		var oldestAt time.Time
		for k, b := range a.sessions {
			if oldest == "" || b.lastSeen.Before(oldestAt) {
				oldest, oldestAt = k, b.lastSeen
			}
		}
		delete(a.sessions, oldest)
	}
	a.sessions[sid] = sessionBinding{tokenHash: th, lastSeen: now}
}

func (a *Adapter) forget(sid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, sid)
}

// bearerToken extracts a non-empty Bearer token.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	tok = strings.TrimSpace(tok)
	return tok, tok != ""
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func contextWithToken(r *http.Request, token string) context.Context {
	return context.WithValue(r.Context(), tokenKey{}, token)
}

// sessionRecorder captures the Mcp-Session-Id the SDK assigns on initialize.
type sessionRecorder struct {
	http.ResponseWriter
	sid string
}

func (s *sessionRecorder) WriteHeader(code int) {
	s.sid = s.Header().Get("Mcp-Session-Id")
	s.ResponseWriter.WriteHeader(code)
}

func (s *sessionRecorder) Write(b []byte) (int, error) {
	if s.sid == "" {
		s.sid = s.Header().Get("Mcp-Session-Id")
	}
	return s.ResponseWriter.Write(b)
}

// Flush keeps streaming (SSE) responses working through the wrapper.
func (s *sessionRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *sessionRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// newSessionID returns the session id the SDK assigned in this response when
// the request did not already carry one (i.e. this request created it).
func (s *sessionRecorder) newSessionID(r *http.Request) string {
	if r.Header.Get("Mcp-Session-Id") != "" {
		return ""
	}
	return s.sid
}
