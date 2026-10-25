package httpapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/engine"
	gh "github.com/zeggriim2/agentic-os/internal/github"
	"github.com/zeggriim2/agentic-os/internal/httpapi"
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
	expectStatus(t, "projet absent", c.do("POST", "/api/features", map[string]any{"project_id": 999, "title": "T"}, true), 422, "invalid_project")
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
	if pl.status != 200 || len(pl.body["columns"].([]any)) == 0 {
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
	if r := c.do("POST", "/api/features", map[string]any{"project_id": p.ID, "title": "Nouvelle"}, true); r.status != 201 {
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
