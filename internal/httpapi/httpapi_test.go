package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/engine"
	gh "github.com/zeggriim2/agentic-os/internal/github"
	"github.com/zeggriim2/agentic-os/internal/gitops"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/httpapi"
	"github.com/zeggriim2/agentic-os/internal/notify"
	"github.com/zeggriim2/agentic-os/internal/store"
	"github.com/zeggriim2/agentic-os/internal/testutil/pgtest"
)

const password = "mot-de-passe-solide"

var webhookSecret = []byte("whsec-test")

type client struct {
	t    *testing.T
	srv  *httptest.Server
	http *http.Client
	csrf string
	s    *store.Store
	e    *engine.Engine
	hub  *httpapi.Hub
}

func setup(t *testing.T, web fstest.MapFS) *client {
	t.Helper()
	return setupWith(t, web, nil)
}

// setupWith permet d'ajuster le serveur (dépendances optionnelles) avant son démarrage.
func setupWith(t *testing.T, web fstest.MapFS, mod func(*httpapi.Server)) *client {
	t.Helper()
	s := pgtest.New(t)
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	eng := &engine.Engine{Store: s, Log: log, MaxRework: 3}
	eng.Init()
	hub := httpapi.NewHub()
	srv := &httpapi.Server{
		Store: s, Engine: eng, Log: log, Hub: hub, AdminLogin: "admin",
		PasswordHash:  func() (string, error) { return string(hash), nil },
		SessionKey:    bytes.Repeat([]byte("k"), 32),
		WebhookSecret: func() ([]byte, error) { return webhookSecret, nil },
		Version:       "test", Revision: "abc",
	}
	if web != nil {
		srv.Web = web
	}
	if mod != nil {
		mod(srv)
	}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, srv: ts, http: &http.Client{Jar: jar}, s: s, e: eng, hub: hub}
}

type resp struct {
	status int
	body   map[string]any
	raw    string
	header http.Header
}

func (c *client) do(method, path string, body any, csrf bool) resp {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		if s, ok := body.(string); ok {
			rdr = strings.NewReader(s)
		} else {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		}
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf && c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	r := resp{status: res.StatusCode, raw: string(raw), header: res.Header}
	_ = json.Unmarshal(raw, &r.body)
	return r
}

func (c *client) login() {
	c.t.Helper()
	r := c.do("POST", "/api/auth/login", map[string]string{"login": "admin", "password": password}, false)
	if r.status != 200 {
		c.t.Fatalf("login: %d %s", r.status, r.raw)
	}
	c.csrf = r.body["csrf_token"].(string)
}

func errCode(r resp) string {
	e, _ := r.body["error"].(map[string]any)
	if e == nil {
		return ""
	}
	if m, _ := e["message"].(string); m == "" {
		return "message-absent"
	}
	return e["code"].(string)
}

func expectStatus(t *testing.T, what string, r resp, status int, code string) {
	t.Helper()
	if r.status != status || (code != "" && errCode(r) != code) {
		t.Fatalf("%s : obtenu %d %s, attendu %d %s", what, r.status, r.raw, status, code)
	}
}

func TestAuthSessionAndCSRF(t *testing.T) {
	c := setup(t, nil)
	expectStatus(t, "sans session", c.do("GET", "/api/overview", nil, false), 401, "unauthenticated")
	expectStatus(t, "mauvais mot de passe", c.do("POST", "/api/auth/login", map[string]string{"login": "admin", "password": "faux"}, false), 401, "invalid_credentials")
	expectStatus(t, "mauvais login", c.do("POST", "/api/auth/login", map[string]string{"login": "root", "password": password}, false), 401, "invalid_credentials")
	r := c.do("POST", "/api/auth/login", map[string]string{"login": "admin", "password": password}, false)
	expectStatus(t, "login", r, 200, "")
	c.csrf = r.body["csrf_token"].(string)
	setCookie := r.header.Get("Set-Cookie")
	for _, want := range []string{"aos_session=", "HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(setCookie, want) {
			t.Fatalf("cookie sans %s : %s", want, setCookie)
		}
	}
	me := c.do("GET", "/api/auth/me", nil, false)
	if me.status != 200 || me.body["login"] != "admin" || me.body["csrf_token"] != c.csrf {
		t.Fatalf("me: %s", me.raw)
	}
	project := map[string]any{"slug": "demo", "name": "Demo", "repository": "o/r", "required_checks": []string{"tests"}}
	expectStatus(t, "POST sans CSRF", c.do("POST", "/api/projects", project, false), 403, "csrf_invalid")
	saved := c.csrf
	c.csrf = "faux"
	expectStatus(t, "POST CSRF faux", c.do("POST", "/api/projects", project, true), 403, "csrf_invalid")
	c.csrf = saved
	expectStatus(t, "POST avec CSRF", c.do("POST", "/api/projects", project, true), 201, "")
	expectStatus(t, "doublon", c.do("POST", "/api/projects", project, true), 409, "duplicate")
	expectStatus(t, "logout sans CSRF", c.do("POST", "/api/auth/logout", map[string]any{}, false), 403, "csrf_invalid")
	expectStatus(t, "logout", c.do("POST", "/api/auth/logout", map[string]any{}, true), 200, "")
	expectStatus(t, "après logout", c.do("GET", "/api/auth/me", nil, false), 401, "unauthenticated")
}

func TestLoginRateLimit(t *testing.T) {
	c := setup(t, nil)
	for i := 0; i < 5; i++ {
		expectStatus(t, "tentative", c.do("POST", "/api/auth/login", map[string]string{"login": "admin", "password": "x"}, false), 401, "invalid_credentials")
	}
	expectStatus(t, "6e tentative", c.do("POST", "/api/auth/login", map[string]string{"login": "admin", "password": password}, false), 429, "too_many_attempts")
}

func TestPayloadValidationAndErrorFormat(t *testing.T) {
	c := setup(t, nil)
	c.login()
	expectStatus(t, "champ inconnu", c.do("POST", "/api/projects", map[string]any{"slug": "a", "name": "A", "repository": "o/r", "evil": 1}, true), 400, "invalid_json")
	expectStatus(t, "json cassé", c.do("POST", "/api/projects", "{", true), 400, "invalid_json")
	expectStatus(t, "slug invalide", c.do("POST", "/api/projects", map[string]any{"slug": "../x", "name": "A", "repository": "o/r"}, true), 422, "invalid_slug")
	expectStatus(t, "dépôt invalide", c.do("POST", "/api/projects", map[string]any{"slug": "a", "name": "A", "repository": "pas un dépôt"}, true), 422, "invalid_repository")
	expectStatus(t, "projet absent", c.do("POST", "/api/features", map[string]any{"project_id": 999, "title": "T", "description": "D"}, true), 422, "invalid_project")
	expectStatus(t, "titre vide", c.do("POST", "/api/features", map[string]any{"project_id": 1, "title": " "}, true), 422, "invalid_title")
	expectStatus(t, "limit", c.do("GET", "/api/features?limit=500", nil, false), 400, "invalid_limit")
	expectStatus(t, "id", c.do("GET", "/api/features/abc", nil, false), 400, "invalid_id")
	expectStatus(t, "inexistant", c.do("GET", "/api/features/12345", nil, false), 404, "not_found")
	expectStatus(t, "route inconnue", c.do("GET", "/api/nope", nil, false), 404, "not_found")
	expectStatus(t, "action invalide", c.do("POST", "/api/features/1/actions", map[string]any{"action": "deploy"}, true), 422, "invalid_action")
	expectStatus(t, "dispatch sans champ", c.do("POST", "/api/system/dispatch", map[string]any{}, true), 422, "invalid_request")
	r := c.do("POST", "/api/projects", map[string]any{"slug": "a", "name": "A", "repository": "o/r", "evil": 1}, true)
	if msg := r.body["error"].(map[string]any)["message"].(string); !strings.Contains(msg, "champ inconnu") {
		t.Fatalf("message non français: %s", msg)
	}
	expectStatus(t, "dispatch", c.do("POST", "/api/system/dispatch", map[string]any{"paused": true}, true), 200, "")
	ov := c.do("GET", "/api/overview", nil, false)
	if ov.body["dispatch_paused"] != true || ov.body["tokens_7d"] == nil {
		t.Fatalf("overview: %s", ov.raw)
	}
}

func TestProductProjectRequiresConfirmation(t *testing.T) {
	c := setup(t, nil)
	c.login()
	r := c.do("POST", "/api/projects", map[string]any{"slug": "prod", "name": "Prod", "repository": "o/prod", "mode": "product"}, true)
	expectStatus(t, "création", r, 201, "")
	if r.body["enabled"] != false {
		t.Fatal("un projet doit être créé désactivé")
	}
	id := itoa(r.body["id"])
	expectStatus(t, "sans confirmation", c.do("PATCH", "/api/projects/"+id, map[string]any{"enabled": true}, true), 422, "confirmation_required")
	expectStatus(t, "mauvaise confirmation", c.do("PATCH", "/api/projects/"+id, map[string]any{"enabled": true, "confirm": "autre"}, true), 422, "confirmation_required")
	r = c.do("PATCH", "/api/projects/"+id, map[string]any{"enabled": true, "confirm": "prod"}, true)
	if r.status != 200 || r.body["enabled"] != true {
		t.Fatalf("activation: %s", r.raw)
	}
}

func TestProjectStructuralFieldsFrozenWhileFeatureActive(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx := context.Background()
	p, err := store.CreateProject(ctx, c.s.Pool, store.ProjectInput{Slug: "frozen", Name: "Frozen", Repository: "o/r", BaseBranch: "develop",
		RequiredChecks: []string{"tests"}, Mode: "sandbox", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	f, err := store.CreateFeature(ctx, c.s.Pool, p.ID, "Active", "")
	if err != nil {
		t.Fatal(err)
	}
	f, err = store.SetFeatureState(ctx, c.s.Pool, f.ID, f.Version, string(domain.FeatureSpec), "", "")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/projects/" + itoa(p.ID)
	expectStatus(t, "repository figé", c.do("PATCH", path, map[string]any{"repository": "o/other"}, true), 409, "project_in_use")
	expectStatus(t, "nom modifiable", c.do("PATCH", path, map[string]any{"name": "Nouveau nom"}, true), 200, "")
	if _, err := store.SetFeatureState(ctx, c.s.Pool, f.ID, f.Version, string(domain.FeatureDone), "", ""); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, "repository modifiable après fin", c.do("PATCH", path, map[string]any{"repository": "o/other"}, true), 200, "")
}

func itoa(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestApprovalDecisionAndProjection(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx := context.Background()
	p, _ := store.CreateProject(ctx, c.s.Pool, store.ProjectInput{Slug: "demo", Name: "Demo", Repository: "o/r", BaseBranch: "main", Mode: "sandbox", Enabled: true})
	f, _ := store.CreateFeature(ctx, c.s.Pool, p.ID, "F", "")
	f, _ = store.SetFeatureState(ctx, c.s.Pool, f.ID, f.Version, "SPEC", "", "")
	_, _ = store.SetFeatureState(ctx, c.s.Pool, f.ID, f.Version, "SPEC_REVIEW", "", "")
	ap, _, _ := store.CreateApproval(ctx, c.s.Pool, store.NewApproval{Kind: "spec_review", SubjectType: "feature", SubjectID: f.ID, Title: "Spec", Effect: "Passer en conception"})
	path := "/api/approvals/" + itoa(ap.ID) + "/decision"
	expectStatus(t, "décision invalide", c.do("POST", path, map[string]any{"decision": "maybe"}, true), 422, "invalid_decision")
	expectStatus(t, "rejet sans commentaire", c.do("POST", path, map[string]any{"decision": "rejected", "comment": ""}, true), 422, "comment_required")
	r := c.do("POST", path, map[string]any{"decision": "approved", "comment": "ok"}, true)
	if r.status != 200 || r.body["status"] != "approved" || r.body["decided_by"] != "user:admin" || r.body["executed_at"] != nil {
		t.Fatalf("décision: %s", r.raw)
	}
	if did, err := c.e.RunOnce(ctx, "http-test-worker"); err != nil || !did {
		t.Fatalf("exécution durable: did=%v err=%v", did, err)
	}
	r = c.do("GET", "/api/approvals/"+itoa(ap.ID), nil, false)
	if r.status != 200 || r.body["status"] != "executed" || r.body["executed_at"] == nil {
		t.Fatalf("approbation non exécutée: %s", r.raw)
	}
	expectStatus(t, "décision répétée", c.do("POST", path, map[string]any{"decision": "rejected", "comment": "non"}, true), 409, "already_decided")
	expectStatus(t, "approbation inconnue", c.do("POST", "/api/approvals/99999/decision", map[string]any{"decision": "approved"}, true), 404, "not_found")

	// la feature est passée en TECH_DESIGN avec un job tech_design : vérifier la projection
	_, _ = c.s.Pool.Exec(ctx, `UPDATE jobs SET lease_owner='host-secret-owner'`)
	det := c.do("GET", "/api/features/"+itoa(f.ID), nil, false)
	if det.status != 200 || det.body["feature"].(map[string]any)["state"] != "TECH_DESIGN" {
		t.Fatalf("détail: %s", det.raw)
	}
	for _, forbidden := range []string{"idempotency_key", "lease_owner", "host-secret-owner", "tech_design:feature:"} {
		if strings.Contains(det.raw, forbidden) {
			t.Fatalf("la projection expose %q", forbidden)
		}
	}
	tk, _ := store.InsertTicket(ctx, c.s.Pool, store.TicketInput{FeatureID: f.ID, ProjectID: p.ID, Key: "demo-F1-T1", Seq: 1, Title: "T"})
	td := c.do("GET", "/api/tickets/"+itoa(tk.ID), nil, false)
	wt := td.body["ticket"].(map[string]any)["worktree"].(string)
	if wt != "demo/demo-F1-T1" {
		t.Fatalf("worktree non relatif: %q", wt)
	}
	pl := c.do("GET", "/api/pipeline", nil, false)
	if pl.status != 200 || len(pl.body["features"].(map[string]any)["columns"].([]any)) == 0 {
		t.Fatalf("pipeline: %s", pl.raw)
	}
	lst := c.do("GET", "/api/approvals?status=executed", nil, false)
	if items := lst.body["items"].([]any); len(items) != 1 || lst.body["next_cursor"] != nil {
		t.Fatalf("liste: %s", lst.raw)
	}
	au := c.do("GET", "/api/audit?subject_type=feature&subject_id="+itoa(f.ID)+"&limit=1", nil, false)
	if au.body["next_cursor"] == nil {
		t.Fatalf("pagination: %s", au.raw)
	}
}

func signedWebhook(c *client, body, delivery, sig string) resp {
	req, _ := http.NewRequest("POST", c.srv.URL+"/webhooks/github", strings.NewReader(body))
	req.Header.Set("X-GitHub-Event", "pull_request")
	req.Header.Set("X-GitHub-Delivery", delivery)
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	r := resp{status: res.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &r.body)
	return r
}

func TestGitHubWebhookHMACAndDedup(t *testing.T) {
	c := setup(t, nil)
	body := `{"action":"closed","repository":{"full_name":"o/r"},"pull_request":{"number":7,"head":{"sha":"x","ref":"aos/demo-F1-T1"}}}`
	expectStatus(t, "sans signature", signedWebhook(c, body, "d1", ""), 401, "invalid_signature")
	expectStatus(t, "signature invalide", signedWebhook(c, body, "d1", gh.Sign([]byte("autre"), []byte(body))), 401, "invalid_signature")
	expectStatus(t, "corps modifié", signedWebhook(c, body+" ", "d1", gh.Sign(webhookSecret, []byte(body))), 401, "invalid_signature")
	r := signedWebhook(c, body, "d1", gh.Sign(webhookSecret, []byte(body)))
	if r.status != 200 || r.body["status"] != "processed" {
		t.Fatalf("valide: %s", r.raw)
	}
	r = signedWebhook(c, body, "d1", gh.Sign(webhookSecret, []byte(body)))
	if r.status != 200 || r.body["status"] != "duplicate" {
		t.Fatalf("doublon: %s", r.raw)
	}
}

func TestSecurityHeadersAndSPA(t *testing.T) {
	web := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	c := setup(t, web)
	r := c.do("GET", "/features/12", nil, false)
	if r.status != 200 || !strings.Contains(r.raw, "<html>app</html>") {
		t.Fatalf("fallback SPA: %d %s", r.status, r.raw)
	}
	if csp := r.header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP: %s", csp)
	}
	if r.header.Get("X-Content-Type-Options") != "nosniff" || r.header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("en-têtes de sécurité absents")
	}
	if a := c.do("GET", "/assets/app.js", nil, false); a.status != 200 || a.raw != "console.log(1)" {
		t.Fatalf("asset: %d", a.status)
	}
	if a := c.do("GET", "/api/unknown", nil, false); a.status != 401 && a.status != 404 {
		t.Fatalf("api inconnue: %d", a.status)
	}
	if h := c.do("GET", "/healthz", nil, false); h.status != 200 {
		t.Fatal("healthz")
	}
	if h := c.do("GET", "/readyz", nil, false); h.status != 200 {
		t.Fatalf("readyz: %s", h.raw)
	}
	empty := setup(t, fstest.MapFS{".gitkeep": {Data: nil}})
	if r := empty.do("GET", "/", nil, false); r.status != 200 || !strings.Contains(r.raw, "interface web n'est pas incluse") {
		t.Fatalf("sans UI: %s", r.raw)
	}
}

func TestSSEReceivesCommittedChanges(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go httpapi.ListenPG(ctx, c.s.Pool, c.hub, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	req, _ := http.NewRequestWithContext(ctx, "GET", c.srv.URL+"/api/events", nil)
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type: %s", res.Header.Get("Content-Type"))
	}
	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	// attendre l'abonnement et la connexion LISTEN
	deadline := time.Now().Add(5 * time.Second)
	for c.hub.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	p, _ := store.CreateProject(ctx, c.s.Pool, store.ProjectInput{Slug: "demo", Name: "Demo", Repository: "o/r", BaseBranch: "main", Mode: "sandbox"})
	if r := c.do("POST", "/api/features", map[string]any{"project_id": p.ID, "title": "Nouvelle", "description": "Décrire l'idée"}, true); r.status != 201 {
		t.Fatalf("création: %s", r.raw)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case l := <-lines:
			if l == "event: feature.updated" {
				return
			}
		case <-timeout:
			t.Fatal("événement SSE feature.updated non reçu")
		}
	}
}

func TestSSERunEventCarriesPersistedEvent(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go httpapi.ListenPG(ctx, c.s.Pool, c.hub, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	job, _, err := store.EnqueueJob(ctx, c.s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "sse-run"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRun(ctx, c.s.Pool, job.ID, "po", "aos-po")
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", c.srv.URL+"/api/events", nil)
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for c.hub.Subscribers() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)
	emit := func(seq int) {
		err := c.s.InTx(ctx, func(tx pgx.Tx) error {
			ins, err := store.InsertRunEvent(ctx, tx, run.ID, seq, "tool.started", []byte(`{"event":"tool.started","tool":"terminal"}`))
			if err != nil || !ins {
				return fmt.Errorf("insertion seq %d: %v", seq, err)
			}
			return store.NotifyRunEvent(ctx, tx, run.ID, seq)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	emit(7)
	var payload map[string]any
	timeout := time.After(5 * time.Second)
	for payload == nil {
		select {
		case l := <-lines:
			if l != "event: run.event" {
				continue
			}
			data := strings.TrimPrefix(<-lines, "data: ")
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
				t.Fatalf("payload illisible: %s", data)
			}
		case <-timeout:
			t.Fatal("événement SSE run.event non reçu")
		}
	}
	if payload["run_id"] != float64(run.ID) || payload["seq"] != float64(7) || payload["event"] != "tool.started" {
		t.Fatalf("payload SSE incomplet: %v", payload)
	}
	if d, _ := payload["data"].(map[string]any); d["tool"] != "terminal" {
		t.Fatalf("data absente: %v", payload)
	}
	if ts, _ := payload["created_at"].(string); ts == "" {
		t.Fatalf("created_at absent: %v", payload)
	}
	// Le détail du run expose le même format (created_at, plus de champ "at").
	emit(8)
	det := c.do("GET", "/api/runs/"+itoa(run.ID), nil, false)
	evs := det.body["events"].([]any)
	if len(evs) != 2 {
		t.Fatalf("événements persistés: %s", det.raw)
	}
	first := evs[0].(map[string]any)
	if first["created_at"] == nil || first["at"] != nil || first["run_id"] != float64(run.ID) || first["seq"] != float64(7) {
		t.Fatalf("format REST incohérent avec SSE: %v", first)
	}
}

func TestTruncationIsExplicitAndKeepsMostRecent(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx := context.Background()
	job, _, _ := store.EnqueueJob(ctx, c.s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "trunc"})
	run, _ := store.CreateRun(ctx, c.s.Pool, job.ID, "po", "aos-po")
	if _, err := c.s.Pool.Exec(ctx, `INSERT INTO run_events (run_id, seq, event) SELECT $1, g, 'tool.x' FROM generate_series(1, $2) g`,
		run.ID, store.MaxRunEvents+5); err != nil {
		t.Fatal(err)
	}
	det := c.do("GET", "/api/runs/"+itoa(run.ID), nil, false)
	evs := det.body["events"].([]any)
	if det.body["events_truncated"] != true || len(evs) != store.MaxRunEvents {
		t.Fatalf("troncature des événements: %v, %d", det.body["events_truncated"], len(evs))
	}
	first, last := evs[0].(map[string]any), evs[len(evs)-1].(map[string]any)
	if first["seq"] != float64(6) || last["seq"] != float64(store.MaxRunEvents+5) {
		t.Fatalf("les plus récents doivent être conservés, en ordre croissant: %v … %v", first["seq"], last["seq"])
	}
	p, _ := store.CreateProject(ctx, c.s.Pool, store.ProjectInput{Slug: "demo", Name: "Demo", Repository: "o/r", BaseBranch: "main", Mode: "sandbox"})
	f, _ := store.CreateFeature(ctx, c.s.Pool, p.ID, "F", "d")
	if _, err := c.s.Pool.Exec(ctx, `INSERT INTO audit_log (actor, action, subject_type, subject_id) SELECT 'system', 'test.' || g, 'feature', $1 FROM generate_series(1, $2) g`,
		f.ID, store.MaxTimeline+3); err != nil {
		t.Fatal(err)
	}
	fd := c.do("GET", "/api/features/"+itoa(f.ID), nil, false)
	tl := fd.body["timeline"].([]any)
	if fd.body["timeline_truncated"] != true || len(tl) != store.MaxTimeline {
		t.Fatalf("troncature timeline: %v %d", fd.body["timeline_truncated"], len(tl))
	}
	if a := tl[len(tl)-1].(map[string]any)["action"]; a != "test."+itoa(store.MaxTimeline+3) {
		t.Fatalf("dernière entrée: %v", a)
	}
	tk, _ := store.InsertTicket(ctx, c.s.Pool, store.TicketInput{FeatureID: f.ID, ProjectID: p.ID, Key: "demo-F1-T1", Seq: 1, Title: "T"})
	td := c.do("GET", "/api/tickets/"+itoa(tk.ID), nil, false)
	if td.body["timeline_truncated"] != false {
		t.Fatalf("timeline ticket non tronquée attendue: %v", td.body["timeline_truncated"])
	}
}

func TestAgentWithoutDesiredModelIsNotDrifting(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx := context.Background()
	m, prov := "observed-model", "p"
	if err := store.SetAgentObservation(ctx, c.s.Pool, "po", "SYNCED", &m, &prov); err != nil {
		t.Fatal(err)
	}
	r := c.do("PATCH", "/api/agents/po", map[string]any{"desired_model": ""}, true)
	if r.status != 200 || r.body["sync_state"] != "SYNCED" {
		t.Fatalf("aucun modèle imposé → SYNCED attendu: %s", r.raw)
	}
	r = c.do("PATCH", "/api/agents/po", map[string]any{"desired_model": "autre-modele"}, true)
	if r.body["sync_state"] != "DRIFT_DETECTED" {
		t.Fatalf("modèle différent → DRIFT attendu: %s", r.raw)
	}
	expectStatus(t, "budget sous le minimum", c.do("PATCH", "/api/agents/po", map[string]any{"max_tokens_per_run": 999}, true), 422, "invalid_max_tokens")
	expectStatus(t, "modèle invalide", c.do("PATCH", "/api/agents/po", map[string]any{"desired_model": "a b"}, true), 422, "invalid_model")
	if err := store.SetAgentObservation(ctx, c.s.Pool, "po", "UNREACHABLE", nil, nil); err != nil {
		t.Fatal(err)
	}
	r = c.do("PATCH", "/api/agents/po", map[string]any{"max_tokens_per_run": 5000}, true)
	if r.body["sync_state"] != "UNREACHABLE" {
		t.Fatalf("UNREACHABLE ne doit pas être masqué par un PATCH: %s", r.raw)
	}
	// Le lien d'alerte du dispatch en pause mène vers une route existante (vue d'ensemble).
	c.do("POST", "/api/system/dispatch", map[string]any{"paused": true}, true)
	ov := c.do("GET", "/api/overview", nil, false)
	if !strings.Contains(ov.raw, `"link":"/"`) || strings.Contains(ov.raw, "/system") {
		t.Fatalf("lien d'alerte: %s", ov.raw)
	}
}

// ------------------------------------------------------------------ graphe Git

type fakeGraph struct {
	calls int
	last  gitops.GraphQuery
	err   error
}

func (f *fakeGraph) Graph(_ context.Context, slug, repo string, q gitops.GraphQuery) (*gitops.Graph, error) {
	f.calls++
	f.last = q
	if f.err != nil {
		return nil, f.err
	}
	now := time.Now().UTC()
	return &gitops.Graph{Since: now.Add(-30 * 24 * time.Hour),
		Refs: []gitops.GraphRef{{Name: "main", Kind: gitops.KindMain, SHA: "m1", Date: now},
			{Name: "aos/g-F1-T1", Kind: gitops.KindFeature, SHA: "t1", Date: now}},
		Tags: []gitops.GraphTag{{Name: "v1.0.0", SHA: "m1"}},
		Commits: []gitops.GraphCommit{
			{SHA: "t1", Parents: []string{"m0"}, Subject: "ticket", Branch: "aos/g-F1-T1", Kind: gitops.KindFeature},
			{SHA: "m1", Parents: []string{"m0", "x"}, Subject: "Merge pull request #4 from o/b", Branch: "main", Kind: gitops.KindMain, PR: 4},
		}}, nil
}

func TestProjectGitGraph(t *testing.T) {
	c := setup(t, nil)
	fg := &fakeGraph{}
	// Second serveur sur la même base, avec la source de graphe et une URL web personnalisée.
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	srv := &httpapi.Server{Store: c.s, Engine: c.e, Log: log, Hub: httpapi.NewHub(), AdminLogin: "admin",
		PasswordHash: func() (string, error) { return string(hash), nil }, SessionKey: bytes.Repeat([]byte("k"), 32),
		WebhookSecret: func() ([]byte, error) { return webhookSecret, nil }, GitGraph: fg, GitHubWebURL: "https://gh.example/"}
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(ts.Close)
	c.srv = ts
	c.login()

	r := c.do("POST", "/api/projects", map[string]any{"slug": "g", "name": "G", "repository": "o/g"}, true)
	expectStatus(t, "projet", r, 201, "")
	pid := int64(r.body["id"].(float64))
	f, err := store.CreateFeature(context.Background(), c.s.Pool, pid, "F", "d")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.Pool.Exec(context.Background(), `INSERT INTO tickets (feature_id, project_id, key, seq, title, body, acceptance, state, branch, pr_number, pr_url)
		VALUES ($1,$2,'g-F1-T1',1,'T','b','[]','PR_OPENED','aos/g-F1-T1',12,'https://gh.example/o/g/pull/12')`, f.ID, pid); err != nil {
		t.Fatal(err)
	}

	path := fmt.Sprintf("/api/projects/%d/git-graph", pid)
	expectStatus(t, "days invalide", c.do("GET", path+"?days=0", nil, false), 400, "invalid_query")
	expectStatus(t, "days trop grand", c.do("GET", path+"?days=366", nil, false), 400, "invalid_query")
	expectStatus(t, "type inconnu", c.do("GET", path+"?kinds=main,evil", nil, false), 400, "invalid_query")
	expectStatus(t, "merged invalide", c.do("GET", path+"?merged=yes", nil, false), 400, "invalid_query")
	expectStatus(t, "projet absent", c.do("GET", "/api/projects/999999/git-graph", nil, false), 404, "not_found")
	if fg.calls != 0 {
		t.Fatal("dépôt lu malgré une requête invalide")
	}

	r = c.do("GET", path+"?days=7&kinds=feature,main", nil, false)
	expectStatus(t, "graphe", r, 200, "")
	if fg.last.Days != 7 || !fg.last.Kinds["main"] || !fg.last.Kinds["feature"] || fg.last.Kinds["develop"] || fg.last.IncludeMerged {
		t.Fatalf("paramètres transmis : %+v", fg.last)
	}
	refs := r.body["refs"].([]any)
	tr := refs[1].(map[string]any)
	if tk := tr["ticket"].(map[string]any); tk["key"] != "g-F1-T1" || tk["state"] != "PR_OPENED" {
		t.Fatalf("ticket non relié à sa branche : %v", tr)
	}
	if pr := tr["pr"].(map[string]any); pr["url"] != "https://gh.example/o/g/pull/12" {
		t.Fatalf("lien de PR du ticket : %v", pr)
	}
	if refs[0].(map[string]any)["ticket"] != nil {
		t.Fatal("ticket attaché à main")
	}
	cm := r.body["commits"].([]any)[1].(map[string]any)
	if cm["url"] != "https://gh.example/o/g/commit/m1" || cm["pr"].(map[string]any)["url"] != "https://gh.example/o/g/pull/4" {
		t.Fatalf("liens du commit : %v", cm)
	}
	if tag := r.body["tags"].([]any)[0].(map[string]any); tag["url"] != "https://gh.example/o/g/releases/tag/v1.0.0" {
		t.Fatalf("lien du tag : %v", tag)
	}

	// Cache 30 s ; refresh=1 force la relecture.
	c.do("GET", path+"?days=7&kinds=main,feature", nil, false)
	if fg.calls != 1 {
		t.Fatalf("cache non utilisé (ordre des types indifférent) : %d lectures", fg.calls)
	}
	c.do("GET", path+"?days=7&kinds=main,feature&refresh=1", nil, false)
	if fg.calls != 2 {
		t.Fatalf("refresh ignoré : %d lectures", fg.calls)
	}
	c.do("GET", path+"?days=7&kinds=main,feature&merged=1", nil, false)
	if fg.calls != 3 || !fg.last.IncludeMerged {
		t.Fatalf("merged=1 non transmis ou servi depuis le cache : %d %+v", fg.calls, fg.last)
	}

	// Erreur Git : message générique, aucun détail interne.
	fg.err = errors.New("fatal: could not read from https://secret.example/token")
	r = c.do("GET", path+"?days=8", nil, false)
	expectStatus(t, "erreur git", r, 502, "git_error")
	if strings.Contains(r.raw, "secret.example") {
		t.Fatal("détail Git renvoyé au client")
	}

	// Sans authentification.
	anon := &http.Client{}
	res, err := anon.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("graphe accessible sans session : %d", res.StatusCode)
	}
}

func TestRunAndOverviewExposeTokenSplit(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx := context.Background()
	job, _, err := store.EnqueueJob(ctx, c.s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "split"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := store.CreateRun(ctx, c.s.Pool, job.ID, "developer", "aos-developer")
	if err := store.FinishRun(ctx, c.s.Pool, run.ID, store.RunResult{Status: "completed", InputTokens: 305443, OutputTokens: 2637,
		TotalTokens: 308080, CacheReadTokens: 248960, CacheWriteTokens: 0}); err != nil {
		t.Fatal(err)
	}
	r := c.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, false)
	expectStatus(t, "run", r, 200, "")
	rv, _ := r.body["run"].(map[string]any)
	r.body = rv
	if r.body["fresh_input_tokens"] != float64(56483) || r.body["cache_read_tokens"] != float64(248960) ||
		r.body["input_tokens"] != float64(305443) || r.body["total_tokens"] != float64(308080) {
		t.Fatalf("répartition du run : %s", r.raw)
	}
	ov := c.do("GET", "/api/overview", nil, false)
	sp, _ := ov.body["tokens_today_split"].(map[string]any)
	if sp["fresh_input"] != float64(56483) || sp["cache_read"] != float64(248960) || sp["output"] != float64(2637) {
		t.Fatalf("répartition du jour : %s", ov.raw)
	}
	days, _ := ov.body["tokens_7d"].([]any)
	last, _ := days[len(days)-1].(map[string]any)
	if last["total"] != float64(308080) || last["fresh_input"] != float64(56483) || last["cache_read"] != float64(248960) || last["output"] != float64(2637) {
		t.Fatalf("série 7 jours : %v", last)
	}
}

func TestProjectAgentProfiles(t *testing.T) {
	c := setup(t, nil)
	c.login()
	base := map[string]any{"slug": "fz", "name": "FZ", "repository": "o/fz"}
	with := func(ap any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m["agent_profiles"] = ap
		return m
	}
	expectStatus(t, "rôle inconnu", c.do("POST", "/api/projects", with(map[string]string{"admin": "aos-x"}), true), 422, "invalid_agent_profiles")
	expectStatus(t, "profil invalide", c.do("POST", "/api/projects", with(map[string]string{"developer": "../x"}), true), 422, "invalid_agent_profiles")
	r := c.do("POST", "/api/projects", with(map[string]string{"developer": " aos-fz-developer ", "qa": ""}), true)
	expectStatus(t, "création", r, 201, "")
	ap, _ := r.body["agent_profiles"].(map[string]any)
	if len(ap) != 1 || ap["developer"] != "aos-fz-developer" {
		t.Fatalf("agent_profiles créés : %s", r.raw)
	}
	id := int64(r.body["id"].(float64))
	path := fmt.Sprintf("/api/projects/%d", id)
	r = c.do("PATCH", path, map[string]any{"name": "FZ2"}, true)
	if ap, _ := r.body["agent_profiles"].(map[string]any); r.status != 200 || ap["developer"] != "aos-fz-developer" {
		t.Fatalf("patch sans agent_profiles doit les conserver : %s", r.raw)
	}
	r = c.do("PATCH", path, map[string]any{"agent_profiles": map[string]string{"reviewer": "aos-fz-reviewer"}}, true)
	if ap, _ := r.body["agent_profiles"].(map[string]any); r.status != 200 || len(ap) != 1 || ap["reviewer"] != "aos-fz-reviewer" {
		t.Fatalf("patch remplace l'ensemble : %s", r.raw)
	}
	r = c.do("PATCH", path, map[string]any{"agent_profiles": map[string]string{}}, true)
	if ap, _ := r.body["agent_profiles"].(map[string]any); r.status != 200 || len(ap) != 0 {
		t.Fatalf("patch {} remet les profils par défaut : %s", r.raw)
	}
}

func TestHermesProfilesListAndProjectCheck(t *testing.T) {
	c := setupWith(t, nil, func(s *httpapi.Server) {
		s.HermesProfiles = func() ([]string, error) {
			return []string{"aos-developer", "aos-fz-developer", "aos-fz-qa", "aos-tech-lead", "perso"}, nil
		}
	})
	c.login()
	r := c.do("GET", "/api/hermes-profiles", nil, false)
	if r.status != 200 || !strings.Contains(r.raw, `{"profile":"aos-fz-developer","role":"developer"}`) ||
		!strings.Contains(r.raw, `{"profile":"aos-tech-lead","role":"tech_lead"}`) || !strings.Contains(r.raw, `{"profile":"perso","role":""}`) {
		t.Fatalf("liste des profils : %s", r.raw)
	}
	base := map[string]any{"slug": "fz", "name": "FZ", "repository": "o/fz"}
	base["agent_profiles"] = map[string]string{"developer": "aos-fz-developpeur"}
	expectStatus(t, "profil sans clé", c.do("POST", "/api/projects", base, true), 422, "unknown_agent_profile")
	base["agent_profiles"] = map[string]string{"developer": "aos-fz-developer", "qa": "aos-fz-qa"}
	r = c.do("POST", "/api/projects", base, true)
	expectStatus(t, "profils connus", r, 201, "")
	path := fmt.Sprintf("/api/projects/%d", int64(r.body["id"].(float64)))
	expectStatus(t, "patch profil sans clé", c.do("PATCH", path, map[string]any{"agent_profiles": map[string]string{"reviewer": "inconnu"}}, true), 422, "unknown_agent_profile")
	expectStatus(t, "patch sans agent_profiles", c.do("PATCH", path, map[string]any{"name": "FZ2"}, true), 200, "")
}

func TestProjectTelegramChannel(t *testing.T) {
	type sent struct {
		to   notify.Destination
		text string
	}
	var got []sent
	fail := false
	c := setupWith(t, nil, func(s *httpapi.Server) {
		s.TelegramSend = func(_ context.Context, to notify.Destination, text string) error {
			if fail {
				return notify.ErrNotConfigured
			}
			got = append(got, sent{to, text})
			return nil
		}
	})
	c.login()
	base := map[string]any{"slug": "fz", "name": "FraisZen", "repository": "o/fz"}
	with := func(chat any, thread any) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m["telegram_chat_id"], m["telegram_thread_id"] = chat, thread
		return m
	}
	expectStatus(t, "chat invalide", c.do("POST", "/api/projects", with("@groupe", 0), true), 422, "invalid_telegram_chat")
	expectStatus(t, "sujet sans groupe", c.do("POST", "/api/projects", with("", 13), true), 422, "invalid_telegram_thread")
	expectStatus(t, "sujet négatif", c.do("POST", "/api/projects", with("-100", -1), true), 422, "invalid_telegram_thread")
	r := c.do("POST", "/api/projects", with(" -1004205411589 ", 13), true)
	expectStatus(t, "création", r, 201, "")
	if r.body["telegram_chat_id"] != "-1004205411589" || r.body["telegram_thread_id"] != float64(13) {
		t.Fatalf("canal : %s", r.raw)
	}
	path := fmt.Sprintf("/api/projects/%d", int64(r.body["id"].(float64)))
	expectStatus(t, "test sans CSRF", c.do("POST", path+"/telegram-test", map[string]any{}, false), 403, "csrf_invalid")
	expectStatus(t, "test", c.do("POST", path+"/telegram-test", map[string]any{}, true), 200, "")
	if len(got) != 1 || got[0].to.ChatID != "-1004205411589" || got[0].to.ThreadID != 13 || !strings.Contains(got[0].text, "FraisZen") {
		t.Fatalf("message de test : %+v", got)
	}
	r = c.do("PATCH", path, map[string]any{"name": "FZ"}, true)
	if r.status != 200 || r.body["telegram_thread_id"] != float64(13) {
		t.Fatalf("PATCH sans canal doit le conserver : %s", r.raw)
	}
	r = c.do("PATCH", path, map[string]any{"telegram_chat_id": "", "telegram_thread_id": 0}, true)
	if r.status != 200 || r.body["telegram_chat_id"] != "" {
		t.Fatalf("PATCH retire le canal : %s", r.raw)
	}
	fail = true
	expectStatus(t, "Telegram non configuré", c.do("POST", path+"/telegram-test", map[string]any{}, true), 422, "telegram_not_configured")
}

// fakeCatalog simule le catalogue Hermes (ou son indisponibilité).
type fakeCatalog struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (f *fakeCatalog) ModelOptions(_ context.Context, profile string) (hermes.ModelOptions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return hermes.ModelOptions{}, f.err
	}
	if profile != "aos-po" {
		return hermes.ModelOptions{}, &hermes.Error{Status: 401, Class: domain.ErrClassPolicy, Msg: "profil inattendu " + profile}
	}
	return hermes.ModelOptions{Model: "gpt-6-sol", Provider: "openai-codex", Providers: []hermes.ModelProvider{
		{Slug: "openai-codex", Name: "OpenAI Codex", IsCurrent: true, Authenticated: true, Models: []string{"gpt-6-sol", "gpt-5.6-luna-900k"}},
		{Slug: "anthropic", Name: "Anthropic", Authenticated: true, Models: []string{"claude-sonnet-5"}},
		{Slug: "ollama", Name: "Ollama", Authenticated: false, Models: []string{}},
	}}, nil
}

func TestHermesModelsCatalogFiltersAndLeaksNothing(t *testing.T) {
	cat := &fakeCatalog{}
	c := setupWith(t, nil, func(s *httpapi.Server) { s.Models = cat })
	expectStatus(t, "non authentifié", c.do("GET", "/api/hermes/models", nil, false), 401, "")
	c.login()
	r := c.do("GET", "/api/hermes/models", nil, false)
	expectStatus(t, "catalogue", r, 200, "")
	items, _ := r.body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("seuls les fournisseurs authentifiés : %s", r.raw)
	}
	first, _ := items[0].(map[string]any)
	if first["provider"] != "openai-codex" || first["name"] != "OpenAI Codex" || first["is_current"] != true {
		t.Fatalf("premier fournisseur : %v", first)
	}
	models, _ := first["models"].([]any)
	if len(models) != 2 || models[0] != "gpt-5.6-luna-900k" {
		t.Fatalf("modèles triés : %v", models)
	}
	for _, k := range []string{"key", "secret", "token", "api_key", "warning", "authenticated", "ollama"} {
		if strings.Contains(strings.ToLower(r.raw), k) {
			t.Fatalf("champ %q exposé : %s", k, r.raw)
		}
	}
	// Chaque entrée n'expose que les quatre champs attendus.
	for _, it := range items {
		m, _ := it.(map[string]any)
		if len(m) != 4 {
			t.Fatalf("champs inattendus : %v", m)
		}
	}
	c.do("GET", "/api/hermes/models", nil, false)
	if cat.calls != 1 {
		t.Fatalf("le catalogue doit être mis en cache : %d appels", cat.calls)
	}
}

func TestProjectAgentModelsValidatedAgainstCatalog(t *testing.T) {
	cat := &fakeCatalog{}
	c := setupWith(t, nil, func(s *httpapi.Server) { s.Models = cat })
	c.login()
	base := func(am any) map[string]any {
		return map[string]any{"slug": "mdl", "name": "Modèles", "repository": "o/mdl", "agent_models": am}
	}
	choice := func(p, m string) map[string]string { return map[string]string{"provider": p, "model": m} }
	expectStatus(t, "rôle inconnu", c.do("POST", "/api/projects", base(map[string]any{"admin": choice("anthropic", "claude-sonnet-5")}), true), 422, "invalid_agent_models")
	expectStatus(t, "fournisseur manquant", c.do("POST", "/api/projects", base(map[string]any{"developer": choice("", "claude-sonnet-5")}), true), 422, "invalid_agent_models")
	expectStatus(t, "fournisseur invalide", c.do("POST", "/api/projects", base(map[string]any{"developer": choice("a b", "claude-sonnet-5")}), true), 422, "invalid_agent_models")
	expectStatus(t, "modèle invalide", c.do("POST", "/api/projects", base(map[string]any{"developer": choice("anthropic", "a b")}), true), 422, "invalid_agent_models")
	expectStatus(t, "fournisseur non authentifié", c.do("POST", "/api/projects", base(map[string]any{"developer": choice("ollama", "llama")}), true), 422, "invalid_agent_models")
	r := c.do("POST", "/api/projects", base(map[string]any{"developer": choice("anthropic", "claude-zzz")}), true)
	expectStatus(t, "modèle hors liste", r, 422, "invalid_agent_models")
	if !strings.Contains(r.raw, "anthropic/claude-zzz") || !strings.Contains(r.raw, "developer") || !strings.Contains(r.raw, "openai-codex") {
		t.Fatalf("message : %s", r.raw)
	}
	r = c.do("POST", "/api/projects", base(map[string]any{"developer": choice(" anthropic ", " claude-sonnet-5 "), "qa": choice("", "")}), true)
	expectStatus(t, "modèle du catalogue (entrée vide ignorée)", r, 201, "")
	am, _ := r.body["agent_models"].(map[string]any)
	dev, _ := am["developer"].(map[string]any)
	if len(am) != 1 || dev["provider"] != "anthropic" || dev["model"] != "claude-sonnet-5" {
		t.Fatalf("agent_models créés : %s", r.raw)
	}
	path := fmt.Sprintf("/api/projects/%d", int64(r.body["id"].(float64)))
	r = c.do("PATCH", path, map[string]any{"name": "Modèles 2"}, true)
	if am, _ := r.body["agent_models"].(map[string]any); r.status != 200 || len(am) != 1 {
		t.Fatalf("patch sans agent_models doit les conserver : %s", r.raw)
	}
	expectStatus(t, "patch hors liste", c.do("PATCH", path, map[string]any{"agent_models": map[string]any{"reviewer": choice("openai-codex", "gpt-4")}}, true), 422, "invalid_agent_models")
	r = c.do("PATCH", path, map[string]any{"agent_models": map[string]any{"reviewer": choice("openai-codex", "gpt-6-sol")}}, true)
	if am, _ := r.body["agent_models"].(map[string]any); r.status != 200 || len(am) != 1 || am["reviewer"] == nil {
		t.Fatalf("patch remplace l'ensemble : %s", r.raw)
	}
	r = c.do("PATCH", path, map[string]any{"agent_models": map[string]any{}}, true)
	if am, _ := r.body["agent_models"].(map[string]any); r.status != 200 || len(am) != 0 {
		t.Fatalf("patch {} remet le défaut : %s", r.raw)
	}
	// Hermes injoignable (cache expiré simulé par un nouveau serveur) : refus explicite, pas d'acceptation aveugle.
	down := setupWith(t, nil, func(s *httpapi.Server) {
		s.Models = &fakeCatalog{err: &hermes.Error{Class: domain.ErrClassProvider, Msg: "Hermes injoignable"}}
	})
	down.login()
	expectStatus(t, "catalogue indisponible", down.do("GET", "/api/hermes/models", nil, false), 503, "hermes_unreachable")
	expectStatus(t, "création refusée", down.do("POST", "/api/projects", base(map[string]any{"developer": choice("anthropic", "claude-sonnet-5")}), true), 503, "hermes_unreachable")
	r = down.do("POST", "/api/projects", base(map[string]any{}), true)
	expectStatus(t, "sans modèle : accepté", r, 201, "")
	path = fmt.Sprintf("/api/projects/%d", int64(r.body["id"].(float64)))
	expectStatus(t, "patch refusé", down.do("PATCH", path, map[string]any{"agent_models": map[string]any{"qa": choice("anthropic", "claude-sonnet-5")}}, true), 503, "hermes_unreachable")
	expectStatus(t, "patch sans modèle accepté", down.do("PATCH", path, map[string]any{"name": "ok"}, true), 200, "")
	// Catalogue non configuré (nil) : même refus.
	none := setup(t, nil)
	none.login()
	expectStatus(t, "non configuré", none.do("POST", "/api/projects", base(map[string]any{"developer": choice("anthropic", "claude-sonnet-5")}), true), 503, "hermes_unreachable")
}

func TestRunViewExposesRequestedModelAndFallback(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx := context.Background()
	job, _, err := store.EnqueueJob(ctx, c.s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "fb"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := store.CreateRun(ctx, c.s.Pool, job.ID, "developer", "aos-developer")
	_ = store.SetRunRequestedModel(ctx, c.s.Pool, run.ID, domain.ModelChoice{Provider: "anthropic", Model: "claude-sonnet-5"})
	_ = store.SetRunHermesID(ctx, c.s.Pool, run.ID, "run_a")
	_ = store.MarkRunModelFallback(ctx, c.s.Pool, run.ID, "run_b")
	if err := store.FinishRun(ctx, c.s.Pool, run.ID, store.RunResult{Status: "completed", Model: "gpt-6-sol", Provider: "openai-codex"}); err != nil {
		t.Fatal(err)
	}
	r := c.do("GET", fmt.Sprintf("/api/runs/%d", run.ID), nil, false)
	expectStatus(t, "run", r, 200, "")
	rv, _ := r.body["run"].(map[string]any)
	if rv["requested_model"] != "claude-sonnet-5" || rv["requested_provider"] != "anthropic" || rv["model_fallback"] != true ||
		rv["model"] != "gpt-6-sol" || rv["provider"] != "openai-codex" {
		t.Fatalf("vue du run : %s", r.raw)
	}
	l := c.do("GET", "/api/runs", nil, false)
	if !strings.Contains(l.raw, `"model_fallback":true`) || !strings.Contains(l.raw, `"requested_provider":"anthropic"`) {
		t.Fatalf("liste des runs : %s", l.raw)
	}
}

func TestPipelineTwoBoardsWithFeatureProgress(t *testing.T) {
	c := setup(t, nil)
	c.login()
	ctx := context.Background()
	p, _ := store.CreateProject(ctx, c.s.Pool, store.ProjectInput{Slug: "fz", Name: "FraisZen", Repository: "o/fz", BaseBranch: "develop", Mode: "product"})
	f, _ := store.CreateFeature(ctx, c.s.Pool, p.ID, "Alerte frais réels", "")
	_, _ = c.s.Pool.Exec(ctx, `UPDATE features SET state='IN_DEV' WHERE id=$1`, f.ID)
	states := map[int]string{1: "DONE", 2: "REVIEW", 3: "PLANNED", 4: "CANCELLED"}
	for seq := 1; seq <= 4; seq++ {
		tk, err := store.InsertTicket(ctx, c.s.Pool, store.TicketInput{FeatureID: f.ID, ProjectID: p.ID, Key: fmt.Sprintf("fz-F%d-T%d", f.ID, seq), Seq: seq, Title: "T"})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.s.Pool.Exec(ctx, `UPDATE tickets SET state=$2 WHERE id=$1`, tk.ID, states[seq])
	}
	r := c.do("GET", "/api/pipeline", nil, false)
	if r.status != 200 {
		t.Fatalf("pipeline : %s", r.raw)
	}
	col := func(board, state string) []any {
		for _, c := range r.body[board].(map[string]any)["columns"].([]any) {
			cm := c.(map[string]any)
			if cm["state"] == state {
				return cm["items"].([]any)
			}
		}
		t.Fatalf("colonne %s/%s absente", board, state)
		return nil
	}
	inDev := col("features", "IN_DEV")
	if len(inDev) != 1 {
		t.Fatalf("feature en cours : %v", inDev)
	}
	prog := inDev[0].(map[string]any)["progress"].(map[string]any)
	cur := prog["current"].(map[string]any)
	if prog["total"] != float64(3) || prog["done"] != float64(1) || cur["state"] != "REVIEW" || cur["key"] != fmt.Sprintf("fz-F%d-T2", f.ID) {
		t.Fatalf("avancement (annulé exclu, planifié compté, ticket courant) : %v", prog)
	}
	rev := col("tickets", "REVIEW")
	if len(rev) != 1 || rev[0].(map[string]any)["feature_title"] != "Alerte frais réels" || rev[0].(map[string]any)["feature_id"] != float64(f.ID) {
		t.Fatalf("ticket en revue : %v", rev)
	}
	for _, c := range r.body["features"].(map[string]any)["columns"].([]any) {
		if s := c.(map[string]any)["state"]; s == "DEV" || s == "REVIEW" {
			t.Fatalf("colonne de ticket %v dans le tableau des features", s)
		}
	}
	for _, c := range r.body["tickets"].(map[string]any)["columns"].([]any) {
		if s := c.(map[string]any)["state"]; s == "IN_DEV" || s == "PLANNED" || s == "SPEC" {
			t.Fatalf("colonne %v inattendue dans le tableau des tickets", s)
		}
	}
	if !strings.Contains(r.raw, `"projects":[{"id":`) || !strings.Contains(r.raw, `"slug":"fz"`) {
		t.Fatalf("projets : %s", r.raw)
	}
}

func TestIntegrationModeOnProjectFeatureAndSwitch(t *testing.T) {
	c := setup(t, nil)
	c.login()
	expectStatus(t, "mode projet invalide", c.do("POST", "/api/projects", map[string]any{"slug": "fz", "name": "FZ", "repository": "o/fz",
		"integration_mode": "branch"}, true), 422, "invalid_integration_mode")
	r := c.do("POST", "/api/projects", map[string]any{"slug": "fz", "name": "FZ", "repository": "o/fz", "base_branch": "develop",
		"integration_mode": "feature_branch"}, true)
	expectStatus(t, "création", r, 201, "")
	if r.body["integration_mode"] != "feature_branch" {
		t.Fatalf("projet : %s", r.raw)
	}
	pid := itoa(r.body["id"])
	r = c.do("PATCH", "/api/projects/"+pid, map[string]any{"integration_mode": "direct"}, true)
	if r.status != 200 || r.body["integration_mode"] != "direct" {
		t.Fatalf("patch : %s", r.raw)
	}
	r = c.do("PATCH", "/api/projects/"+pid, map[string]any{"integration_mode": "feature_branch"}, true)
	expectStatus(t, "retour", r, 200, "")

	// Feature : défaut du projet, surcharge, mode invalide refusé.
	r = c.do("POST", "/api/features", map[string]any{"project_id": r.body["id"], "title": "Par défaut", "description": "d"}, true)
	if r.status != 201 || r.body["integration_mode"] != "feature_branch" || r.body["feature_branch"] != nil {
		t.Fatalf("feature par défaut : %s", r.raw)
	}
	fid := itoa(r.body["id"])
	r = c.do("POST", "/api/features", map[string]any{"project_id": r.body["project_id"], "title": "Directe", "description": "d", "integration_mode": "direct"}, true)
	if r.status != 201 || r.body["integration_mode"] != "direct" {
		t.Fatalf("surcharge : %s", r.raw)
	}
	expectStatus(t, "mode feature invalide", c.do("POST", "/api/features", map[string]any{"project_id": r.body["project_id"], "title": "X",
		"description": "d", "integration_mode": "x"}, true), 422, "invalid_integration_mode")

	// Bascule avant le feu vert : simple réglage.
	r = c.do("POST", "/api/features/"+fid+"/integration", map[string]any{"mode": "direct"}, true)
	if r.status != 200 || r.body["integration_mode"] != "direct" {
		t.Fatalf("bascule : %s", r.raw)
	}
	expectStatus(t, "bascule invalide", c.do("POST", "/api/features/"+fid+"/integration", map[string]any{"mode": "?"}, true), 422, "invalid_integration_mode")
	expectStatus(t, "sans CSRF", c.do("POST", "/api/features/"+fid+"/integration", map[string]any{"mode": "direct"}, false), 403, "")
	// Feature terminée : verrouillée.
	if _, err := c.s.Pool.Exec(context.Background(), `UPDATE features SET state='DONE' WHERE id=$1`, fid); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, "terminée", c.do("POST", "/api/features/"+fid+"/integration", map[string]any{"mode": "feature_branch"}, true), 409, "integration_locked")
	// Le pipeline connaît la colonne de recette.
	r = c.do("GET", "/api/pipeline", nil, false)
	if !strings.Contains(r.raw, `"label":"Recette humaine","state":"ACCEPTANCE"`) {
		t.Fatalf("pipeline : colonne de recette absente")
	}
}

// profileCatalog : catalogue Hermes dont le modèle courant dépend du profil (GET /p/<profil>/api/model/options).
type profileCatalog struct {
	mu      sync.Mutex
	current map[string][2]string
	calls   map[string]int
}

func (f *profileCatalog) ModelOptions(_ context.Context, profile string) (hermes.ModelOptions, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[profile]++
	cur, ok := f.current[profile]
	if !ok {
		return hermes.ModelOptions{}, &hermes.Error{Status: 404, Class: domain.ErrClassProvider, Msg: "profil inconnu " + profile}
	}
	return hermes.ModelOptions{Provider: cur[0], Model: cur[1], Providers: []hermes.ModelProvider{
		{Slug: "openai-codex", Name: "OpenAI Codex", Authenticated: true, Models: []string{"gpt-5.6-luna-900k"}},
		{Slug: "anthropic", Name: "Anthropic", Authenticated: true, Models: []string{"claude-opus-5-5"}},
	}}, nil
}

func TestProfileModelsListRequestAndMismatch(t *testing.T) {
	cat := &profileCatalog{calls: map[string]int{}, current: map[string][2]string{
		"aos-po": {"openai-codex", "gpt-5.6-luna-900k"}, "aos-developer": {"openai-codex", "gpt-5.6-luna-900k"},
		"aos-fz-developer": {"openai-codex", "gpt-5.6-luna-900k"}, "aos-fz-qa": {"anthropic", "claude-opus-5-5"},
	}}
	profiles := []string{"aos-po", "aos-developer", "aos-fz-developer", "aos-fz-qa", "aos-cassé", "dev-developer", "default"}
	c := setupWith(t, nil, func(s *httpapi.Server) {
		s.Models = cat
		s.HermesProfiles = func() ([]string, error) { return profiles, nil }
	})
	expectStatus(t, "non authentifié", c.do("GET", "/api/profile-models", nil, false), 401, "")
	c.login()
	// Projet : développeur et QA sur des profils propres, tous deux demandés en Claude.
	choice := map[string]string{"provider": "anthropic", "model": "claude-opus-5-5"}
	r := c.do("POST", "/api/projects", map[string]any{"slug": "fz", "name": "FZ", "repository": "o/fz",
		"agent_profiles": map[string]string{"developer": "aos-fz-developer", "qa": "aos-fz-qa"},
		"agent_models":   map[string]any{"developer": choice, "qa": choice, "po": choice}}, true)
	expectStatus(t, "projet", r, 201, "")
	expectStatus(t, "activation", c.do("PATCH", "/api/projects/"+itoa(r.body["id"]), map[string]any{"enabled": true}, true), 200, "")

	r = c.do("GET", "/api/profile-models", nil, false)
	expectStatus(t, "liste", r, 200, "")
	items, _ := r.body["items"].([]any)
	names := []string{}
	for _, it := range items {
		names = append(names, it.(map[string]any)["profile"].(string))
	}
	if strings.Join(names, ",") != "aos-developer,aos-fz-developer,aos-fz-qa,aos-po" {
		t.Fatalf("seuls les profils aos-* valides : %v", names)
	}
	dev := items[1].(map[string]any)
	if dev["role"] != "developer" || dev["state"] != "" || dev["desired"] != nil {
		t.Fatalf("profil sans demande : %v", dev)
	}
	if cur := dev["current"].(map[string]any); cur["provider"] != "openai-codex" {
		t.Fatalf("modèle courant : %v", cur)
	}
	if u := dev["usages"].([]any); len(u) != 1 || u[0].(map[string]any)["project"] != "fz" {
		t.Fatalf("usages : %v", u)
	}
	// Écarts : développeur fz (profil Codex, Claude demandé) et PO (profil par défaut aos-po). QA aligné.
	mm, _ := r.body["mismatches"].([]any)
	got := []string{}
	for _, m := range mm {
		x := m.(map[string]any)
		got = append(got, x["project"].(string)+"/"+x["role"].(string)+"/"+x["profile"].(string))
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "fz/developer/aos-fz-developer,fz/po/aos-po" {
		t.Fatalf("écarts : %v", got)
	}
	if sy := r.body["sync"].(map[string]any); sy["stale"] != true || sy["last_seen_at"] != nil {
		t.Fatalf("script jamais vu : %v", sy)
	}
	// Cache : une seconde lecture n'interroge pas Hermes.
	c.do("GET", "/api/profile-models", nil, false)
	if cat.calls["aos-fz-qa"] != 1 {
		t.Fatalf("cache des modèles courants : %v", cat.calls)
	}

	// Demandes : refus (hors aos-*, inconnu, hors catalogue, sans CSRF), puis acceptation.
	put := func(p, prov, model string, csrf bool) resp {
		return c.do("PUT", "/api/profile-models/"+p, map[string]string{"provider": prov, "model": model}, csrf)
	}
	expectStatus(t, "dev-* refusé", put("dev-developer", "anthropic", "claude-opus-5-5", true), 422, "invalid_profile")
	expectStatus(t, "inconnu", put("aos-absent", "anthropic", "claude-opus-5-5", true), 404, "unknown_profile")
	expectStatus(t, "hors catalogue", put("aos-fz-developer", "anthropic", "claude-x", true), 422, "invalid_agent_models")
	expectStatus(t, "nom invalide", put("aos-fz-developer", "anthropic", "a b", true), 422, "invalid_model")
	expectStatus(t, "sans CSRF", put("aos-fz-developer", "anthropic", "claude-opus-5-5", false), 403, "")
	r = put("aos-fz-developer", "anthropic", "claude-opus-5-5", true)
	if r.status != 200 || r.body["state"] != "PENDING" || r.body["revision"] != float64(1) {
		t.Fatalf("demande : %s", r.raw)
	}
	r = put("aos-fz-developer", "anthropic", "claude-opus-5-5", true)
	if r.body["revision"] != float64(2) {
		t.Fatalf("nouvelle révision : %s", r.raw)
	}
	// Le script applique : état APPLIED, Hermes relit le nouveau modèle → aligné, plus d'écart.
	if _, err := c.s.Pool.Exec(context.Background(), `UPDATE profile_models SET state='APPLIED', applied_at=now() WHERE profile='aos-fz-developer';
		INSERT INTO profile_sync_status(last_seen_at, host) VALUES (now(), 'srv')`); err != nil {
		t.Fatal(err)
	}
	cat.mu.Lock()
	cat.current["aos-fz-developer"] = [2]string{"anthropic", "claude-opus-5-5"}
	cat.mu.Unlock()
	r = c.do("GET", "/api/profile-models", nil, false)
	for _, it := range r.body["items"].([]any) {
		x := it.(map[string]any)
		if x["profile"] == "aos-fz-developer" && (x["state"] != "APPLIED" || x["matches_desired"] != true) {
			t.Fatalf("appliqué : %v", x)
		}
	}
	if mm, _ := r.body["mismatches"].([]any); len(mm) != 1 {
		t.Fatalf("écart restant (PO) : %v", mm)
	}
	if sy := r.body["sync"].(map[string]any); sy["stale"] != false || sy["host"] != "srv" {
		t.Fatalf("script vu : %v", sy)
	}
	var n int
	if err := c.s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action='profile_model.requested'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("audit : %d %v", n, err)
	}
}

func TestQARecordingsListAndFileServing(t *testing.T) {
	root := t.TempDir()
	c := setupWith(t, nil, func(s *httpapi.Server) { s.Recordings = root })
	ctx := context.Background()
	c.login()
	r := c.do("POST", "/api/projects", map[string]any{"slug": "fz", "name": "FZ", "repository": "o/fz", "qa_browser": true}, true)
	expectStatus(t, "projet", r, 201, "")
	if r.body["qa_browser"] != true {
		t.Fatalf("qa_browser : %s", r.raw)
	}
	pid := int64(r.body["id"].(float64))
	r = c.do("PATCH", fmt.Sprintf("/api/projects/%d", pid), map[string]any{"qa_browser": false}, true)
	if r.status != 200 || r.body["qa_browser"] != false {
		t.Fatalf("patch qa_browser : %s", r.raw)
	}
	f, err := store.CreateFeature(ctx, c.s.Pool, pid, "F", "d")
	if err != nil {
		t.Fatal(err)
	}
	var tid, jid, rid int64
	if err := c.s.Pool.QueryRow(ctx, `INSERT INTO tickets (feature_id, project_id, key, seq, title, body, acceptance, state)
		VALUES ($1,$2,'fz-F1-T1',1,'T','b','["c1","c2"]','QA') RETURNING id`, f.ID, pid).Scan(&tid); err != nil {
		t.Fatal(err)
	}
	if err := c.s.Pool.QueryRow(ctx, `INSERT INTO jobs (kind, subject_type, subject_id, idempotency_key) VALUES ('qa','ticket',$1,'k-qa') RETURNING id`, tid).Scan(&jid); err != nil {
		t.Fatal(err)
	}
	if err := c.s.Pool.QueryRow(ctx, `INSERT INTO runs (job_id, agent_role, hermes_profile, status) VALUES ($1,'qa','aos-qa','completed') RETURNING id`, jid).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "store", fmt.Sprint(rid))
	files := map[string][]byte{
		"video.webm": {0x1A, 0x45, 0xDF, 0xA3, 7}, "trace.zip": {'P', 'K', 3, 4},
		"shots/01-recap.png": {0x89, 'P', 'N', 'G'}, "report.json": []byte(`{"ok":false,"error":"timeout","console_errors":["TypeError: x"],"failed_requests":["500 GET /api/summary"]}`),
	}
	for name, b := range files {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	two := 2
	if err := store.InsertRecordings(ctx, c.s.Pool, rid, tid, []store.NewRecording{
		{Kind: "video", Name: "video.webm", Size: 5, SHA256: strings.Repeat("a", 64)},
		{Kind: "trace", Name: "trace.zip", Size: 4, SHA256: strings.Repeat("b", 64)},
		{Kind: "screenshot", Name: "shots/01-recap.png", Label: "Récapitulatif", Criterion: &two, Size: 4, SHA256: strings.Repeat("c", 64)},
		{Kind: "report", Name: "report.json", Size: 10, SHA256: strings.Repeat("d", 64)},
	}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	expectStatus(t, "ticket inconnu", c.do("GET", "/api/tickets/999999/recordings", nil, false), 404, "")
	r = c.do("GET", fmt.Sprintf("/api/tickets/%d/recordings", tid), nil, false)
	expectStatus(t, "liste", r, 200, "")
	items := r.body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("sessions : %s", r.raw)
	}
	run := items[0].(map[string]any)
	rep := run["report"].(map[string]any)
	shots := run["shots"].([]any)
	if run["run_id"] != float64(rid) || rep["ok"] != false || rep["error"] != "timeout" || len(shots) != 1 ||
		shots[0].(map[string]any)["criterion"] != float64(2) || run["video"] == nil || run["trace"] == nil {
		t.Fatalf("session : %s", r.raw)
	}
	vid := int64(run["video"].(map[string]any)["id"].(float64))
	trace := int64(run["trace"].(map[string]any)["id"].(float64))

	get := func(id int64) *http.Response {
		req, _ := http.NewRequest("GET", c.srv.URL+fmt.Sprintf("/api/recordings/%d/file", id), nil)
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	resp := get(vid)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/webm" || !bytes.Equal(body, files["video.webm"]) ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "inline") || resp.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("vidéo : %d %v", resp.StatusCode, resp.Header)
	}
	// Lecture partielle (lecteur vidéo).
	req, _ := http.NewRequest("GET", c.srv.URL+fmt.Sprintf("/api/recordings/%d/file", vid), nil)
	req.Header.Set("Range", "bytes=0-1")
	pr, err := c.http.Do(req)
	if err != nil || pr.StatusCode != 206 {
		t.Fatalf("Range : %v %v", pr, err)
	}
	pr.Body.Close()
	if resp := get(trace); resp.Header.Get("Content-Type") != "application/zip" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("trace : %v", resp.Header)
	}
	// Expiré ou fichier disparu : 404 ; non authentifié : 401.
	if _, err := c.s.Pool.Exec(ctx, `UPDATE qa_recordings SET expires_at = now() - interval '1 second' WHERE id=$1`, trace); err != nil {
		t.Fatal(err)
	}
	if resp := get(trace); resp.StatusCode != 404 {
		t.Fatalf("expiré : %d", resp.StatusCode)
	}
	_ = os.Remove(filepath.Join(dir, "video.webm"))
	if resp := get(vid); resp.StatusCode != 404 {
		t.Fatalf("fichier disparu : %d", resp.StatusCode)
	}
	anon, _ := http.Get(c.srv.URL + fmt.Sprintf("/api/recordings/%d/file", vid))
	if anon.StatusCode != 401 {
		t.Fatalf("anonyme : %d", anon.StatusCode)
	}
	anon.Body.Close()
}

func TestTraceViewerHeaders(t *testing.T) {
	web := fstest.MapFS{"index.html": {Data: []byte("<html>app</html>")}, "trace-viewer/index.html": {Data: []byte("<html>trace</html>")}}
	c := setupWith(t, web, nil)
	resp, err := http.Get(c.srv.URL + "/trace-viewer/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "worker-src 'self'") {
		t.Fatalf("visualiseur : %v", resp.Header)
	}
	resp, _ = http.Get(c.srv.URL + "/features")
	resp.Body.Close()
	if resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatalf("reste de l'interface : %v", resp.Header)
	}
}

func TestCorrectionEndpointValidationAndState(t *testing.T) {
	c := setup(t, nil)
	c.login()
	r := c.do("POST", "/api/projects", map[string]any{"slug": "fz", "name": "FZ", "repository": "o/fz", "integration_mode": "feature_branch"}, true)
	expectStatus(t, "projet", r, 201, "")
	r = c.do("POST", "/api/features", map[string]any{"project_id": r.body["id"], "title": "Récap", "description": "d"}, true)
	expectStatus(t, "feature", r, 201, "")
	fid := itoa(r.body["id"])
	url := "/api/features/" + fid + "/corrections"
	body := map[string]any{"title": "Forfait absent", "description": "Le bloc ne s'affiche pas.", "acceptance_criteria": []string{"Le bloc s'affiche"}}
	expectStatus(t, "hors recette", c.do("POST", url, body, true), 409, "correction_not_allowed")
	expectStatus(t, "sans CSRF", c.do("POST", url, body, false), 403, "")
	ctx := context.Background()
	if _, err := c.s.Pool.Exec(ctx, `UPDATE features SET state='ACCEPTANCE', feature_branch='feature/F1-recap' WHERE id=$1`, fid); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, "description vide", c.do("POST", url, map[string]any{"title": "x", "description": " "}, true), 422, "invalid_correction")
	expectStatus(t, "trop de critères", c.do("POST", url, map[string]any{"title": "x", "description": "y", "acceptance_criteria": make([]string, 11)}, true), 422, "invalid_correction")
	r = c.do("POST", url, body, true)
	expectStatus(t, "correction", r, 201, "")
	if r.body["origin"] != "correction" || !strings.HasSuffix(r.body["key"].(string), "-F"+fid+"-T1") || r.body["state"] != "PLANNED" {
		t.Fatalf("ticket : %s", r.raw)
	}
	r = c.do("GET", "/api/features/"+fid, nil, false)
	if r.body["feature"].(map[string]any)["state"] != "IN_DEV" || !strings.Contains(r.raw, "feature.correction_ticket_added") {
		t.Fatalf("feature : %s", r.raw)
	}
}
