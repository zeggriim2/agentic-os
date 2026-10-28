// Package hermes est le client de l'API Hermes (/p/<profil>/v1/runs).
package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

// KeySource fournit la clé API d'un profil.
type KeySource interface {
	HermesKey(profile string) (string, error)
}

// Client appelle l'API Hermes.
type Client struct {
	BaseURL string
	HTTP    *http.Client
	Keys    KeySource
}

// New construit un client avec un timeout par requête raisonnable.
func New(baseURL string, keys KeySource) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Keys: keys, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// RunRequest est le corps de création de run.
type RunRequest struct {
	Input        string `json:"input"`
	Instructions string `json:"instructions"`
}

// CreateResult est la réponse 202.
type CreateResult struct {
	RunID    string `json:"run_id"`
	Status   string `json:"status"`
	Replayed bool   `json:"replayed"`
}

// Usage décrit la consommation de tokens.
type Usage struct {
	InputTokens  int64 `json:"input_tokens"` // inclut les tokens relus du cache
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
	// Tokens d'entrée relus du cache du fournisseur (comptés dans InputTokens) / écrits en cache.
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

// Runtime décrit le modèle effectif.
type Runtime struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// RunStatus est la réponse de GET /runs/<id>.
type RunStatus struct {
	Status      string  `json:"status"`
	Output      string  `json:"output"`
	Usage       Usage   `json:"usage"`
	Runtime     Runtime `json:"runtime"`
	Completed   bool    `json:"completed"`
	Partial     bool    `json:"partial"`
	Interrupted bool    `json:"interrupted"`
	Error       string  `json:"error"`
}

// Terminal indique si Hermes considère le run terminé.
func (s RunStatus) Terminal() bool {
	switch s.Status {
	case "completed", "failed", "cancelled", "canceled", "stopped", "interrupted", "error":
		return true
	}
	return false
}

// NormalizedStatus mappe le statut Hermes vers un statut de run Agentic OS.
func (s RunStatus) NormalizedStatus() string {
	switch s.Status {
	case "completed":
		return domain.RunCompleted
	case "failed", "error":
		return domain.RunFailed
	case "cancelled", "canceled", "stopped", "interrupted":
		return domain.RunStopped
	}
	return domain.RunRunning
}

// Event est un événement SSE.
type Event struct {
	Event string          `json:"event"`
	Seq   int             `json:"seq"`
	Raw   json.RawMessage `json:"-"`
}

// IsDelta indique un événement de flux non persisté individuellement.
func (e Event) IsDelta() bool { return strings.HasSuffix(e.Event, ".delta") }

// Error est une erreur Hermes classée.
type Error struct {
	Status int
	Code   string
	Class  string
	Msg    string
}

func (e *Error) Error() string {
	if e.Status > 0 {
		return fmt.Sprintf("hermes: HTTP %d %s: %s", e.Status, e.Code, e.Msg)
	}
	return "hermes: " + e.Msg
}

// Erreurs sentinelles.
var (
	ErrConflict = errors.New("idempotency_key_conflict")
	ErrNotFound = errors.New("run_not_found")
)

// Is permet errors.Is(err, ErrConflict/ErrNotFound).
func (e *Error) Is(target error) bool {
	switch target {
	case ErrConflict:
		return e.Status == http.StatusConflict
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	}
	return false
}

// ClassOf renvoie la classe d'erreur d'Agentic OS.
func ClassOf(err error) string {
	var he *Error
	if errors.As(err, &he) {
		return he.Class
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.ErrClassTimeout
	}
	return domain.ErrClassInternal
}

const maxBody = 8 << 20

func (c *Client) do(ctx context.Context, method, profile, path string, body any, headers map[string]string, out any) (int, error) {
	if !domain.ValidProfile(profile) {
		return 0, &Error{Class: domain.ErrClassInternal, Msg: "profil invalide"}
	}
	key, err := c.Keys.HermesKey(profile)
	if err != nil {
		return 0, &Error{Class: domain.ErrClassInternal, Msg: "clé du profil indisponible"}
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.BaseURL + "/p/" + url.PathEscape(profile) + "/v1" + path
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, &Error{Class: domain.ErrClassProvider, Msg: "Hermes injoignable"}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, httpError(resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, &Error{Status: resp.StatusCode, Class: domain.ErrClassProvider, Msg: "réponse Hermes illisible"}
		}
	}
	return resp.StatusCode, nil
}

func httpError(status int, data []byte) error {
	var payload struct {
		Error any    `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(data, &payload)
	code := payload.Code
	switch v := payload.Error.(type) {
	case string:
		if code == "" {
			code = v
		}
	case map[string]any:
		if c, ok := v["code"].(string); ok && code == "" {
			code = c
		}
	}
	e := &Error{Status: status, Code: code, Msg: http.StatusText(status)}
	switch {
	case status == http.StatusConflict:
		e.Class = domain.ErrClassInternal
		if e.Code == "" {
			e.Code = "idempotency_key_conflict"
		}
	case status == http.StatusNotFound:
		e.Class = domain.ErrClassProvider
		if e.Code == "" {
			e.Code = "run_not_found"
		}
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.Class = domain.ErrClassPolicy
	case status == http.StatusTooManyRequests || status >= 500:
		e.Class = domain.ErrClassProvider
	default:
		e.Class = domain.ErrClassInternal
	}
	return e
}

// CreateRun soumet un run (idempotent par clé).
func (c *Client) CreateRun(ctx context.Context, profile, idempotencyKey string, req RunRequest) (CreateResult, error) {
	var out CreateResult
	if idempotencyKey == "" {
		return out, &Error{Class: domain.ErrClassInternal, Msg: "clé d'idempotence obligatoire"}
	}
	status, err := c.do(ctx, http.MethodPost, profile, "/runs", req, map[string]string{"Idempotency-Key": idempotencyKey}, &out)
	if err != nil {
		return out, err
	}
	if status != http.StatusAccepted && status != http.StatusOK && status != http.StatusCreated {
		return out, &Error{Status: status, Class: domain.ErrClassProvider, Msg: "statut inattendu"}
	}
	if out.RunID == "" {
		return out, &Error{Status: status, Class: domain.ErrClassProvider, Msg: "run_id absent"}
	}
	return out, nil
}

// GetRun lit l'état d'un run. 404 → ErrNotFound (run perdu).
func (c *Client) GetRun(ctx context.Context, profile, runID string) (RunStatus, error) {
	var out RunStatus
	_, err := c.do(ctx, http.MethodGet, profile, "/runs/"+url.PathEscape(runID), nil, nil, &out)
	return out, err
}

// StopRun demande l'arrêt d'un run.
func (c *Client) StopRun(ctx context.Context, profile, runID string) error {
	_, err := c.do(ctx, http.MethodPost, profile, "/runs/"+url.PathEscape(runID)+"/stop", map[string]any{}, nil, nil)
	return err
}

// Capabilities sert de health check par profil.
func (c *Client) Capabilities(ctx context.Context, profile string) error {
	var out json.RawMessage
	_, err := c.do(ctx, http.MethodGet, profile, "/capabilities", nil, nil, &out)
	return err
}

// Models liste les modèles exposés par le profil (si disponible).
func (c *Client) Models(ctx context.Context, profile string) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if _, err := c.do(ctx, http.MethodGet, profile, "/models", nil, nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		if d.ID != "" {
			ids = append(ids, d.ID)
		}
	}
	return ids, nil
}

// StreamEvents consomme le flux SSE et appelle fn pour chaque ligne `data:` JSON.
// Retourne nil à la fin du flux.
func (c *Client) StreamEvents(ctx context.Context, profile, runID string, fn func(Event) error) error {
	if !domain.ValidProfile(profile) {
		return &Error{Class: domain.ErrClassInternal, Msg: "profil invalide"}
	}
	key, err := c.Keys.HermesKey(profile)
	if err != nil {
		return &Error{Class: domain.ErrClassInternal, Msg: "clé du profil indisponible"}
	}
	u := c.BaseURL + "/p/" + url.PathEscape(profile) + "/v1/runs/" + url.PathEscape(runID) + "/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	hc := *c.HTTP
	hc.Timeout = 0 // flux long : borné par le contexte
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Class: domain.ErrClassProvider, Msg: "flux d'événements injoignable"}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return httpError(resp.StatusCode, data)
	}
	return ParseSSE(resp.Body, fn)
}

// ParseSSE lit des lignes SSE et décode chaque `data:` JSON en Event.
func ParseSSE(r io.Reader, fn func(Event) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(payload), &ev); err != nil || ev.Event == "" {
			continue
		}
		ev.Raw = json.RawMessage(payload)
		if err := fn(ev); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return &Error{Class: domain.ErrClassProvider, Msg: "flux d'événements interrompu"}
	}
	return nil
}
