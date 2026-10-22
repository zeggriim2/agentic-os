package engine_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/engine"
	gh "github.com/zeggriim2/agentic-os/internal/github"
	"github.com/zeggriim2/agentic-os/internal/gitops"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
	"github.com/zeggriim2/agentic-os/internal/testutil/pgtest"
)

// ------------------------------------------------------------------ fakes

type fakeHermes struct {
	mu       sync.Mutex
	runs     map[string]hermes.RunStatus
	keys     map[string]string // idempotency key → run id
	creates  int
	stops    int
	respond  func(profile string, req hermes.RunRequest) (string, int64)
	sequence int
}

func newFakeHermes(respond func(string, hermes.RunRequest) (string, int64)) *fakeHermes {
	return &fakeHermes{runs: map[string]hermes.RunStatus{}, keys: map[string]string{}, respond: respond}
}

func (f *fakeHermes) CreateRun(_ context.Context, profile, key string, req hermes.RunRequest) (hermes.CreateResult, error) {
	f.mu.Lock()
	if id, ok := f.keys[key]; ok {
		f.mu.Unlock()
		return hermes.CreateResult{RunID: id, Status: "started", Replayed: true}, nil
	}
	f.creates++
	f.sequence++
	id := fmt.Sprintf("run_%d", f.sequence)
	f.keys[key] = id
	f.mu.Unlock()
	out, total := f.respond(profile, req)
	f.mu.Lock()
	f.runs[id] = hermes.RunStatus{Status: "completed", Output: out, Completed: true,
		Usage: hermes.Usage{InputTokens: total / 2, OutputTokens: total - total/2, TotalTokens: total}, Runtime: hermes.Runtime{Provider: "p", Model: "m1"}}
	f.mu.Unlock()
	return hermes.CreateResult{RunID: id, Status: "started"}, nil
}

func (f *fakeHermes) GetRun(_ context.Context, _, id string) (hermes.RunStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.runs[id]
	if !ok {
		return st, &hermes.Error{Status: 404, Code: "run_not_found", Class: domain.ErrClassProvider}
	}
	return st, nil
}

func (f *fakeHermes) StreamEvents(_ context.Context, _, id string, fn func(hermes.Event) error) error {
	_ = fn(hermes.Event{Event: "message.delta", Seq: 1, Raw: []byte(`{"event":"message.delta","seq":1}`)})
	_ = fn(hermes.Event{Event: "tool.started", Seq: 2, Raw: []byte(`{"event":"tool.started","seq":2}`)})
	return nil
}

func (f *fakeHermes) StopRun(context.Context, string, string) error {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
	return nil
}
func (f *fakeHermes) Capabilities(context.Context, string) error       { return nil }
func (f *fakeHermes) Models(context.Context, string) ([]string, error) { return []string{"m1"}, nil }

type fakeGitHub struct {
	mu        sync.Mutex
	bare      string
	issues    map[string]int
	issuePOST int
	prs       map[string]*gh.PR
	ci        string
	merged    bool
}

func (f *fakeGitHub) EnsureIssue(_ context.Context, repo, key, title, body string) (gh.Issue, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n, ok := f.issues[key]; ok {
		return gh.Issue{Number: n, HTMLURL: fmt.Sprintf("https://github.com/%s/issues/%d", repo, n)}, false, nil
	}
	f.issuePOST++
	n := len(f.issues) + 1
	f.issues[key] = n
	return gh.Issue{Number: n, HTMLURL: fmt.Sprintf("https://github.com/%s/issues/%d", repo, n), Body: body + gh.Marker(key)}, true, nil
}

func (f *fakeGitHub) BranchSHA(_ context.Context, _, branch string) (string, error) {
	out, err := exec.Command("/usr/bin/git", "--git-dir", f.bare, "rev-parse", "refs/heads/"+branch).Output()
	if err != nil {
		return "", &gh.Error{Status: 404, Class: domain.ErrClassExternal, Msg: "not found"}
	}
	return strings.TrimSpace(string(out)), nil
}

func (f *fakeGitHub) EnsureDraftPR(ctx context.Context, repo, head, base, title, body string) (gh.PR, bool, error) {
	sha, err := f.BranchSHA(ctx, repo, head)
	if err != nil {
		return gh.PR{}, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := f.prs[head]; ok {
		return *pr, false, nil
	}
	if !strings.Contains(body, "Closes #") {
		return gh.PR{}, false, errors.New("corps de PR sans Closes")
	}
	pr := &gh.PR{Number: 42, HTMLURL: "https://github.com/o/r/pull/42", State: "open", Draft: true}
	pr.Head.Ref, pr.Head.SHA, pr.Base.Ref = head, sha, base
	f.prs[head] = pr
	return *pr, true, nil
}

func (f *fakeGitHub) GetPR(_ context.Context, _ string, n int) (gh.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, pr := range f.prs {
		if pr.Number == n {
			out := *pr
			if f.merged {
				out.Merged, out.State = true, "closed"
			}
			return out, nil
		}
	}
	return gh.PR{}, &gh.Error{Status: 404, Class: domain.ErrClassExternal}
}

func (f *fakeGitHub) CIState(context.Context, string, string, []string) (string, []gh.CheckEvidence, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ci, nil, nil
}

type notes struct {
	mu   sync.Mutex
	msgs []string
}

func (n *notes) Notify(_ context.Context, s string) {
	n.mu.Lock()
	n.msgs = append(n.msgs, s)
	n.mu.Unlock()
}

// ------------------------------------------------------------------ scénario

var (
	worktreeRe = regexp.MustCompile(`Worktree : (\S+)`)
	reviewRe   = regexp.MustCompile(`SHA à relire : ([0-9a-f]{40})`)
	qaRe       = regexp.MustCompile(`SHA à tester : ([0-9a-f]{40})`)
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("/usr/bin/git", append([]string{"-c", "user.name=Agent", "-c", "user.email=a@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

type env struct {
	t      *testing.T
	s      *store.Store
	e      *engine.Engine
	h      *fakeHermes
	g      *fakeGitHub
	n      *notes
	root   string
	review func(sha string, call int) string
	qa     func(sha string, call int) string
	po     func() (string, int64)
	calls  map[string]int
	mu     sync.Mutex
}

const poOK = "Voici la spec.\n```json\n{\"summary\":\"s\",\"user_stories\":[\"en tant que…\"],\"acceptance_criteria\":[\"c1\"],\"open_questions\":[]}\n```"
const designOK = "```json\n{\"summary\":\"d\",\"decisions\":[{\"decision\":\"Go\",\"rationale\":\"simple\",\"rejected_alternatives\":[\"Rust\"]}],\"risks\":[\"r1\"],\"tickets\":[{\"title\":\"Ajouter hello\",\"body\":\"Créer hello.txt\",\"acceptance_criteria\":[\"hello.txt existe\"]}]}\n```"

func reviewJSON(verdict, sha, findings string) string {
	return fmt.Sprintf("```json\n{\"verdict\":%q,\"sha\":%q,\"findings\":[%s],\"summary\":\"revue\"}\n```", verdict, sha, findings)
}

func qaJSON(verdict, sha string) string {
	return fmt.Sprintf("```json\n{\"verdict\":%q,\"sha\":%q,\"checks\":[{\"command\":\"go test ./...\",\"exit_code\":0}],\"acceptance\":[{\"criterion\":\"hello.txt existe\",\"status\":\"met\",\"evidence\":\"ls hello.txt\"}],\"summary\":\"ok\"}\n```", verdict, sha)
}

func newEnv(t *testing.T) *env {
	s := pgtest.New(t)
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	gitRun(t, base, "init", "--bare", bare)
	seed := filepath.Join(base, "seed")
	gitRun(t, base, "clone", bare, seed)
	_ = os.WriteFile(filepath.Join(seed, "README.md"), []byte("demo\n"), 0o644)
	_ = os.WriteFile(filepath.Join(seed, "main_test.go"), []byte("package main\n"), 0o644)
	gitRun(t, seed, "add", "-A")
	gitRun(t, seed, "commit", "-m", "init")
	gitRun(t, seed, "push", "origin", "HEAD:refs/heads/main")

	ev := &env{t: t, s: s, root: filepath.Join(base, "ws"), calls: map[string]int{}}
	ev.review = func(sha string, _ int) string { return reviewJSON("approved", sha, "") }
	ev.qa = func(sha string, _ int) string { return qaJSON("passed", sha) }
	ev.po = func() (string, int64) { return poOK, 1000 }
	ev.h = newFakeHermes(func(profile string, req hermes.RunRequest) (string, int64) {
		ev.mu.Lock()
		ev.calls[profile]++
		call := ev.calls[profile]
		ev.mu.Unlock()
		switch profile {
		case "aos-po":
			return ev.po()
		case "aos-tech-lead":
			return designOK, 1000
		case "aos-developer":
			wt := worktreeRe.FindStringSubmatch(req.Input)[1]
			_ = os.WriteFile(filepath.Join(wt, "hello.txt"), []byte(fmt.Sprintf("hello %d\n", call)), 0o644)
			gitRun(t, wt, "add", "hello.txt")
			gitRun(t, wt, "commit", "-m", fmt.Sprintf("feat: hello %d", call))
			return "fait\n```json\n{\"summary\":\"hello\",\"files_changed\":[\"hello.txt\"],\"tests\":[{\"command\":\"go test ./...\",\"exit_code\":0}],\"notes\":\"\"}\n```", 2000
		case "aos-reviewer":
			return ev.review(reviewRe.FindStringSubmatch(req.Input)[1], call), 500
		case "aos-qa":
			return ev.qa(qaRe.FindStringSubmatch(req.Input)[1], call), 500
		}
		return "", 0
	})
	ev.g = &fakeGitHub{bare: bare, issues: map[string]int{}, prs: map[string]*gh.PR{}, ci: domain.CIPending}
	ev.n = &notes{}
	ev.e = &engine.Engine{
		Store: s, Hermes: ev.h, GitHub: ev.g, Notifier: ev.n,
		Git: &gitops.Manager{Git: gitops.Runner{GitPath: "/usr/bin/git", Timeout: time.Minute}, Root: ev.root,
			RemoteURL: func(string) string { return bare }},
		Log:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Lease: 6 * time.Second, MaxRework: 3, PollInterval: 5 * time.Millisecond, Backoff: time.Millisecond,
	}
	ev.e.Init()
	return ev
}

func (ev *env) project() *store.Project {
	p, err := store.CreateProject(context.Background(), ev.s.Pool, store.ProjectInput{Slug: "demo", Name: "Demo", Repository: "o/r",
		BaseBranch: "main", Mode: "sandbox", Enabled: true, RequiredChecks: []string{"tests"}, TestCommand: "go test ./..."})
	if err != nil {
		ev.t.Fatal(err)
	}
	return p
}

func (ev *env) drain() {
	ev.t.Helper()
	for i := 0; i < 50; i++ {
		did, err := ev.e.RunOnce(context.Background(), "test-worker")
		if err != nil {
			ev.t.Fatal(err)
		}
		if !did {
			return
		}
	}
	ev.t.Fatal("file de jobs non vidée")
}

func (ev *env) feature(id int64) *store.Feature {
	f, err := store.GetFeature(context.Background(), ev.s.Pool, id)
	if err != nil {
		ev.t.Fatal(err)
	}
	return f
}

func (ev *env) pending(kind string) *store.Approval {
	ev.t.Helper()
	aps, err := store.ListApprovals(context.Background(), ev.s.Pool, store.ApprovalFilter{Status: "pending"}, store.Page{Limit: 50})
	if err != nil {
		ev.t.Fatal(err)
	}
	for i := range aps {
		if aps[i].Kind == kind {
			return &aps[i]
		}
	}
	ev.t.Fatalf("aucune approbation pending %s", kind)
	return nil
}

func (ev *env) decide(kind, decision, comment string) *store.Approval {
	ev.t.Helper()
	ap := ev.pending(kind)
	res, err := ev.e.DecideApproval(context.Background(), ap.ID, decision, comment, "admin")
	if err != nil {
		ev.t.Fatal(err)
	}
	return res
}

func (ev *env) ticket(featureID int64) *store.Ticket {
	ts, err := store.ListTicketsByFeature(context.Background(), ev.s.Pool, featureID)
	if err != nil || len(ts) == 0 {
		ev.t.Fatalf("tickets: %v", err)
	}
	return &ts[0]
}

func expect[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s : obtenu %v, attendu %v", what, got, want)
	}
}

// startToDev amène une feature jusqu'au développement lancé (HUMAN_GATE approuvé).
func (ev *env) startToDev() (*store.Feature, *store.Project) {
	ctx := context.Background()
	p := ev.project()
	f, err := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Hello", "Dire bonjour")
	if err != nil {
		ev.t.Fatal(err)
	}
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		ev.t.Fatal(err)
	}
	ev.drain()
	expect(ev.t, "après PO", ev.feature(f.ID).State, "SPEC_REVIEW")
	ev.decide(domain.ApprovalSpecReview, "approved", "")
	ev.drain()
	expect(ev.t, "après Tech Lead", ev.feature(f.ID).State, "TECH_DESIGN_REVIEW")
	if ap := ev.pending(domain.ApprovalDesignReview); !strings.Contains(ap.Effect, "1 issues sur o/r") {
		ev.t.Fatalf("effet: %s", ap.Effect)
	}
	ev.decide(domain.ApprovalDesignReview, "approved", "")
	ev.drain()
	expect(ev.t, "après issues", ev.feature(f.ID).State, "HUMAN_GATE")
	tk := ev.ticket(f.ID)
	if tk.IssueNumber == nil || tk.Key != fmt.Sprintf("demo-F%d-T1", f.ID) {
		ev.t.Fatalf("ticket: %+v", tk)
	}
	ev.decide(domain.ApprovalDevGate, "approved", "")
	return f, p
}

func TestWorkflowEndToEnd(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev()
	ev.drain()
	tk := ev.ticket(f.ID)
	expect(t, "ticket après dev/review/qa", tk.State, "AWAITING_PR_APPROVAL")
	expect(t, "feature", ev.feature(f.ID).State, "IN_DEV")
	remote, _ := ev.g.BranchSHA(ctx, "o/r", "aos/"+tk.Key)
	if tk.HeadSHA == nil || *tk.HeadSHA != remote {
		t.Fatalf("head_sha %v ≠ distant %s", tk.HeadSHA, remote)
	}
	ev.decide(domain.ApprovalPublishPR, "approved", "")
	ev.drain()
	tk = ev.ticket(f.ID)
	expect(t, "ticket après publication", tk.State, "PR_OPENED")
	if tk.PRNumber == nil || *tk.PRNumber != 42 || tk.CIState != domain.CIPending {
		t.Fatalf("PR: %+v", tk)
	}
	// CI en attente → pas de transition ; CI verte → HUMAN_MERGE ; merge → DONE
	if err := ev.e.PollPRs(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, "CI pending", ev.ticket(f.ID).State, "PR_OPENED")
	ev.g.ci = domain.CISuccess
	if _, err := ev.e.HandleGitHubEvent(ctx, "o/r", 42, "", ""); err != nil {
		t.Fatal(err)
	}
	expect(t, "CI verte", ev.ticket(f.ID).State, "HUMAN_MERGE")
	ev.g.merged = true
	if err := ev.e.PollPRs(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, "ticket mergé", ev.ticket(f.ID).State, "DONE")
	expect(t, "feature terminée", ev.feature(f.ID).State, "DONE")

	// Garanties : une issue créée, clés Hermes au format attendu, événements non-delta persistés, audit.
	expect(t, "issues créées", ev.g.issuePOST, 1)
	for k := range ev.h.keys {
		if !regexp.MustCompile(`^aos-job-\d+-a0$`).MatchString(k) {
			t.Fatalf("clé d'idempotence inattendue %s", k)
		}
	}
	expect(t, "runs soumis", ev.h.creates, 5)
	runs, _ := store.ListRuns(ctx, ev.s.Pool, store.RunFilter{}, store.Page{Limit: 50})
	for _, r := range runs {
		evs, _ := store.ListRunEvents(ctx, ev.s.Pool, r.ID)
		if r.Status != "completed" || len(evs) != 1 || evs[0].Event != "tool.started" || len(r.Artifact) == 0 {
			t.Fatalf("run %d: statut %s, %d événements", r.ID, r.Status, len(evs))
		}
	}
	entries, _ := store.ListAudit(ctx, ev.s.Pool, store.AuditFilter{SubjectType: "ticket", SubjectID: tk.ID}, store.Page{Limit: 200})
	actions := map[string]bool{}
	for _, a := range entries {
		actions[a.Action] = true
	}
	for _, want := range []string{"ticket.dev_started", "ticket.dev_pushed", "ticket.review_approved", "ticket.qa_passed", "ticket.pr_approved", "ticket.pr_published", "ticket.ci_success", "ticket.pr_merged"} {
		if !actions[want] {
			t.Errorf("audit manquant : %s", want)
		}
	}
	if len(ev.n.msgs) == 0 {
		t.Fatal("aucune notification")
	}
	// décision répétée refusée
	aps, _ := store.ListApprovals(ctx, ev.s.Pool, store.ApprovalFilter{}, store.Page{Limit: 50})
	if _, err := ev.e.DecideApproval(ctx, aps[0].ID, "approved", "", "admin"); !errors.Is(err, store.ErrAlreadyDecided) {
		t.Fatalf("décision répétée: %v", err)
	}
}

func TestWorkflowReworkPath(t *testing.T) {
	ev := newEnv(t)
	ev.review = func(sha string, call int) string {
		if call == 1 {
			return reviewJSON("changes_requested", sha, `{"severity":"P1","file":"hello.txt","message":"contenu incorrect"}`)
		}
		return reviewJSON("approved", sha, "")
	}
	f, _ := ev.startToDev()
	ev.drain()
	tk := ev.ticket(f.ID)
	expect(t, "après revue négative", tk.State, "CHANGES_REQUESTED")
	firstSHA := *tk.HeadSHA
	ap := ev.pending(domain.ApprovalRework)
	if !strings.Contains(string(ap.Details), "contenu incorrect") {
		t.Fatalf("findings absents: %s", ap.Details)
	}
	ev.decide(domain.ApprovalRework, "approved", "corrige le contenu")
	expect(t, "après rework approuvé", ev.ticket(f.ID).State, "DEV")
	ev.drain()
	tk = ev.ticket(f.ID)
	expect(t, "après 2e cycle", tk.State, "AWAITING_PR_APPROVAL")
	expect(t, "attempt", tk.Attempt, 1)
	if *tk.HeadSHA == firstSHA {
		t.Fatal("le rework doit produire un nouveau SHA")
	}
	hasA1 := false
	for k := range ev.h.keys {
		if strings.HasSuffix(k, "-a1") {
			hasA1 = true
		}
	}
	if !hasA1 {
		t.Fatal("clé d'idempotence du rework (a1) absente")
	}
	// rejet du publish_pr → ticket annulé → feature terminée
	ev.decide(domain.ApprovalPublishPR, "rejected", "pas maintenant")
	expect(t, "ticket", ev.ticket(f.ID).State, "CANCELLED")
	expect(t, "feature", ev.feature(f.ID).State, "DONE")
}

func TestReworkExhaustedBlocks(t *testing.T) {
	ev := newEnv(t)
	ev.e.MaxRework = 0
	ev.review = func(sha string, _ int) string {
		return reviewJSON("changes_requested", sha, `{"severity":"P0","file":"x","message":"faille"}`)
	}
	f, _ := ev.startToDev()
	ev.drain()
	ap := ev.decide(domain.ApprovalRework, "approved", "")
	expect(t, "approbation", ap.Status, "execution_failed")
	expect(t, "ticket", ev.ticket(f.ID).State, "BLOCKED")
}

func TestInvalidVerdictFailsClosed(t *testing.T) {
	ev := newEnv(t)
	ev.review = func(sha string, _ int) string { return reviewJSON("lgtm", sha, "") }
	f, _ := ev.startToDev()
	ev.drain()
	tk := ev.ticket(f.ID)
	expect(t, "ticket", tk.State, "BLOCKED")
	expect(t, "état précédent", str(tk.PreviousState), "REVIEW")
	jobs, _ := store.ListJobsForSubject(context.Background(), ev.s.Pool, "ticket", tk.ID)
	last := jobs[len(jobs)-1]
	if last.Kind != "review" || last.Status != "failed" || str(last.ErrorClass) != domain.ErrClassInvalidOutput {
		t.Fatalf("job review: %+v", last)
	}
	// un verdict "approved" avec un SHA différent est également refusé
	ev.review = func(_ string, _ int) string { return reviewJSON("approved", strings.Repeat("a", 40), "") }
	if _, err := ev.e.TicketAction(context.Background(), tk.ID, "retry", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	expect(t, "ticket après retry", ev.ticket(f.ID).State, "BLOCKED")
	if len(ev.n.msgs) == 0 || !strings.Contains(strings.Join(ev.n.msgs, "\n"), "bloqué") {
		t.Fatal("notification de blocage absente")
	}
}

func TestBudgetExceededBlocksButStoresArtifact(t *testing.T) {
	ev := newEnv(t)
	ev.po = func() (string, int64) { return poOK, 10_000_000 }
	ctx := context.Background()
	p := ev.project()
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Gros", "")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	nf := ev.feature(f.ID)
	expect(t, "feature", nf.State, "BLOCKED")
	expect(t, "précédent", str(nf.PreviousState), "SPEC")
	runs, _ := store.ListRunsForSubject(ctx, ev.s.Pool, "feature", f.ID)
	if len(runs) != 1 || len(runs[0].Artifact) == 0 || !strings.Contains(str(runs[0].Error), "budget") {
		t.Fatalf("run: %+v", runs)
	}
	jobs, _ := store.ListJobsForSubject(ctx, ev.s.Pool, "feature", f.ID)
	expect(t, "classe", str(jobs[0].ErrorClass), domain.ErrClassBudget)
	// retry après correction du budget
	ev.po = func() (string, int64) { return poOK, 10 }
	if _, err := ev.e.FeatureAction(ctx, f.ID, "retry", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	expect(t, "après retry", ev.feature(f.ID).State, "SPEC_REVIEW")
}

func TestRecoveryAttachesToExistingRun(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "F", "")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	// Simule un worker mort après soumission : lease pris, run Hermes existant, lease expiré.
	j, err := store.Dequeue(ctx, ev.s.Pool, "dead-worker", time.Millisecond)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	r, _ := store.CreateRun(ctx, ev.s.Pool, j.ID, "po", "aos-po")
	ev.h.runs["run_existing"] = hermes.RunStatus{Status: "completed", Output: poOK, Completed: true, Usage: hermes.Usage{TotalTokens: 5}}
	_ = store.SetRunHermesID(ctx, ev.s.Pool, r.ID, "run_existing")
	time.Sleep(20 * time.Millisecond)
	if err := ev.e.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	expect(t, "resoumissions", ev.h.creates, 0)
	expect(t, "feature", ev.feature(f.ID).State, "SPEC_REVIEW")
	got, _ := store.GetRun(ctx, ev.s.Pool, r.ID)
	expect(t, "run", got.Status, "completed")
}

func TestCancelFeatureStopsEverything(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev()
	// develop en file, pas encore exécuté
	if _, err := ev.e.FeatureAction(ctx, f.ID, "cancel", "admin"); err != nil {
		t.Fatal(err)
	}
	expect(t, "feature", ev.feature(f.ID).State, "CANCELLED")
	expect(t, "ticket", ev.ticket(f.ID).State, "CANCELLED")
	jobs, _ := store.ListActiveJobs(ctx, ev.s.Pool)
	expect(t, "jobs actifs", len(jobs), 0)
	if _, err := ev.e.FeatureAction(ctx, f.ID, "cancel", "admin"); !errors.Is(err, domain.ErrTransition) {
		t.Fatalf("double annulation: %v", err)
	}
}

func TestDisabledProjectRefusesStart(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p, _ := store.CreateProject(ctx, ev.s.Pool, store.ProjectInput{Slug: "off", Name: "Off", Repository: "o/off", BaseBranch: "main", Mode: "sandbox"})
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "F", "")
	_, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin")
	var ue *engine.UserError
	if !errors.As(err, &ue) || ue.Code != "project_disabled" {
		t.Fatalf("attendu project_disabled: %v", err)
	}
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
