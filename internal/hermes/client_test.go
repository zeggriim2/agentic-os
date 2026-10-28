package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

type keys map[string]string

func (k keys) HermesKey(p string) (string, error) {
	if v, ok := k[p]; ok {
		return v, nil
	}
	return "", errors.New("absent")
}

func newTest(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, keys{"aos-dev": "secret-dev"})
}

func TestCreateRunStartedAndReplayed(t *testing.T) {
	seen := map[string]string{}
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/p/aos-dev/v1/runs" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret-dev" {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req RunRequest
		if err := json.Unmarshal(body, &req); err != nil || req.Input == "" {
			w.WriteHeader(400)
			return
		}
		key := r.Header.Get("Idempotency-Key")
		prev, replay := seen[key]
		if replay && prev != string(body) {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":{"code":"idempotency_key_conflict"}}`))
			return
		}
		seen[key] = string(body)
		w.WriteHeader(202)
		fmt.Fprintf(w, `{"run_id":"run_1","status":"started","replayed":%v}`, replay)
	})
	ctx := context.Background()
	res, err := c.CreateRun(ctx, "aos-dev", "aos-job-1-a0", RunRequest{Input: "x", Instructions: "y"})
	if err != nil || res.RunID != "run_1" || res.Replayed || res.Status != "started" {
		t.Fatalf("création: %+v %v", res, err)
	}
	res, err = c.CreateRun(ctx, "aos-dev", "aos-job-1-a0", RunRequest{Input: "x", Instructions: "y"})
	if err != nil || !res.Replayed {
		t.Fatalf("rejeu: %+v %v", res, err)
	}
	_, err = c.CreateRun(ctx, "aos-dev", "aos-job-1-a0", RunRequest{Input: "autre", Instructions: "y"})
	if !errors.Is(err, ErrConflict) || ClassOf(err) != domain.ErrClassInternal {
		t.Fatalf("409 attendu classé internal: %v", err)
	}
	if strings.Contains(err.Error(), "secret-dev") {
		t.Fatal("clé dans l'erreur")
	}
	if _, err := c.CreateRun(ctx, "inconnu", "k", RunRequest{Input: "x"}); err == nil {
		t.Fatal("profil sans clé accepté")
	}
	if _, err := c.CreateRun(ctx, "aos-dev", "", RunRequest{Input: "x"}); err == nil {
		t.Fatal("clé d'idempotence vide acceptée")
	}
}

func TestGetRunStatuses(t *testing.T) {
	bodies := map[string]string{
		"done":    `{"status":"completed","output":"ok","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3},"runtime":{"provider":"p","model":"m"},"completed":true,"partial":false,"interrupted":false}`,
		"failed":  `{"status":"failed","output":"","completed":false}`,
		"partial": `{"status":"completed","output":"x","completed":true,"partial":true,"interrupted":false}`,
		"intr":    `{"status":"completed","output":"x","completed":true,"partial":false,"interrupted":true}`,
		"running": `{"status":"running"}`,
	}
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/p/aos-dev/v1/runs/")
		b, ok := bodies[id]
		if !ok {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"run_not_found"}`))
			return
		}
		_, _ = w.Write([]byte(b))
	})
	ctx := context.Background()
	st, err := c.GetRun(ctx, "aos-dev", "done")
	if err != nil || st.NormalizedStatus() != domain.RunCompleted || st.Usage.TotalTokens != 3 || st.Runtime.Model != "m" || !st.Terminal() {
		t.Fatalf("completed: %+v %v", st, err)
	}
	if domain.RunOutcomeAcceptable(st.NormalizedStatus(), st.Partial, st.Interrupted) != nil {
		t.Fatal("run complet refusé")
	}
	st, _ = c.GetRun(ctx, "aos-dev", "failed")
	if st.NormalizedStatus() != domain.RunFailed {
		t.Fatal("failed mal mappé")
	}
	for _, id := range []string{"partial", "intr"} {
		st, _ = c.GetRun(ctx, "aos-dev", id)
		if domain.RunOutcomeAcceptable(st.NormalizedStatus(), st.Partial, st.Interrupted) == nil {
			t.Fatalf("%s accepté", id)
		}
	}
	st, _ = c.GetRun(ctx, "aos-dev", "running")
	if st.Terminal() {
		t.Fatal("running terminal")
	}
	_, err = c.GetRun(ctx, "aos-dev", "zzz")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 attendu: %v", err)
	}
}

func TestStreamEventsSSE(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p/aos-dev/v1/runs/r1/events" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, ": commentaire\n\n")
		_, _ = io.WriteString(w, "data: {\"event\":\"message.delta\",\"seq\":1,\"delta\":\"he\"}\n\n")
		_, _ = io.WriteString(w, "event: x\ndata: {\"event\":\"tool.started\",\"seq\":2,\"tool\":\"terminal\"}\n\n")
		_, _ = io.WriteString(w, "data: pas du json\n\n")
		_, _ = io.WriteString(w, "data: {\"event\":\"run.completed\",\"seq\":3}\n\n")
	})
	var got []Event
	err := c.StreamEvents(context.Background(), "aos-dev", "r1", func(e Event) error {
		got = append(got, e)
		return nil
	})
	if err != nil || len(got) != 3 {
		t.Fatalf("événements: %d %v", len(got), err)
	}
	if !got[0].IsDelta() || got[1].Event != "tool.started" || got[1].Seq != 2 || got[2].Event != "run.completed" {
		t.Fatalf("contenu: %+v", got)
	}
	if !strings.Contains(string(got[1].Raw), "terminal") {
		t.Fatal("raw absent")
	}
}

func TestStopAndCapabilitiesAndModels(t *testing.T) {
	var stopped bool
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/p/aos-dev/v1/runs/r1/stop":
			stopped = r.Method == http.MethodPost
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/p/aos-dev/v1/capabilities":
			_, _ = w.Write([]byte(`{"runs":true}`))
		case "/p/aos-dev/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
		default:
			w.WriteHeader(500)
		}
	})
	ctx := context.Background()
	if err := c.StopRun(ctx, "aos-dev", "r1"); err != nil || !stopped {
		t.Fatalf("stop: %v", err)
	}
	if err := c.Capabilities(ctx, "aos-dev"); err != nil {
		t.Fatal(err)
	}
	m, err := c.Models(ctx, "aos-dev")
	if err != nil || len(m) != 2 {
		t.Fatalf("models %v %v", m, err)
	}
	if err := c.StopRun(ctx, "aos-dev", "r2"); ClassOf(err) != domain.ErrClassProvider {
		t.Fatalf("500 → provider attendu: %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	c := New("http://127.0.0.1:1", keys{"aos-dev": "k"})
	_, err := c.GetRun(context.Background(), "aos-dev", "x")
	if ClassOf(err) != domain.ErrClassProvider {
		t.Fatalf("injoignable → provider: %v", err)
	}
}

func TestCreateRunSendsModelOverrideOnlyWhenSet(t *testing.T) {
	var bodies []map[string]any
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		bodies = append(bodies, m)
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"run_id":"run_1","status":"started"}`))
	})
	ctx := context.Background()
	if _, err := c.CreateRun(ctx, "aos-dev", "k1", RunRequest{Input: "i", Instructions: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateRun(ctx, "aos-dev", "k2", RunRequest{Input: "i", Instructions: "s", Model: "claude-sonnet-5", Provider: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateRun(ctx, "aos-dev", "k3", RunRequest{Input: "i", Instructions: "s", Model: "gpt-9"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 3 {
		t.Fatalf("%d corps", len(bodies))
	}
	// Sans surcharge : les clés model/provider sont absentes (Hermes applique le défaut du profil).
	if _, ok := bodies[0]["model"]; ok {
		t.Fatalf("model ne doit pas être envoyé sans surcharge : %v", bodies[0])
	}
	if _, ok := bodies[0]["provider"]; ok {
		t.Fatalf("provider ne doit pas être envoyé sans surcharge : %v", bodies[0])
	}
	if bodies[1]["model"] != "claude-sonnet-5" || bodies[1]["provider"] != "anthropic" || bodies[1]["input"] != "i" {
		t.Fatalf("surcharge complète : %v", bodies[1])
	}
	if _, ok := bodies[2]["provider"]; ok || bodies[2]["model"] != "gpt-9" {
		t.Fatalf("modèle seul : %v", bodies[2])
	}
}

func TestGetRunReadsRequestedAndRouteSource(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"completed","output":"ok","usage":{"total_tokens":5},"completed":true,
			"runtime":{"provider":"openai-codex","model":"gpt-6-sol","route_source":"raw_request"},
			"requested":{"provider":"anthropic","model":"claude-sonnet-5"},"route_source":"raw_request"}`))
	})
	st, err := c.GetRun(context.Background(), "aos-dev", "run_1")
	if err != nil {
		t.Fatal(err)
	}
	if st.Runtime.Model != "gpt-6-sol" || st.Requested == nil || st.Requested.Provider != "anthropic" || st.RouteSource != "raw_request" {
		t.Fatalf("%+v", st)
	}
}

func TestIsProviderResolutionFailure(t *testing.T) {
	cases := []struct {
		name string
		st   RunStatus
		want bool
	}{
		{"échec auth fournisseur (fr)", RunStatus{Status: "failed", Error: "⚠️ Échec d'authentification du fournisseur: anthropic"}, true},
		{"échec provider (en)", RunStatus{Status: "failed", Error: "⚠️ Provider authentication failed: no credentials"}, true},
		{"échec immédiat sans token ni sortie", RunStatus{Status: "failed", Error: "agent run failed"}, true},
		{"échec après travail", RunStatus{Status: "failed", Error: "agent run failed", Usage: Usage{TotalTokens: 1200}}, false},
		{"échec avec sortie", RunStatus{Status: "failed", Error: "boom", Output: "partiel"}, false},
		{"terminé", RunStatus{Status: "completed", Usage: Usage{TotalTokens: 0}}, false},
		{"arrêté", RunStatus{Status: "cancelled", Error: "provider"}, false},
		{"en cours", RunStatus{Status: "running"}, false},
	}
	for _, c := range cases {
		if got := IsProviderResolutionFailure(c.st); got != c.want {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestModelOptionsCatalog(t *testing.T) {
	c := newTest(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/p/aos-dev/api/model/options" || r.Header.Get("Authorization") != "Bearer secret-dev" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"model":"gpt-6-sol","provider":"openai-codex","providers":[
			{"slug":"openai-codex","name":"OpenAI Codex","is_current":true,"authenticated":true,"total_models":2,"models":["gpt-6-sol","gpt-5.6-luna-900k"],"api_key":"sk-secret","warning":""},
			{"slug":"anthropic","name":"Anthropic","is_current":false,"authenticated":true,"models":["claude-sonnet-5"]},
			{"slug":"ollama","name":"Ollama","authenticated":false,"models":[],"warning":"non configuré"}]}`))
	})
	o, err := c.ModelOptions(context.Background(), "aos-dev")
	if err != nil {
		t.Fatal(err)
	}
	auth := o.Authenticated()
	if len(auth) != 2 || auth[0].Slug != "openai-codex" || !auth[0].IsCurrent || auth[1].Slug != "anthropic" {
		t.Fatalf("%+v", auth)
	}
	if !o.Has("anthropic", "claude-sonnet-5") || o.Has("ollama", "llama") || o.Has("anthropic", "gpt-6-sol") || o.Has("", "") {
		t.Fatal("Has incohérent")
	}
	if _, err := c.ModelOptions(context.Background(), "inconnu"); err == nil {
		t.Fatal("profil sans clé doit échouer")
	}
	down := New("http://127.0.0.1:1", keys{"aos-dev": "k"})
	if _, err := down.ModelOptions(context.Background(), "aos-dev"); err == nil || ClassOf(err) != domain.ErrClassProvider {
		t.Fatalf("injoignable : %v", err)
	}
}
