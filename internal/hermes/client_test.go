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
