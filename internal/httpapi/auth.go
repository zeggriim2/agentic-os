package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/store"
)

const cookieName = "aos_session"

type ctxKey int

const sessionKey ctxKey = 1

// ------------------------------------------------------------------ rate limit

type rateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: map[string][]time.Time{}}
}

func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 10000 { // borne mémoire
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ------------------------------------------------------------------ sessions

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("aléa indisponible")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Server) tokenHash(token string) string {
	m := hmac.New(sha256.New, s.SessionKey)
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

// dummyHash sert à garder un temps de réponse constant quand le login est inconnu.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("agentic-os-dummy"), bcrypt.MinCost)

type loginReq struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "too_many_attempts", "trop de tentatives de connexion, réessayez dans une minute")
		return
	}
	var req loginReq
	if !decode(w, r, &req) {
		return
	}
	if req.Login == "" || req.Password == "" || len(req.Password) > 512 || len(req.Login) > 100 {
		writeError(w, 400, "invalid_request", "identifiant et mot de passe requis")
		return
	}
	hash, err := s.PasswordHash()
	if err != nil || hash == "" || len(s.SessionKey) < 32 {
		s.Log.Error("authentification non configurée (admin_password_hash ou session_key absent)")
		writeError(w, 503, "auth_not_configured", "authentification non configurée sur le serveur")
		return
	}
	loginOK := subtle.ConstantTimeCompare([]byte(req.Login), []byte(s.AdminLogin)) == 1
	h := []byte(hash)
	if !loginOK {
		h = dummyHash
	}
	if bcrypt.CompareHashAndPassword(h, []byte(req.Password)) != nil || !loginOK {
		s.Log.Warn("échec de connexion", "ip", clientIP(r))
		writeError(w, 401, "invalid_credentials", "identifiant ou mot de passe incorrect")
		return
	}
	token := randomToken(32)
	csrf := randomToken(32)
	expires := time.Now().Add(s.SessionTTL)
	ctx := r.Context()
	if err := store.CreateSession(ctx, s.Store.Pool, s.tokenHash(token), s.AdminLogin, csrf, expires); err != nil {
		s.fail(w, r, err)
		return
	}
	_ = store.PurgeSessions(ctx, s.Store.Pool)
	_ = audit.Write(ctx, s.Store.Pool, audit.Entry{Actor: audit.User(s.AdminLogin), Action: "auth.login", SubjectType: "system",
		After: map[string]any{"ip": clientIP(r)}})
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: s.CookieSecure || r.TLS != nil, Expires: expires})
	writeJSON(w, 200, map[string]any{"login": s.AdminLogin, "csrf_token": csrf})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	_ = store.DeleteSession(r.Context(), s.Store.Pool, sess.TokenHash)
	_ = audit.Write(r.Context(), s.Store.Pool, audit.Entry{Actor: audit.User(sess.Login), Action: "auth.logout", SubjectType: "system"})
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: s.CookieSecure || r.TLS != nil, MaxAge: -1})
	writeJSON(w, 200, map[string]any{"status": "ok"})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	writeJSON(w, 200, map[string]any{"login": sess.Login, "csrf_token": sess.CSRFToken})
}

func sessionFrom(ctx context.Context) *store.Session {
	s, _ := ctx.Value(sessionKey).(*store.Session)
	if s == nil {
		return &store.Session{Login: "inconnu"}
	}
	return s
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || c.Value == "" || len(c.Value) > 200 || len(s.SessionKey) < 32 {
			writeError(w, 401, "unauthenticated", "authentification requise")
			return
		}
		sess, err := store.GetSession(r.Context(), s.Store.Pool, s.tokenHash(c.Value))
		if err != nil {
			writeError(w, 401, "unauthenticated", "session invalide ou expirée")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, sess)))
	})
}

func (s *Server) requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		sess := sessionFrom(r.Context())
		got := r.Header.Get("X-CSRF-Token")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
			writeError(w, 403, "csrf_invalid", "jeton CSRF manquant ou invalide")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func actorLogin(r *http.Request) string { return sessionFrom(r.Context()).Login }

func trim(s string) string { return strings.TrimSpace(s) }
