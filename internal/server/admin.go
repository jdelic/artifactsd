package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jdelic/artifactsd/internal/auth"
	"github.com/jdelic/artifactsd/internal/config"
	"github.com/jdelic/artifactsd/internal/session"
)

const (
	flowCookieName  = "artifactsd_oidc_flow"
	adminSessionTTL = 8 * time.Hour
	flowStateTTL    = 10 * time.Minute
)

// flowState is the short-lived CSRF/PKCE state stored in a signed cookie across
// the OIDC redirect.
type flowState struct {
	State    string    `json:"state"`
	Verifier string    `json:"verifier"`
	ReturnTo string    `json:"return_to"`
	Expiry   time.Time `json:"exp"`
}

// initAdminAuth sets up the session manager and the basic-admin fallback hash.
func (s *Server) initAdminAuth() {
	key, err := session.GenerateKey()
	if err != nil {
		// crypto/rand failure is fatal-grade; use a zero key is unacceptable, so
		// panic at startup rather than run with a predictable key.
		panic("server: generating session key: " + err.Error())
	}
	secure := s.cfg.TLS.Mode != config.TLSOff || strings.HasPrefix(s.cfg.Server.BaseURL, "https://")
	s.session = session.NewManager(key, "", secure)

	switch {
	case s.cfg.Admin.BasicPasswordHash != "":
		s.adminBasicHash = s.cfg.Admin.BasicPasswordHash
	case s.cfg.Admin.BasicPassword != "":
		if h, herr := auth.HashPassword(s.cfg.Admin.BasicPassword); herr == nil {
			s.adminBasicHash = h
		}
	}
}

// adminLogin lazily builds the OIDC login flow (issuer discovery happens once).
func (s *Server) adminLogin(ctx context.Context) (*auth.OIDCLogin, error) {
	s.oidcLoginOnce.Do(func() {
		if !s.cfg.OIDC.Enabled() {
			return
		}
		s.oidcLogin, s.oidcLoginErr = auth.NewOIDCLogin(ctx, s.oidcCache,
			s.cfg.OIDC.Issuer, s.cfg.OIDC.ClientID, s.cfg.OIDC.ClientSecret,
			s.cfg.OIDC.RedirectURL, s.cfg.Admin.GroupClaim, s.cfg.OIDC.Scopes)
	})
	return s.oidcLogin, s.oidcLoginErr
}

// handleOIDCLogin starts the Authorization Code + PKCE flow.
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	login, err := s.adminLogin(r.Context())
	if err != nil || login == nil {
		s.log.Error("oidc login unavailable", "error", err)
		http.Error(w, "oidc login unavailable", http.StatusServiceUnavailable)
		return
	}

	verifier := auth.NewVerifier()
	state := randomToken()
	returnTo := sanitizeReturnTo(r.URL.Query().Get("return_to"), s.cfg.Admin.Path)

	fs := flowState{State: state, Verifier: verifier, ReturnTo: returnTo, Expiry: time.Now().Add(flowStateTTL)}
	payload, _ := json.Marshal(fs)
	http.SetCookie(w, &http.Cookie{
		Name:     flowCookieName,
		Value:    s.session.Sign(payload),
		Path:     "/auth/oidc/",
		Expires:  fs.Expiry,
		HttpOnly: true,
		Secure:   s.Secure(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, login.AuthCodeURL(state, verifier), http.StatusFound)
}

// handleOIDCCallback completes the flow, verifies state, and establishes a session.
func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	login, err := s.adminLogin(r.Context())
	if err != nil || login == nil {
		http.Error(w, "oidc login unavailable", http.StatusServiceUnavailable)
		return
	}

	fs, ok := s.readFlowState(r)
	if !ok {
		http.Error(w, "invalid or expired login state", http.StatusBadRequest)
		return
	}
	// CSRF: the returned state must match the one we stored.
	if r.URL.Query().Get("state") != fs.State || fs.State == "" {
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	// The IdP signals a failed/denied authorization by redirecting back with an
	// error (and optional error_description) instead of a code. Surface it in the
	// logs so the failure is self-explaining rather than an opaque missing-code.
	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		desc := r.URL.Query().Get("error_description")
		s.log.Warn("oidc provider returned an authorization error", "error", oauthErr, "error_description", desc)
		http.Error(w, "login failed: "+oauthErr, http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		// No code and no error almost always means the IdP client is not using
		// Authorization Code flow: a Hybrid/Implicit client returns the code/token
		// in the URL fragment, which the browser never sends to the server.
		s.log.Warn("oidc callback has no authorization code and no error; the IdP client is likely not configured for Authorization Code flow (response_type=code) — a Hybrid/Implicit client returns tokens in the URL fragment, which never reaches the server")
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	result, err := login.Exchange(r.Context(), code, fs.Verifier)
	if err != nil {
		s.log.Warn("oidc exchange failed", "error", err)
		http.Error(w, "login failed", http.StatusUnauthorized)
		return
	}

	id := &session.Identity{
		Subject: result.Subject,
		Email:   result.Email,
		Groups:  result.Groups,
		Expiry:  time.Now().Add(adminSessionTTL),
	}
	if err := s.session.SetCookie(w, id); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	s.clearFlowCookie(w)
	http.Redirect(w, r, fs.ReturnTo, http.StatusFound)
}

func (s *Server) readFlowState(r *http.Request) (flowState, bool) {
	c, err := r.Cookie(flowCookieName)
	if err != nil {
		return flowState{}, false
	}
	payload, err := s.session.Open(c.Value)
	if err != nil {
		return flowState{}, false
	}
	var fs flowState
	if err := json.Unmarshal(payload, &fs); err != nil || time.Now().After(fs.Expiry) {
		return flowState{}, false
	}
	return fs, true
}

func (s *Server) clearFlowCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: flowCookieName, Value: "", Path: "/auth/oidc/", MaxAge: -1, HttpOnly: true})
}

// adminGate protects admin routes: a valid admin session (OIDC group) or the
// basic fallback grants access; otherwise browsers are redirected to OIDC login
// and API clients get a 401 (design/14 §7). On success it stashes the
// identity in the request context (session.NewContext) — computed once here
// rather than re-derived independently downstream, since the basic-auth
// fallback carries no session cookie at all, and a freshly re-derived
// per-call identity for it could disagree across requests (breaking anything,
// like CSRF tokens, that needs the same identity to round-trip — design/09 §5).
// adminSecurityHeaders sets browser-isolation headers on every admin response
// as defense-in-depth for the same-origin artifact threat (design/14 §7,
// artifactsd-c63): the admin UI must never be framable, must not be sniffed
// into another content type, and must not be cached (its pages embed CSRF
// tokens and one-time secrets). The primary boundary remains sandboxing active
// artifact responses; these headers harden the admin side regardless.
func (s *Server) adminSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id, err := s.session.FromRequest(r); err == nil && s.isAdmin(id) {
			next.ServeHTTP(w, r.WithContext(session.NewContext(r.Context(), id)))
			return
		}
		if id := s.checkAdminBasic(r); id != nil {
			next.ServeHTTP(w, r.WithContext(session.NewContext(r.Context(), id)))
			return
		}
		s.challengeAdmin(w, r)
	})
}

// logAdminMode reports the effective admin-authorization mode once at startup,
// so a disabled or wide-open OIDC admin setup is visible in the logs (design/14 §7).
func (s *Server) logAdminMode() {
	if s.cfg.OIDC.Enabled() {
		if len(s.cfg.Admin.Subjects) > 0 {
			s.log.Info("OIDC admin granted to a subject/email allowlist", "count", len(s.cfg.Admin.Subjects))
		}
		switch s.cfg.Admin.Group {
		case "":
			if len(s.cfg.Admin.Subjects) == 0 {
				s.log.Warn("OIDC admin disabled: neither ARTIFACTSD_ADMIN_GROUP nor ARTIFACTSD_ADMIN_SUBJECTS is set, so no one is granted admin via OIDC; set a subject/email allowlist, a group name, or \"*\" to admit any authenticated user")
			}
		case "*":
			s.log.Warn("OIDC admin granted to ANY authenticated user (ARTIFACTSD_ADMIN_GROUP=*); ensure the issuer only admits trusted users")
		default:
			s.log.Info("OIDC admin restricted to a group", "group", s.cfg.Admin.Group)
		}
	}
	if s.cfg.Admin.BasicUser != "" {
		s.log.Info("admin basic-auth fallback enabled", "user", s.cfg.Admin.BasicUser)
	}
	if s.cfg.Server.AdminHost != "" {
		s.log.Info("admin UI isolated on a separate host; public artifacts are not served there",
			"admin_host", s.cfg.Server.AdminHost)
	}
}

// isAdmin reports whether an authenticated OIDC identity is authorized for admin
// (design/14 §7). Authorization is an any-of predicate over two independent
// mechanisms:
//
//   - ARTIFACTSD_ADMIN_SUBJECTS: a portable sub/email allowlist. An identity is
//     an admin if its sub OR email matches any entry. sub is always present and
//     email is standard, so this works on every IdP even when group claims are
//     unavailable (e.g. Google Workspace).
//   - ARTIFACTSD_ADMIN_GROUP: group-claim gating, with two sentinels —
//     "" (unset): grants nothing via groups; "*": any authenticated user;
//     any other value: authorize only identities carrying that group.
//
// With both unset/empty, isAdmin fails closed. The caller (adminGate) only
// invokes this with an already-authenticated identity, so "*" means
// "authentication alone suffices".
func (s *Server) isAdmin(id *session.Identity) bool {
	for _, allowed := range s.cfg.Admin.Subjects {
		if allowed != "" && (allowed == id.Subject || allowed == id.Email) {
			return true
		}
	}
	switch s.cfg.Admin.Group {
	case "":
		return false
	case "*":
		return true
	}
	for _, g := range id.Groups {
		if g == s.cfg.Admin.Group {
			return true
		}
	}
	return false
}

// checkAdminBasic verifies the optional htaccess fallback admin credentials,
// returning a synthetic identity on success (deterministic and stable across
// requests, since there's no session cookie backing this path) or nil.
func (s *Server) checkAdminBasic(r *http.Request) *session.Identity {
	if s.cfg.Admin.BasicUser == "" || s.adminBasicHash == "" {
		return nil
	}
	v := auth.NewBasicVerifier(s.cfg.Admin.BasicUser, s.adminBasicHash, auth.DefaultRealm)
	ok, _ := v.Verify(r)
	if !ok {
		return nil
	}
	return &session.Identity{Subject: "basic:" + s.cfg.Admin.BasicUser}
}

// challengeAdmin redirects browsers to OIDC login (when configured) and
// otherwise prompts for basic credentials / returns 401.
func (s *Server) challengeAdmin(w http.ResponseWriter, r *http.Request) {
	browser := strings.Contains(r.Header.Get("Accept"), "text/html")
	if s.cfg.OIDC.Enabled() && browser {
		http.Redirect(w, r, "/auth/oidc/login?return_to="+url.QueryEscape(r.URL.Path), http.StatusFound)
		return
	}
	if s.cfg.Admin.BasicUser != "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="`+auth.DefaultRealm+`"`)
	}
	http.Error(w, "admin authentication required", http.StatusUnauthorized)
}

// randomToken returns a URL-safe 128-bit random string.
func randomToken() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// sanitizeReturnTo only allows local admin-path redirects to prevent open-redirect.
func sanitizeReturnTo(returnTo, adminPath string) string {
	if strings.HasPrefix(returnTo, adminPath) && !strings.HasPrefix(returnTo, "//") {
		return returnTo
	}
	return adminPath + "/"
}
