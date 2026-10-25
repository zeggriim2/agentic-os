package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

type fakeGH struct {
	mu      sync.Mutex
	issues  map[int]map[string]any
	pulls   map[int]map[string]any
	posts   int
	prPosts int
	auth    []string
	checks  string
	status  string
}

func (f *fakeGH) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	if r.Header.Get("User-Agent") != "agentic-os" {
		w.WriteHeader(400)
		return
	}
	path := r.URL.Path
	switch {
	case r.Method == "GET" && path == "/search/issues":
		q := r.URL.Query().Get("q")
		items := []map[string]any{}
		for _, is := range f.issues {
			body := is["body"].(string)
			if strings.Contains(q, `"agentic-os:ticket=`) {
				key := strings.Split(strings.Split(q, `agentic-os:ticket=`)[1], `"`)[0]
				if strings.Contains(body, "agentic-os:ticket="+key+" ") {
					items = append(items, is)
				}
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	case r.Method == "POST" && path == "/repos/o/r/issues":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.posts++
		n := len(f.issues) + 1
		is := map[string]any{"number": n, "html_url": fmt.Sprintf("https://github.com/o/r/issues/%d", n), "body": in["body"], "state": "open"}
		f.issues[n] = is
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(is)
	case r.Method == "GET" && strings.HasPrefix(path, "/repos/o/r/issues/"):
		var n int
		fmt.Sscanf(strings.TrimPrefix(path, "/repos/o/r/issues/"), "%d", &n)
		if is, ok := f.issues[n]; ok {
			_ = json.NewEncoder(w).Encode(is)
			return
		}
		w.WriteHeader(404)
	case r.Method == "GET" && path == "/repos/o/r/branches/aos/k":
		_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"sha": sha}})
	case r.Method == "GET" && path == "/repos/o/r/pulls":
		out := []map[string]any{}
		for _, pr := range f.pulls {
			if r.URL.Query().Get("head") == "o:"+pr["head"].(map[string]any)["ref"].(string) {
				out = append(out, pr)
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == "POST" && path == "/repos/o/r/pulls":
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["draft"] != true {
			w.WriteHeader(422)
			return
		}
		f.prPosts++
		n := 100 + len(f.pulls)
		pr := map[string]any{"number": n, "html_url": "https://github.com/o/r/pull/x", "state": "open", "draft": true,
			"head": map[string]any{"ref": in["head"], "sha": sha}, "base": map[string]any{"ref": in["base"]}}
		f.pulls[n] = pr
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(pr)
	case r.Method == "GET" && strings.HasPrefix(path, "/repos/o/r/pulls/"):
		var n int
		fmt.Sscanf(strings.TrimPrefix(path, "/repos/o/r/pulls/"), "%d", &n)
		if pr, ok := f.pulls[n]; ok {
			_ = json.NewEncoder(w).Encode(pr)
			return
		}
		w.WriteHeader(404)
	case path == "/repos/o/r/commits/"+sha+"/check-runs":
		_, _ = io.WriteString(w, f.checks)
	case path == "/repos/o/r/commits/"+sha+"/status":
		_, _ = io.WriteString(w, f.status)
	default:
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}
}

func newFake(t *testing.T) (*fakeGH, *Client) {
	f := &fakeGH{issues: map[int]map[string]any{}, pulls: map[int]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return f, New(srv.URL, func() (string, error) { return "tok-secret", nil })
}

func TestEnsureIssueSearchesBeforeCreate(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	is, created, err := c.EnsureIssue(ctx, "o/r", "demo-F1-T1", "Titre", "Corps")
	if err != nil || !created || is.Number != 1 || !strings.HasSuffix(is.Body, Marker("demo-F1-T1")) {
		t.Fatalf("création: %+v %v %v", is, created, err)
	}
	is2, created, err := c.EnsureIssue(ctx, "o/r", "demo-F1-T1", "Titre", "Corps")
	if err != nil || created || is2.Number != 1 {
		t.Fatalf("réutilisation: %+v %v %v", is2, created, err)
	}
	// une clé qui est un préfixe ne doit pas être confondue
	_, created, err = c.EnsureIssue(ctx, "o/r", "demo-F1-T11", "T", "B")
	if err != nil || !created {
		t.Fatalf("clé distincte: %v %v", created, err)
	}
	if f.posts != 2 {
		t.Fatalf("POST attendus 2, obtenu %d", f.posts)
	}
	for _, a := range f.auth {
		if a != "Bearer tok-secret" {
			t.Fatal("authentification absente")
		}
	}
	if _, _, err := c.EnsureIssue(ctx, "o/../r", "demo-F1-T1", "T", "B"); err == nil {
		t.Fatal("dépôt invalide accepté")
	}
}

func TestBranchSHAAndNotFound(t *testing.T) {
	_, c := newFake(t)
	got, err := c.BranchSHA(context.Background(), "o/r", "aos/k")
	if err != nil || got != sha {
		t.Fatalf("%s %v", got, err)
	}
	_, err = c.BranchSHA(context.Background(), "o/r", "absente")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("404 attendu: %v", err)
	}
}

func TestEnsureDraftPRIdempotent(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	pr, created, err := c.EnsureDraftPR(ctx, "o/r", "aos/k", "main", "T", "Corps\nCloses #1")
	if err != nil || !created || !pr.Draft || pr.Head.SHA != sha || pr.Base.Ref != "main" {
		t.Fatalf("%+v %v %v", pr, created, err)
	}
	pr2, created, err := c.EnsureDraftPR(ctx, "o/r", "aos/k", "main", "T", "Corps")
	if err != nil || created || pr2.Number != pr.Number {
		t.Fatalf("réutilisation: %+v %v %v", pr2, created, err)
	}
	if f.prPosts != 1 {
		t.Fatalf("un seul POST attendu, obtenu %d", f.prPosts)
	}
	f.pulls[pr.Number]["draft"] = false
	if _, _, err := c.EnsureDraftPR(ctx, "o/r", "aos/k", "main", "T", "Corps"); err == nil {
		t.Fatal("PR existante non draft acceptée")
	} else {
		var ge *Error
		if !errors.As(err, &ge) || ge.Class != domain.ErrClassPolicy {
			t.Fatalf("erreur de politique attendue, obtenu %v", err)
		}
	}
}

func TestCIStateAggregation(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	cases := []struct {
		name, checks, status string
		required             []string
		want                 string
	}{
		{"no policy", `{"check_runs":[]}`, `{"statuses":[]}`, nil, domain.CIUnverified},
		{"absent", `{"check_runs":[]}`, `{"statuses":[]}`, []string{"tests"}, domain.CIPending},
		{"running", `{"check_runs":[{"id":1,"name":"tests","status":"in_progress","conclusion":null}]}`, `{"statuses":[]}`, []string{"tests"}, domain.CIPending},
		{"success", `{"check_runs":[{"id":1,"name":"tests","status":"completed","conclusion":"success"}]}`, `{"statuses":[]}`, []string{"tests"}, domain.CISuccess},
		{"failure", `{"check_runs":[{"id":1,"name":"tests","status":"completed","conclusion":"failure"}]}`, `{"statuses":[]}`, []string{"tests"}, domain.CIFailure},
		{"cancelled is failure", `{"check_runs":[{"id":1,"name":"tests","status":"completed","conclusion":"cancelled"}]}`, `{"statuses":[]}`, []string{"tests"}, domain.CIFailure},
		{"latest run wins", `{"check_runs":[{"id":1,"name":"tests","status":"completed","conclusion":"failure"},{"id":2,"name":"tests","status":"completed","conclusion":"success"}]}`, `{"statuses":[]}`, []string{"tests"}, domain.CISuccess},
		{"other sha ignored", `{"check_runs":[{"id":1,"name":"tests","status":"completed","conclusion":"success","head_sha":"ffffffffffffffffffffffffffffffffffffffff"}]}`, `{"statuses":[]}`, []string{"tests"}, domain.CIPending},
		{"status success", `{"check_runs":[]}`, `{"statuses":[{"context":"tests","state":"success"}]}`, []string{"tests"}, domain.CISuccess},
		{"status error", `{"check_runs":[]}`, `{"statuses":[{"context":"tests","state":"error"}]}`, []string{"tests"}, domain.CIFailure},
		{"mixed one missing", `{"check_runs":[{"id":1,"name":"tests","status":"completed","conclusion":"success"}]}`, `{"statuses":[]}`, []string{"tests", "lint"}, domain.CIPending},
		{"mixed one failing", `{"check_runs":[{"id":1,"name":"tests","status":"completed","conclusion":"success"}]}`, `{"statuses":[{"context":"lint","state":"failure"}]}`, []string{"tests", "lint"}, domain.CIFailure},
	}
	for _, cc := range cases {
		t.Run(cc.name, func(t *testing.T) {
			f.checks, f.status = cc.checks, cc.status
			got, ev, err := c.CIState(ctx, "o/r", sha, cc.required)
			if err != nil || got != cc.want {
				t.Fatalf("obtenu %s %v (%+v), attendu %s", got, err, ev, cc.want)
			}
		})
	}
}

func TestVerifySignature(t *testing.T) {
	secret := []byte("s3cr3t")
	body := []byte(`{"action":"closed"}`)
	sig := Sign(secret, body)
	if !VerifySignature(secret, body, sig) {
		t.Fatal("signature valide refusée")
	}
	if VerifySignature(secret, []byte(`{"action":"opened"}`), sig) {
		t.Fatal("corps modifié accepté")
	}
	if VerifySignature([]byte("autre"), body, sig) {
		t.Fatal("mauvais secret accepté")
	}
	if VerifySignature(secret, body, "") || VerifySignature(secret, body, "sha1=abc") || VerifySignature(secret, body, "sha256=zz") {
		t.Fatal("signature absente/malformée acceptée")
	}
	if VerifySignature(nil, body, sig) {
		t.Fatal("secret vide accepté")
	}
}

func TestTokenUnavailableIsPolicy(t *testing.T) {
	c := New("http://127.0.0.1:1", func() (string, error) { return "", errors.New("x") })
	_, err := c.BranchSHA(context.Background(), "o/r", "main")
	if ClassOf(err) != domain.ErrClassPolicy {
		t.Fatalf("%v", err)
	}
}
