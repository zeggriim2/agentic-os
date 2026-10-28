// Package httpapi expose l'API REST /api, le flux SSE, le webhook GitHub et l'UI embarquée.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/engine"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// HealthDeps regroupe les sondes des dépendances externes.
type HealthDeps struct {
	Hermes interface {
		Capabilities(ctx context.Context, profile string) error
	}
	GitHub interface {
		Check(ctx context.Context) error
	}
	Telegram interface {
		Check(ctx context.Context) error
	}
}

// Server porte les dépendances de l'API.
type Server struct {
	Store         *store.Store
	Engine        *engine.Engine
	Log           *slog.Logger
	Hub           *Hub
	Web           fs.FS
	AdminLogin    string
	PasswordHash  func() (string, error)
	SessionKey    []byte
	WebhookSecret func() ([]byte, error)
	CookieSecure  bool
	SessionTTL    time.Duration
	Version       string
	Revision      string
	Health        HealthDeps
	Workers       int
	GitGraph      GitGraphSource // lecture du graphe Git des projets (nil : indisponible)
	GitHubWebURL  string         // base des liens commits/PR/tags (défaut https://github.com)
	// HermesProfiles liste les profils Hermes dont la clé est configurée (nil : pas de contrôle).
	HermesProfiles func() ([]string, error)

	gitCache    gitGraphCache
	limiter     *rateLimiter
	healthMu    sync.Mutex
	healthCache map[string]any
	healthAt    time.Time
}

// Router construit le routeur chi.
func (s *Server) Router() http.Handler {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	if s.Hub == nil {
		s.Hub = NewHub()
	}
	if s.GitHubWebURL == "" {
		s.GitHubWebURL = "https://github.com"
	}
	if s.SessionTTL <= 0 {
		s.SessionTTL = 12 * time.Hour
	}
	s.limiter = newRateLimiter(5, time.Minute)
	r := chi.NewRouter()
	r.Use(s.recoverer, securityHeaders)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok"})
	})
	r.Get("/readyz", s.readyz)
	r.Post("/webhooks/github", s.githubWebhook)
	r.Route("/api", func(r chi.Router) {
		r.Use(noStore)
		r.Post("/auth/login", s.login)
		r.Group(func(r chi.Router) {
			r.Use(s.requireSession, s.requireCSRF)
			r.Post("/auth/logout", s.logout)
			r.Get("/auth/me", s.me)
			r.Get("/overview", s.overview)
			r.Get("/projects", s.listProjects)
			r.Post("/projects", s.createProject)
			r.Get("/projects/{id}", s.getProject)
			r.Get("/projects/{id}/git-graph", s.projectGitGraph)
			r.Patch("/projects/{id}", s.patchProject)
			r.Get("/agents", s.listAgents)
			r.Get("/hermes-profiles", s.listHermesProfiles)
			r.Post("/agents/sync", s.syncAgents)
			r.Patch("/agents/{role}", s.patchAgent)
			r.Get("/features", s.listFeatures)
			r.Post("/features", s.createFeature)
			r.Get("/features/{id}", s.getFeature)
			r.Post("/features/{id}/actions", s.featureAction)
			r.Get("/tickets/{id}", s.getTicket)
			r.Post("/tickets/{id}/actions", s.ticketAction)
			r.Get("/pipeline", s.pipeline)
			r.Get("/runs", s.listRuns)
			r.Get("/runs/{id}", s.getRun)
			r.Post("/runs/{id}/stop", s.stopRun)
			r.Get("/approvals", s.listApprovals)
			r.Get("/approvals/{id}", s.getApproval)
			r.Post("/approvals/{id}/decision", s.decideApproval)
			r.Get("/audit", s.listAudit)
			r.Get("/system/health", s.systemHealth)
			r.Post("/system/dispatch", s.setDispatch)
			r.Get("/events", s.events)
		})
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, 404, "not_found", "ressource introuvable")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, 405, "method_not_allowed", "méthode non autorisée")
		})
	})
	r.NotFound(s.spa)
	return r
}

// ------------------------------------------------------------------ réponses

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]apiError{"error": {Code: code, Message: msg}})
}

// fail convertit une erreur métier en réponse HTTP.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ue *engine.UserError
	var te *domain.TransitionError
	switch {
	case errors.As(err, &ue):
		writeError(w, ue.Status, ue.Code, ue.Message)
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "not_found", "ressource introuvable")
	case errors.Is(err, store.ErrAlreadyDecided):
		writeError(w, 409, "already_decided", "cette approbation a déjà été décidée")
	case errors.Is(err, store.ErrVersionConflict):
		writeError(w, 409, "version_conflict", "modification concurrente, rechargez la page")
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, 409, "duplicate", "cette ressource existe déjà")
	case errors.As(err, &te):
		writeError(w, 409, "invalid_transition", "action impossible dans l'état actuel ("+te.From+")")
	case errors.Is(err, context.Canceled):
		writeError(w, 503, "cancelled", "requête annulée")
	default:
		s.Log.Error("erreur interne", "path", r.URL.Path, "error", err.Error())
		writeError(w, 500, "internal", "erreur interne")
	}
}

// decode lit un corps JSON strict (clés inconnues refusées, 1 Mio max).
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeError(w, 400, "invalid_content_type", "le corps doit être en JSON (Content-Type: application/json)")
		return false
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, 400, "invalid_json", "corps JSON invalide : "+sanitizeJSONErr(err))
		return false
	}
	if dec.More() {
		writeError(w, 400, "invalid_json", "corps JSON invalide : données en trop")
		return false
	}
	return true
}

func sanitizeJSONErr(err error) string {
	msg := err.Error()
	if strings.HasPrefix(msg, "json: unknown field ") {
		return "champ inconnu " + strings.TrimPrefix(msg, "json: unknown field ")
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return "type invalide pour " + te.Field
	}
	return "syntaxe incorrecte"
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "invalid_id", "identifiant invalide")
		return 0, false
	}
	return id, true
}

func page(w http.ResponseWriter, r *http.Request) (store.Page, bool) {
	p := store.Page{Limit: 50}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, 400, "invalid_limit", "limit doit être compris entre 1 et 200")
			return p, false
		}
		p.Limit = n
	}
	if v := r.URL.Query().Get("cursor"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, 400, "invalid_cursor", "curseur invalide")
			return p, false
		}
		p.Cursor = n
	}
	return p, true
}

func queryInt(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, 400, "invalid_"+key, fmt.Sprintf("paramètre %s invalide", key))
		return 0, false
	}
	return n, true
}

// ------------------------------------------------------------------ middlewares

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panique dans un handler", "path", r.URL.Path, "panic", fmt.Sprint(v))
				writeError(w, 500, "internal", "erreur interne")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ------------------------------------------------------------------ readyz

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		writeError(w, 503, "database_unavailable", "base de données injoignable")
		return
	}
	v, err := s.Store.SchemaVersion(r.Context())
	if err != nil || v != store.ExpectedSchemaVersion() {
		writeError(w, 503, "migrations_pending", "migrations non appliquées")
		return
	}
	writeJSON(w, 200, map[string]any{"status": "ready", "schema_version": v})
}

// ------------------------------------------------------------------ SPA

func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, 404, "not_found", "ressource introuvable")
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/webhooks/") {
		writeError(w, 404, "not_found", "ressource introuvable")
		return
	}
	if s.Web == nil {
		s.noUI(w)
		return
	}
	if _, err := fs.Stat(s.Web, "index.html"); err != nil {
		s.noUI(w)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p != "" && !strings.Contains(p, "..") {
		if st, err := fs.Stat(s.Web, p); err == nil && !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.Web, p)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-cache")
	b, err := fs.ReadFile(s.Web, "index.html")
	if err != nil {
		s.noUI(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) noUI(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(200)
	_, _ = io.WriteString(w, "Agentic OS : l'interface web n'est pas incluse dans ce binaire (web/dist vide). L'API est disponible sous /api.\n")
}
