package engine_test

import (
	"context"
	"encoding/json"
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
	"github.com/zeggriim2/agentic-os/internal/notify"
	"github.com/zeggriim2/agentic-os/internal/store"
	"github.com/zeggriim2/agentic-os/internal/testutil/pgtest"
)

// ------------------------------------------------------------------ fakes

type fakeHermes struct {
	cacheHeavy bool // usage dominé par le cache (budget sur les tokens nouveaux)
	mu         sync.Mutex
	runs       map[string]hermes.RunStatus
	keys       map[string]string // idempotency key → run id
	creates    int
	stops      int
	respond    func(profile string, req hermes.RunRequest) (string, int64)
	hold       bool  // les runs restent "running" (jamais terminés)
	stopErr    error // erreur renvoyée par StopRun
	stopEnds   bool  // un StopRun réussi termine le run (statut "cancelled")
	sequence   int
	// reqs garde le corps envoyé par clé d'idempotence (vérification de model/provider).
	reqs map[string]hermes.RunRequest
	// rejectProvider simule le refus de résolution du fournisseur par Hermes (run failed, 0 token).
	rejectProvider func(req hermes.RunRequest) bool
}

func newFakeHermes(respond func(string, hermes.RunRequest) (string, int64)) *fakeHermes {
	return &fakeHermes{runs: map[string]hermes.RunStatus{}, keys: map[string]string{}, reqs: map[string]hermes.RunRequest{}, respond: respond}
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
	f.reqs[key] = req
	if f.rejectProvider != nil && f.rejectProvider(req) {
		f.runs[id] = hermes.RunStatus{Status: "failed", Error: "⚠️ Échec d'authentification du fournisseur: " + req.Provider + " non configuré",
			Requested: &hermes.Runtime{Provider: req.Provider, Model: req.Model}, RouteSource: "raw_request"}
		f.mu.Unlock()
		return hermes.CreateResult{RunID: id, Status: "started"}, nil
	}
	f.mu.Unlock()
	out, total := f.respond(profile, req)
	f.mu.Lock()
	if f.hold {
		f.runs[id] = hermes.RunStatus{Status: "running"}
		f.mu.Unlock()
		return hermes.CreateResult{RunID: id, Status: "started"}, nil
	}
	usage := hermes.Usage{InputTokens: total / 2, OutputTokens: total - total/2, TotalTokens: total, CacheReadTokens: total / 4}
	if f.cacheHeavy { // run outillé typique : 90 % du total relu du cache, 2 % de sortie
		usage = hermes.Usage{InputTokens: total - total/50, OutputTokens: total / 50, TotalTokens: total, CacheReadTokens: total * 9 / 10}
	}
	f.runs[id] = hermes.RunStatus{Status: "completed", Output: out, Completed: true, Usage: usage, Runtime: hermes.Runtime{Provider: "p", Model: "m1"}}
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

func (f *fakeHermes) StopRun(_ context.Context, _, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	if f.stopErr != nil {
		return &hermes.Error{Status: 502, Class: domain.ErrClassProvider, Msg: f.stopErr.Error()}
	}
	if st, ok := f.runs[id]; ok && f.stopEnds && !st.Terminal() {
		st.Status = "cancelled"
		f.runs[id] = st
	}
	return nil
}
func (f *fakeHermes) Capabilities(context.Context, string) error       { return nil }
func (f *fakeHermes) Models(context.Context, string) ([]string, error) { return []string{"m1"}, nil }
func (f *fakeHermes) ModelOptions(context.Context, string) (hermes.ModelOptions, error) {
	return hermes.ModelOptions{Model: "m1", Provider: "p", Providers: []hermes.ModelProvider{
		{Slug: "p", Name: "P", IsCurrent: true, Authenticated: true, Models: []string{"m1", "m2"}},
		{Slug: "autre", Name: "Autre", Authenticated: false, Models: []string{}},
	}}, nil
}

type fakeGitHub struct {
	mu        sync.Mutex
	bare      string
	issues    map[string]int
	issuePOST int
	prs       map[string]*gh.PR
	ci        string
	merged    bool
	prErr     error
	prBody    string // corps de la dernière PR créée
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
	f.prBody = body
	pr := &gh.PR{Number: 42, HTMLURL: "https://github.com/o/r/pull/42", State: "open", Draft: true}
	pr.Head.Ref, pr.Head.SHA, pr.Base.Ref = head, sha, base
	f.prs[head] = pr
	return *pr, true, nil
}

func (f *fakeGitHub) GetPR(_ context.Context, _ string, n int) (gh.PR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prErr != nil {
		return gh.PR{}, &gh.Error{Status: 502, Class: domain.ErrClassExternal, Msg: f.prErr.Error()}
	}
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
	mu    sync.Mutex
	msgs  []string
	dests []notify.Destination
}

func (n *notes) Notify(_ context.Context, to notify.Destination, s string) {
	n.mu.Lock()
	n.msgs = append(n.msgs, s)
	n.dests = append(n.dests, to)
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
	telegramChat   string // canal Telegram du projet de test
	telegramThread int64
	agentProfiles  map[string]string // profils propres au projet de test (nil = défaut)
	t              *testing.T
	s              *store.Store
	e              *engine.Engine
	h              *fakeHermes
	g              *fakeGitHub
	n              *notes
	root           string
	review         func(sha string, call int) string
	qa             func(sha string, call int) string
	po             func() (string, int64)
	calls          map[string]int
	mu             sync.Mutex
	// devHook est appelé après le commit du développeur (simule un événement concurrent).
	devHook func(worktree string)
	// devNoChange : le développeur ne commit rien et l'atteste (files_changed vide).
	devNoChange bool
	// design remplace le livrable du Tech Lead (vide = designOK, un ticket).
	design string
	// agentModels : modèles imposés par le projet (rôle → fournisseur/modèle).
	agentModels map[string]domain.ModelChoice
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
		// Les profils propres au projet (aos-demo-<rôle>) se comportent comme les profils par défaut.
		switch strings.Replace(profile, "aos-demo-", "aos-", 1) {
		case "aos-po":
			return ev.po()
		case "aos-tech-lead":
			if ev.design != "" {
				return ev.design, 1000
			}
			return designOK, 1000
		case "aos-developer":
			wt := worktreeRe.FindStringSubmatch(req.Input)[1]
			if ev.devNoChange {
				return "```json\n{\"summary\":\"rien à corriger : échec dû à l'environnement\",\"files_changed\":[],\"tests\":[{\"command\":\"go test ./...\",\"exit_code\":0}]}\n```", 2000
			}
			_ = os.WriteFile(filepath.Join(wt, "hello.txt"), []byte(fmt.Sprintf("hello %d\n", call)), 0o644)
			gitRun(t, wt, "add", "hello.txt")
			gitRun(t, wt, "commit", "-m", fmt.Sprintf("feat: hello %d", call))
			if ev.devHook != nil {
				ev.devHook(wt)
			}
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
		BaseBranch: "main", Mode: "sandbox", Enabled: true, RequiredChecks: []string{"tests"}, TestCommand: "go test ./...",
		AgentProfiles: ev.agentProfiles, AgentModels: ev.agentModels, TelegramChatID: ev.telegramChat, TelegramThreadID: ev.telegramThread})
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
		evs, _, _ := store.ListRunEvents(ctx, ev.s.Pool, r.ID)
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
	// L'usage Hermes est enregistré avec sa part de cache (fake : cache = total/4).
	var nRuns, wrong int
	if err := ev.s.Pool.QueryRow(context.Background(), `SELECT count(*), count(*) FILTER (WHERE total_tokens > 0 AND cache_read_tokens <> total_tokens/4) FROM runs`).Scan(&nRuns, &wrong); err != nil {
		t.Fatal(err)
	}
	if nRuns == 0 || wrong != 0 {
		t.Fatalf("cache des runs : %d runs, %d incorrects", nRuns, wrong)
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
	if did, err := ev.e.RunOnce(context.Background(), "approval-worker"); err != nil || !did {
		t.Fatalf("exécution durable du rework: did=%v err=%v", did, err)
	}
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
	ev.drain()
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
	ev.drain()
	ap, _ = store.GetApproval(context.Background(), ev.s.Pool, ap.ID)
	expect(t, "approbation", ap.Status, "execution_failed")
	expect(t, "ticket", ev.ticket(f.ID).State, "BLOCKED")
}

func TestApprovalExecutionSurvivesEngineRestart(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	f, err := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Durable", "")
	if err != nil {
		t.Fatal(err)
	}
	f, err = store.SetFeatureState(ctx, ev.s.Pool, f.ID, f.Version, string(domain.FeatureSpec), "", "")
	if err != nil {
		t.Fatal(err)
	}
	f, err = store.SetFeatureState(ctx, ev.s.Pool, f.ID, f.Version, string(domain.FeatureSpecReview), "", "")
	if err != nil {
		t.Fatal(err)
	}
	ap, _, err := store.CreateApproval(ctx, ev.s.Pool, store.NewApproval{Kind: domain.ApprovalSpecReview, SubjectType: "feature", SubjectID: f.ID,
		Title: "Spec", Effect: "Lancer la conception"})
	if err != nil {
		t.Fatal(err)
	}
	decided, err := ev.e.DecideApproval(ctx, ap.ID, domain.ApprovalApproved, "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "décision persistée", decided.Status, domain.ApprovalApproved)
	expect(t, "effet différé", ev.feature(f.ID).State, string(domain.FeatureSpecReview))
	jobs, err := store.ListActiveJobs(ctx, ev.s.Pool)
	if err != nil || len(jobs) != 1 || jobs[0].Kind != domain.JobExecuteApproval {
		t.Fatalf("job durable absent: %+v err=%v", jobs, err)
	}
	restarted := &engine.Engine{Store: ev.s, Log: ev.e.Log, MaxRework: ev.e.MaxRework, Backoff: time.Millisecond}
	restarted.Init()
	if did, err := restarted.RunOnce(ctx, "restarted-worker"); err != nil || !did {
		t.Fatalf("reprise après redémarrage: did=%v err=%v", did, err)
	}
	expect(t, "effet exécuté", ev.feature(f.ID).State, string(domain.FeatureTechDesign))
	ap, _ = store.GetApproval(ctx, ev.s.Pool, ap.ID)
	expect(t, "approbation exécutée", ap.Status, domain.ApprovalExecuted)
}

// Deux workers exécutent simultanément deux jobs execute_approval pour la même
// approbation (doublon injecté, ou reprise après perte de lease) : l'effet
// n'est appliqué qu'une fois, et un rejeu ultérieur est sans effet.
func TestApprovalConcurrentExecutionAppliesEffectOnce(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	f, err := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Concurrence", "")
	if err != nil {
		t.Fatal(err)
	}
	f, _ = store.SetFeatureState(ctx, ev.s.Pool, f.ID, f.Version, string(domain.FeatureSpec), "", "")
	f, _ = store.SetFeatureState(ctx, ev.s.Pool, f.ID, f.Version, string(domain.FeatureSpecReview), "", "")
	ap, _, err := store.CreateApproval(ctx, ev.s.Pool, store.NewApproval{Kind: domain.ApprovalSpecReview, SubjectType: "feature", SubjectID: f.ID,
		Title: "Spec", Effect: "Lancer la conception"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.e.DecideApproval(ctx, ap.ID, domain.ApprovalApproved, "", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, created, err := store.EnqueueJob(ctx, ev.s.Pool, store.NewJob{Kind: domain.JobExecuteApproval, SubjectType: "approval",
		SubjectID: ap.ID, IdempotencyKey: "doublon-concurrent"}); err != nil || !created {
		t.Fatalf("doublon: %v %v", created, err)
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, owner := range []string{"worker-a", "worker-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			did, err := ev.e.RunOnce(ctx, owner)
			if err == nil && !did {
				err = fmt.Errorf("%s n'a pris aucun job", owner)
			}
			errs <- err
		}(owner)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	countEffects := func() (techDesign, executed int) {
		jobs, _ := store.ListJobsForSubject(ctx, ev.s.Pool, "feature", f.ID)
		for _, j := range jobs {
			if j.Kind == domain.JobTechDesign {
				techDesign++
			}
		}
		entries, _ := store.ListAudit(ctx, ev.s.Pool, store.AuditFilter{SubjectType: "feature", SubjectID: f.ID}, store.Page{Limit: 200})
		for _, a := range entries {
			if a.Action == "approval.executed" {
				executed++
			}
		}
		return
	}
	td, ex := countEffects()
	expect(t, "jobs tech_design", td, 1)
	expect(t, "audits approval.executed", ex, 1)
	expect(t, "feature", ev.feature(f.ID).State, string(domain.FeatureTechDesign))
	execJobs, _ := store.ListJobsForSubject(ctx, ev.s.Pool, "approval", ap.ID)
	for _, j := range execJobs {
		expect(t, fmt.Sprintf("job %d", j.ID), j.Status, domain.JobSucceeded)
	}
	// rejeu après perte de lease : le job revient en file et repasse
	if _, err := ev.s.Pool.Exec(ctx, `UPDATE jobs SET status='queued', run_after=now() WHERE id=$1`, execJobs[0].ID); err != nil {
		t.Fatal(err)
	}
	if did, err := ev.e.RunOnce(ctx, "worker-c"); err != nil || !did {
		t.Fatalf("rejeu: did=%v err=%v", did, err)
	}
	td, ex = countEffects()
	expect(t, "jobs tech_design après rejeu", td, 1)
	expect(t, "audits après rejeu", ex, 1)
	got, _ := store.GetApproval(ctx, ev.s.Pool, ap.ID)
	expect(t, "approbation", got.Status, domain.ApprovalExecuted)
}

func TestApprovalFailsWhenApprovedProjectSnapshotChanges(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	f, err := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Snapshot", "Projet immuable")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	ev.decide(domain.ApprovalSpecReview, "approved", "")
	ev.drain()
	ap := ev.pending(domain.ApprovalDesignReview)
	_, err = store.UpdateProject(ctx, ev.s.Pool, p.ID, store.ProjectInput{Slug: p.Slug, Name: p.Name, Repository: "o/changed",
		BaseBranch: p.BaseBranch, RequiredChecks: p.RequiredChecks, Mode: p.Mode, Enabled: p.Enabled, TestCommand: p.TestCommand})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.e.DecideApproval(ctx, ap.ID, domain.ApprovalApproved, "", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	ap, _ = store.GetApproval(ctx, ev.s.Pool, ap.ID)
	expect(t, "approbation", ap.Status, domain.ApprovalExecutionFailed)
	expect(t, "feature inchangée", ev.feature(f.ID).State, string(domain.FeatureTechDesignReview))
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

func TestBudgetCountsFreshTokensNotCacheReads(t *testing.T) {
	ev := newEnv(t)
	ev.h.cacheHeavy = true
	// 2 000 000 tokens bruts dont 1 800 000 relus du cache : 200 000 nouveaux < budget 400 000.
	ev.po = func() (string, int64) { return poOK, 2_000_000 }
	ctx := context.Background()
	p := ev.project()
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Cache", "")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	expect(t, "feature", ev.feature(f.ID).State, "SPEC_REVIEW")
	// 5 000 000 bruts → 500 000 nouveaux > 400 000 : rejeté, message explicite.
	ev.po = func() (string, int64) { return poOK, 5_000_000 }
	f2, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Gros", "")
	if _, err := ev.e.FeatureAction(ctx, f2.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	expect(t, "feature trop chère", ev.feature(f2.ID).State, "BLOCKED")
	runs, _ := store.ListRunsForSubject(ctx, ev.s.Pool, "feature", f2.ID)
	if len(runs) != 1 || !strings.Contains(str(runs[0].Error), "500000 tokens nouveaux (entrée nouvelle + sortie) > 400000") {
		t.Fatalf("message : %v", str(runs[0].Error))
	}
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

// ------------------------------------------------------------------ P1 fiabilité

func (ev *env) job(kind string) store.Job {
	ev.t.Helper()
	var id int64
	if err := ev.s.Pool.QueryRow(context.Background(), `SELECT id FROM jobs WHERE kind=$1 ORDER BY id DESC LIMIT 1`, kind).Scan(&id); err != nil {
		ev.t.Fatalf("job %s: %v", kind, err)
	}
	j, err := store.GetJob(context.Background(), ev.s.Pool, id)
	if err != nil {
		ev.t.Fatal(err)
	}
	return *j
}

func TestRecoveryFailsJobAndBlocksSubjectAtomically(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Atomique", "")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	j, err := store.Dequeue(ctx, ev.s.Pool, "dead-worker", time.Millisecond)
	if err != nil || j == nil {
		t.Fatal(err)
	}
	if _, err := ev.s.Pool.Exec(ctx, `UPDATE jobs SET attempts=max_attempts WHERE id=$1`, j.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	// Panne simulée au milieu de la transaction : l'écriture d'audit du blocage échoue.
	if _, err := ev.s.Pool.Exec(ctx, `CREATE FUNCTION fail_block() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.action = 'feature.fail' THEN RAISE EXCEPTION 'panne simulée'; END IF; RETURN NEW; END; $$;
		CREATE TRIGGER fail_block BEFORE INSERT ON audit_log FOR EACH ROW EXECUTE FUNCTION fail_block();`); err != nil {
		t.Fatal(err)
	}
	if err := ev.e.Recover(ctx); err == nil {
		t.Fatal("la recovery devait signaler l'échec du blocage")
	}
	got, _ := store.GetJob(ctx, ev.s.Pool, j.ID)
	expect(t, "job après panne (rollback)", got.Status, domain.JobLeased)
	expect(t, "feature après panne", ev.feature(f.ID).State, string(domain.FeatureSpec))
	if _, err := ev.s.Pool.Exec(ctx, `DROP TRIGGER fail_block ON audit_log`); err != nil {
		t.Fatal(err)
	}
	if err := ev.e.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetJob(ctx, ev.s.Pool, j.ID)
	expect(t, "job", got.Status, domain.JobFailed)
	nf := ev.feature(f.ID)
	expect(t, "feature", nf.State, string(domain.FeatureBlocked))
	expect(t, "état précédent", str(nf.PreviousState), string(domain.FeatureSpec))
}

func TestFencingCancelsPushAfterLeaseLoss(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev()
	// Pendant le run développeur, un autre worker récupère le lease.
	ev.devHook = func(string) {
		if _, err := ev.s.Pool.Exec(ctx, `UPDATE jobs SET lease_owner='other-worker' WHERE kind='develop' AND status='leased'`); err != nil {
			t.Error(err)
		}
	}
	ev.drain()
	tk := ev.ticket(f.ID)
	if _, err := ev.g.BranchSHA(ctx, "o/r", "aos/"+tk.Key); err == nil {
		t.Fatal("le worker sans lease a poussé la branche")
	}
	expect(t, "ticket", tk.State, string(domain.TicketDev))
	j := ev.job(domain.JobDevelop)
	expect(t, "job develop", j.Status, domain.JobLeased)
	expect(t, "propriétaire", str(j.LeaseOwner), "other-worker")
}

func TestExternalPushOnBranchBlocksDevelop(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev()
	tk := ev.ticket(f.ID)
	branch := "aos/" + tk.Key
	var foreign string
	ev.devHook = func(string) {
		dir := filepath.Join(t.TempDir(), "intrus")
		gitRun(t, filepath.Dir(dir), "clone", ev.g.bare, dir)
		gitRun(t, dir, "checkout", "-b", branch)
		_ = os.WriteFile(filepath.Join(dir, "intrus.txt"), []byte("x"), 0o644)
		gitRun(t, dir, "add", "intrus.txt")
		gitRun(t, dir, "commit", "-m", "push externe")
		gitRun(t, dir, "push", "origin", branch)
		foreign = gitRun(t, dir, "rev-parse", "HEAD")
	}
	ev.drain()
	remote, _ := ev.g.BranchSHA(ctx, "o/r", branch)
	expect(t, "branche distante intacte", remote, foreign)
	tk = ev.ticket(f.ID)
	expect(t, "ticket", tk.State, string(domain.TicketBlocked))
	expect(t, "état précédent", str(tk.PreviousState), string(domain.TicketDev))
	if !strings.Contains(tk.StateReason, "sans avoir été poussée par Agentic OS") {
		t.Fatalf("raison: %s", tk.StateReason)
	}
	j := ev.job(domain.JobDevelop)
	expect(t, "classe", str(j.ErrorClass), domain.ErrClassPolicy)
}

func TestHeartbeatFailuresAbandonJobBeforeLeaseExpiry(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	ev.e.Lease = 600 * time.Millisecond
	ev.h.hold = true
	var calls int
	var mu sync.Mutex
	ev.e.HeartbeatFn = func(ctx context.Context, id int64, owner string, lease time.Duration) (bool, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 { // la vérification avant soumission Hermes passe
			return store.Heartbeat(ctx, ev.s.Pool, id, owner, lease)
		}
		return false, errors.New("base injoignable")
	}
	p := ev.project()
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Heartbeat", "")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := ev.e.RunOnce(ctx, "test-worker"); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("le worker n'a pas abandonné le job malgré l'échec des heartbeats")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("abandon trop tardif: %v", elapsed)
	}
	j := ev.job(domain.JobPOSpec)
	expect(t, "job laissé à la recovery", j.Status, domain.JobLeased)
	expect(t, "feature", ev.feature(f.ID).State, string(domain.FeatureSpec))
	time.Sleep(700 * time.Millisecond)
	if err := ev.e.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, "job après recovery", ev.job(domain.JobPOSpec).Status, domain.JobQueued)
}

// ------------------------------------------------------------------ arrêt Hermes

// runningPOJob prépare une feature en SPEC avec un run Hermes en cours.
func (ev *env) runningPOJob() (*store.Feature, *store.Run) {
	ev.t.Helper()
	ctx := context.Background()
	p := ev.project()
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Arrêt", "")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		ev.t.Fatal(err)
	}
	j, err := store.Dequeue(ctx, ev.s.Pool, "worker-x", time.Minute)
	if err != nil || j == nil {
		ev.t.Fatal(err)
	}
	r, _ := store.CreateRun(ctx, ev.s.Pool, j.ID, "po", "aos-po")
	ev.h.mu.Lock()
	ev.h.runs["run_live"] = hermes.RunStatus{Status: "running"}
	ev.h.mu.Unlock()
	if err := store.SetRunHermesID(ctx, ev.s.Pool, r.ID, "run_live"); err != nil {
		ev.t.Fatal(err)
	}
	return f, r
}

func TestStopRunIsNotPresumedWhenHermesFails(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, r := ev.runningPOJob()
	ev.h.stopErr = errors.New("hermes indisponible")
	got, err := ev.e.StopRun(ctx, r.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "run après échec de l'arrêt", got.Status, domain.RunStopping)
	if got.StopAttempts != 1 || got.StopError == nil || got.FinishedAt != nil {
		t.Fatalf("tentative non tracée: %+v", got)
	}
	expect(t, "job", ev.job(domain.JobPOSpec).Status, domain.JobCancelled)
	expect(t, "feature", ev.feature(f.ID).State, string(domain.FeatureBlocked))
	// Hermes répond mais le run tourne encore : toujours pas confirmé.
	ev.h.stopErr = nil
	if err := ev.e.ReconcileStops(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetRun(ctx, ev.s.Pool, r.ID)
	expect(t, "run sans confirmation", got.Status, domain.RunStopping)
	// Hermes confirme l'arrêt.
	ev.h.stopEnds = true
	if err := ev.e.ReconcileStops(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetRun(ctx, ev.s.Pool, r.ID)
	expect(t, "run confirmé", got.Status, domain.RunStopped)
	if got.FinishedAt == nil || got.StopAttempts != 3 || got.StopError != nil {
		t.Fatalf("finalisation: %+v", got)
	}
}

func TestStopRunKeepsRealHermesOutcome(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	_, r := ev.runningPOJob()
	// Le run s'est terminé chez Hermes juste avant l'arrêt : on garde l'issue réelle.
	ev.h.mu.Lock()
	ev.h.runs["run_live"] = hermes.RunStatus{Status: "completed", Output: poOK, Completed: true, Usage: hermes.Usage{InputTokens: 30, TotalTokens: 42, CacheReadTokens: 20, CacheWriteTokens: 3}}
	ev.h.mu.Unlock()
	got, err := ev.e.StopRun(ctx, r.ID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "statut réel", got.Status, domain.RunCompleted)
	expect(t, "tokens", got.TotalTokens, int64(42))
	expect(t, "cache relu", got.CacheReadTokens, int64(20))
	expect(t, "cache écrit", got.CacheWriteTokens, int64(3))
	if _, err := ev.e.StopRun(ctx, r.ID, "admin"); err == nil {
		t.Fatal("arrêt d'un run terminé accepté")
	}
}

func TestCancelFeatureLeavesUnconfirmedStopsPending(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, r := ev.runningPOJob()
	ev.h.stopErr = errors.New("timeout")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "cancel", "admin"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRun(ctx, ev.s.Pool, r.ID)
	expect(t, "run", got.Status, domain.RunStopping)
	ev.h.stopErr, ev.h.stopEnds = nil, true
	_ = ev.e.ReconcileStops(ctx)
	got, _ = store.GetRun(ctx, ev.s.Pool, r.ID)
	expect(t, "run après réconciliation", got.Status, domain.RunStopped)
}

// ------------------------------------------------------------------ webhooks

func mergedPayload(prNumber int) []byte {
	return []byte(fmt.Sprintf(`{"action":"closed","repository":{"full_name":"o/r"},"pull_request":{"number":%d,"head":{"sha":"x","ref":"y"}}}`, prNumber))
}

func TestWebhookFailureIsRetriedUntilProcessed(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev()
	ev.drain()
	ev.decide(domain.ApprovalPublishPR, "approved", "")
	ev.drain()
	expect(t, "ticket", ev.ticket(f.ID).State, string(domain.TicketPROpened))
	ev.g.merged = true
	ev.g.prErr = errors.New("GitHub 502")
	status, _, err := ev.e.ReceiveDelivery(ctx, "del-1", "pull_request", mergedPayload(42))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "réponse", status, engine.DeliveryRetry)
	d, _ := store.GetDelivery(ctx, ev.s.Pool, "del-1")
	if d.Status != "failed" || d.Attempts != 1 || d.NextAttemptAt == nil || d.LastError == nil {
		t.Fatalf("livraison: %+v", d)
	}
	expect(t, "ticket inchangé", ev.ticket(f.ID).State, string(domain.TicketPROpened))
	// Encore en panne : nouvelle tentative, toujours en échec.
	time.Sleep(10 * time.Millisecond)
	if err := ev.e.RetryDeliveries(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = store.GetDelivery(ctx, ev.s.Pool, "del-1")
	expect(t, "tentatives", d.Attempts, 2)
	// GitHub revient : la reprise traite la livraison et fait évoluer le ticket.
	ev.g.prErr = nil
	time.Sleep(10 * time.Millisecond)
	if err := ev.e.RetryDeliveries(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ = store.GetDelivery(ctx, ev.s.Pool, "del-1")
	expect(t, "livraison", d.Status, "processed")
	expect(t, "ticket mergé", ev.ticket(f.ID).State, string(domain.TicketDone))
	expect(t, "feature", ev.feature(f.ID).State, string(domain.FeatureDone))
	// Un doublon d'une livraison traitée n'est pas rejoué.
	status, _, _ = ev.e.ReceiveDelivery(ctx, "del-1", "pull_request", mergedPayload(42))
	expect(t, "doublon", status, engine.DeliveryDuplicate)
	d, _ = store.GetDelivery(ctx, ev.s.Pool, "del-1")
	expect(t, "tentatives après doublon", d.Attempts, 3)
}

func TestWebhookRedeliveryRetriesImmediatelyAndRetriesAreBounded(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev()
	ev.drain()
	ev.decide(domain.ApprovalPublishPR, "approved", "")
	ev.drain()
	ev.g.prErr = errors.New("GitHub 502")
	ev.e.Backoff = time.Hour // pas de reprise automatique pendant le test
	if status, _, _ := ev.e.ReceiveDelivery(ctx, "del-2", "pull_request", mergedPayload(42)); status != engine.DeliveryRetry {
		t.Fatalf("1re réception: %s", status)
	}
	if err := ev.e.RetryDeliveries(ctx); err != nil {
		t.Fatal(err)
	}
	d, _ := store.GetDelivery(ctx, ev.s.Pool, "del-2")
	expect(t, "pas encore échue", d.Attempts, 1)
	// Redélivrance manuelle depuis GitHub (même X-GitHub-Delivery) : retraitée tout de suite.
	ev.g.prErr, ev.g.merged = nil, true
	status, _, err := ev.e.ReceiveDelivery(ctx, "del-2", "pull_request", mergedPayload(42))
	if err != nil || status != engine.DeliveryProcessed {
		t.Fatalf("redélivrance: %s %v", status, err)
	}
	expect(t, "ticket", ev.ticket(f.ID).State, string(domain.TicketDone))
	// Plafond : après MaxDeliveryAttempts échecs, plus de reprise automatique.
	ev.g.prErr = errors.New("panne durable")
	ev.e.Backoff = time.Millisecond
	if _, err := ev.s.Pool.Exec(ctx, `UPDATE tickets SET state='PR_OPENED', version=version+1, head_sha='x'`); err != nil {
		t.Fatal(err)
	}
	_, _, _ = ev.e.ReceiveDelivery(ctx, "del-3", "check_run", []byte(`{"repository":{"full_name":"o/r"},"check_run":{"head_sha":"x","check_suite":{"head_branch":"y"}}}`))
	for i := 0; i < engine.MaxDeliveryAttempts+2; i++ {
		_, _ = ev.s.Pool.Exec(ctx, `UPDATE github_deliveries SET next_attempt_at=now() WHERE delivery_id='del-3' AND next_attempt_at IS NOT NULL`)
		if err := ev.e.RetryDeliveries(ctx); err != nil {
			t.Fatal(err)
		}
	}
	d, _ = store.GetDelivery(ctx, ev.s.Pool, "del-3")
	if d.Status != "failed" || d.Attempts != engine.MaxDeliveryAttempts || d.NextAttemptAt != nil {
		t.Fatalf("plafond: %+v", d)
	}
}

func TestWebhookInvalidPayloadIsNotRetried(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	status, _, err := ev.e.ReceiveDelivery(ctx, "del-bad", "pull_request", []byte(`{"pull_request":"pas un objet"}`))
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "statut", status, engine.DeliveryFailed)
	d, _ := store.GetDelivery(ctx, ev.s.Pool, "del-bad")
	if d.Status != "failed" || d.NextAttemptAt != nil {
		t.Fatalf("livraison: %+v", d)
	}
}

// ------------------------------------------------------------------ développement séquentiel

const designTwo = "```json\n{\"summary\":\"d\",\"decisions\":[{\"decision\":\"Go\",\"rationale\":\"simple\",\"rejected_alternatives\":[\"Rust\"]}],\"risks\":[\"r1\"],\"tickets\":[" +
	"{\"title\":\"Ajouter hello\",\"body\":\"Créer hello.txt\",\"acceptance_criteria\":[\"hello.txt existe\"]}," +
	"{\"title\":\"Compléter hello\",\"body\":\"Modifier hello.txt\",\"acceptance_criteria\":[\"hello.txt existe\"]}]}\n```"

// toDevGateTwo amène une feature à deux tickets jusqu'au feu vert développement approuvé.
func (ev *env) toDevGateTwo() (*store.Feature, []store.Ticket) {
	ev.t.Helper()
	return ev.toDevGateTwoIn(ev.project(), "Hello")
}

func (ev *env) toDevGateTwoIn(p *store.Project, title string) (*store.Feature, []store.Ticket) {
	ev.t.Helper()
	ctx := context.Background()
	ev.design = designTwo
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, title, "Dire bonjour")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		ev.t.Fatal(err)
	}
	ev.drain()
	ev.decide(domain.ApprovalSpecReview, "approved", "")
	ev.drain()
	ev.decide(domain.ApprovalDesignReview, "approved", "")
	ev.drain()
	expect(ev.t, "feature", ev.feature(f.ID).State, "HUMAN_GATE")
	ev.decide(domain.ApprovalDevGate, "approved", "")
	// exécute uniquement le job durable de l'approbation (pas le développement)
	if did, err := ev.e.RunOnce(ctx, "test-worker"); err != nil || !did {
		ev.t.Fatalf("exécution de l'approbation: %v %v", did, err)
	}
	expect(ev.t, "approbation exécutée", ev.job(domain.JobExecuteApproval).Status, domain.JobSucceeded)
	ts, _ := store.ListTicketsByFeature(ctx, ev.s.Pool, f.ID)
	if len(ts) != 2 {
		ev.t.Fatalf("tickets: %d", len(ts))
	}
	return f, ts
}

func (ev *env) developJobs(ticketID int64) int {
	var n int
	if err := ev.s.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM jobs WHERE kind='develop' AND subject_id=$1 AND status<>'cancelled'`, ticketID).Scan(&n); err != nil {
		ev.t.Fatal(err)
	}
	return n
}

func (ev *env) ticketState(id int64) string {
	t, err := store.GetTicket(context.Background(), ev.s.Pool, id)
	if err != nil {
		ev.t.Fatal(err)
	}
	return t.State
}

func TestTicketsAreDevelopedSequentiallyPerProject(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, ts := ev.toDevGateTwo()
	t1, t2 := ts[0], ts[1]
	expect(t, "job develop T1", ev.developJobs(t1.ID), 1)
	expect(t, "pas de job develop T2 au feu vert", ev.developJobs(t2.ID), 0)
	expect(t, "T2 prêt", ev.ticketState(t2.ID), "READY_FOR_DEV")

	ev.drain()
	expect(t, "T1 après dev/review/qa", ev.ticketState(t1.ID), "AWAITING_PR_APPROVAL")
	expect(t, "T2 attend tant que T1 est en cours", ev.ticketState(t2.ID), "READY_FOR_DEV")
	expect(t, "toujours pas de job T2", ev.developJobs(t2.ID), 0)
	expect(t, "un seul run développeur", ev.calls["aos-developer"], 1)

	ev.decide(domain.ApprovalPublishPR, "approved", "")
	ev.drain()
	expect(t, "T1 PR ouverte", ev.ticketState(t1.ID), "PR_OPENED")
	expect(t, "T2 attend le merge de T1", ev.developJobs(t2.ID), 0)

	ev.g.merged = true
	if err := ev.e.ReconcilePR(ctx, t1.ID); err != nil {
		t.Fatal(err)
	}
	expect(t, "T1 mergé", ev.ticketState(t1.ID), "DONE")
	expect(t, "T2 démarre après le merge de T1", ev.developJobs(t2.ID), 1)
	ev.g.merged = false
	ev.drain()
	expect(t, "T2 développé", ev.ticketState(t2.ID), "AWAITING_PR_APPROVAL")
	expect(t, "deux runs développeur au total", ev.calls["aos-developer"], 2)
	expect(t, "feature", ev.feature(f.ID).State, "IN_DEV")
}

func TestCancelledTicketReleasesTheProject(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	_, ts := ev.toDevGateTwo()
	t1, t2 := ts[0], ts[1]
	expect(t, "T2 en attente", ev.developJobs(t2.ID), 0)
	if _, err := ev.e.TicketAction(ctx, t1.ID, "cancel", "admin"); err != nil {
		t.Fatal(err)
	}
	expect(t, "T1 annulé", ev.ticketState(t1.ID), "CANCELLED")
	expect(t, "job T1 annulé", ev.developJobs(t1.ID), 0)
	expect(t, "T2 démarre", ev.developJobs(t2.ID), 1)
	ev.drain()
	expect(t, "T2 développé", ev.ticketState(t2.ID), "AWAITING_PR_APPROVAL")
}

func TestBlockedTicketHoldsTheProject(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	_, ts := ev.toDevGateTwo()
	t1, t2 := ts[0], ts[1]
	// Le développeur ne commit rien : livrable refusé, T1 bloqué.
	ev.devHook = func(wt string) { gitRun(t, wt, "reset", "--hard", "HEAD~1") }
	ev.drain()
	expect(t, "T1 bloqué", ev.ticketState(t1.ID), "BLOCKED")
	expect(t, "T2 n'est pas lancé pendant le blocage", ev.developJobs(t2.ID), 0)
	ev.devHook = nil
	if _, err := ev.e.TicketAction(ctx, t1.ID, "retry", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	expect(t, "T1 repris", ev.ticketState(t1.ID), "AWAITING_PR_APPROVAL")
	expect(t, "T2 toujours en attente", ev.developJobs(t2.ID), 0)
}

func TestOtherFeatureWaitsForTheProject(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	_, a := ev.toDevGateTwoIn(p, "A")
	ev.drain()
	expect(t, "A-T1 en cours", ev.ticketState(a[0].ID), "AWAITING_PR_APPROVAL")
	// Une seconde feature du même projet reçoit son feu vert pendant que A-T1 est en cours.
	_, b := ev.toDevGateTwoIn(p, "B")
	expect(t, "A-T2 attend", ev.developJobs(a[1].ID), 0)
	expect(t, "B-T1 attend", ev.developJobs(b[0].ID), 0)
	expect(t, "B-T2 attend", ev.developJobs(b[1].ID), 0)
	_ = ctx
}

// ------------------------------------------------------------------ corps de PR

func TestPRBodyIsShortAndTicketFocused(t *testing.T) {
	ev := newEnv(t)
	long := strings.Repeat("sortie de commande très verbeuse ", 60)
	ev.design = strings.Replace(designOK, `"risks":["r1"]`, `"risks":["risque de la feature `+strings.Repeat("x", 300)+`"]`, 1)
	ev.qa = func(sha string, _ int) string {
		return fmt.Sprintf("```json\n{\"verdict\":\"passed\",\"sha\":%q,\"checks\":[{\"command\":\"go test ./...\",\"exit_code\":0}],"+
			"\"acceptance\":[{\"criterion\":\"hello.txt existe\",\"status\":\"met\",\"evidence\":%q}],\"summary\":\"ok\"}\n```", sha, long)
	}
	ev.startToDev()
	ev.drain()
	ev.decide(domain.ApprovalPublishPR, "approved", "")
	ev.drain()
	body := ev.g.prBody
	for _, want := range []string{"## Ticket", "## Vérifications", "Revue : **approved** · QA : **passed**", "1/1 couverts",
		"`go test ./...` → exit 0", "<details>", "Closes #"} {
		if !strings.Contains(body, want) {
			t.Fatalf("corps sans %q :\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"Décisions techniques", "Risques", "risque de la feature", long[:200]} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("corps contient %q (design/risques/preuve brute) :\n%s", unwanted, body)
		}
	}
	if len([]rune(body)) > 1500 {
		t.Fatalf("corps trop long : %d caractères", len([]rune(body)))
	}
}

func TestProjectAgentProfilesOverrideDefaultProfiles(t *testing.T) {
	ev := newEnv(t)
	ev.agentProfiles = map[string]string{"developer": "aos-demo-developer", "reviewer": "aos-demo-reviewer", "qa": "aos-demo-qa"}
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "ticket après dev/review/qa", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")
	rows, err := ev.s.Pool.Query(context.Background(), `SELECT agent_role, hermes_profile FROM runs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var role, prof string
		if err := rows.Scan(&role, &prof); err != nil {
			t.Fatal(err)
		}
		got[role] = prof
	}
	want := map[string]string{"po": "aos-po", "tech_lead": "aos-tech-lead", "developer": "aos-demo-developer",
		"reviewer": "aos-demo-reviewer", "qa": "aos-demo-qa"}
	for role, prof := range want {
		if got[role] != prof {
			t.Fatalf("rôle %s : profil %q, attendu %q (%v)", role, got[role], prof, got)
		}
	}
	for _, prof := range []string{"aos-developer", "aos-reviewer", "aos-qa"} {
		if ev.calls[prof] != 0 {
			t.Fatalf("profil par défaut %s appelé %d fois", prof, ev.calls[prof])
		}
	}
	profiles, err := store.ProjectAgentProfiles(context.Background(), ev.s.Pool)
	if err != nil || strings.Join(profiles, ",") != "aos-demo-developer,aos-demo-qa,aos-demo-reviewer" {
		t.Fatalf("profils des projets : %v %v", profiles, err)
	}
}

func TestNotificationsGoToProjectChannel(t *testing.T) {
	ev := newEnv(t)
	ev.telegramChat, ev.telegramThread = "-1004205411589", 13
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "ticket", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")
	ev.n.mu.Lock()
	defer ev.n.mu.Unlock()
	if len(ev.n.dests) < 3 {
		t.Fatalf("notifications attendues (spec, conception, issues, PR) : %v", ev.n.msgs)
	}
	for i, d := range ev.n.dests {
		if d.ChatID != "-1004205411589" || d.ThreadID != 13 || d.Label != "Demo" {
			t.Fatalf("notification %d « %s » : destination %+v", i, ev.n.msgs[i], d)
		}
	}
}

func TestNotificationsWithoutChannelCarryProjectLabel(t *testing.T) {
	ev := newEnv(t)
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "ticket", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")
	ev.n.mu.Lock()
	defer ev.n.mu.Unlock()
	for i, d := range ev.n.dests {
		if d.ChatID != "" || d.ThreadID != 0 || d.Label != "Demo" {
			t.Fatalf("notification %d : destination %+v", i, d)
		}
	}
}

// runRow lit les colonnes de modèle d'un run par rôle.
func (ev *env) runRow(role string) (reqModel, reqProvider, served, servedProvider string, fallback bool, status string) {
	ev.t.Helper()
	var rm, rp, m, p *string
	err := ev.s.Pool.QueryRow(context.Background(), `SELECT requested_model, requested_provider, model, provider, model_fallback, status
		FROM runs WHERE agent_role=$1 ORDER BY id DESC LIMIT 1`, role).Scan(&rm, &rp, &m, &p, &fallback, &status)
	if err != nil {
		ev.t.Fatal(err)
	}
	deref := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return deref(rm), deref(rp), deref(m), deref(p), fallback, status
}

func TestModelResolutionProjectThenAgentThenDefault(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	// Niveau 1 : le projet impose fournisseur + modèle au développeur.
	ev.agentModels = map[string]domain.ModelChoice{domain.RoleDeveloper: {Provider: "anthropic", Model: "claude-sonnet-5"}}
	// Niveau 2 : l'agent reviewer a un modèle souhaité global (sans fournisseur) ; le projet impose aussi
	// un modèle au PO pour vérifier que le projet prime sur l'agent.
	for role, desired := range map[string]string{domain.RoleReviewer: "gpt-9", domain.RolePO: "gpt-vieux"} {
		a, _ := store.GetAgent(ctx, ev.s.Pool, role)
		a.DesiredModel = desired
		if _, err := store.UpdateAgent(ctx, ev.s.Pool, a); err != nil {
			t.Fatal(err)
		}
	}
	ev.agentModels[domain.RolePO] = domain.ModelChoice{Provider: "p", Model: "m2"}
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "ticket après dev/review/qa", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")

	// Le corps envoyé à Hermes : surcharge présente seulement quand une valeur est résolue.
	rows, err := ev.s.Pool.Query(ctx, `SELECT agent_role, requested_model, requested_provider, model_fallback FROM runs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]string{}
	for rows.Next() {
		var role string
		var m, p *string
		var fb bool
		if err := rows.Scan(&role, &m, &p, &fb); err != nil {
			t.Fatal(err)
		}
		if fb {
			t.Fatalf("aucun repli attendu (%s)", role)
		}
		pair := [2]string{}
		if m != nil {
			pair[0] = *m
		}
		if p != nil {
			pair[1] = *p
		}
		got[role] = pair
	}
	want := map[string][2]string{
		domain.RolePO:        {"m2", "p"},                      // projet prime sur l'agent
		domain.RoleDeveloper: {"claude-sonnet-5", "anthropic"}, // projet
		domain.RoleReviewer:  {"gpt-9", ""},                    // agent, sans fournisseur
		domain.RoleQA:        {"", ""},                         // défaut du profil
		domain.RoleTechLead:  {"", ""},
	}
	for role, w := range want {
		if got[role] != w {
			t.Fatalf("rôle %s : requested %v, attendu %v (%v)", role, got[role], w, got)
		}
	}
	// Et dans les corps réellement soumis à Hermes.
	ev.h.mu.Lock()
	defer ev.h.mu.Unlock()
	seen := map[[2]string]int{}
	for _, req := range ev.h.reqs {
		seen[[2]string{req.Model, req.Provider}]++
	}
	if seen[[2]string{"claude-sonnet-5", "anthropic"}] != 1 || seen[[2]string{"gpt-9", ""}] != 1 || seen[[2]string{"m2", "p"}] != 1 || seen[[2]string{"", ""}] != 2 {
		t.Fatalf("corps envoyés à Hermes : %v", seen)
	}
}

func TestModelFallbackOnceWithAudit(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	ev.agentModels = map[string]domain.ModelChoice{domain.RoleDeveloper: {Provider: "inconnu", Model: "x-1"}}
	// Hermes refuse tout run demandant le fournisseur « inconnu » ; sans surcharge, il sert m1.
	ev.h.rejectProvider = func(req hermes.RunRequest) bool { return req.Provider == "inconnu" }
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "ticket après dev/review/qa", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")
	rm, rp, served, sp, fb, status := ev.runRow(domain.RoleDeveloper)
	if rm != "x-1" || rp != "inconnu" || served != "m1" || sp != "p" || !fb || status != domain.RunCompleted {
		t.Fatalf("run développeur : requested %s/%s servi %s/%s repli=%v statut=%s", rp, rm, sp, served, fb, status)
	}
	expect(t, "deux soumissions développeur (initiale + repli)", ev.calls["aos-developer"], 1) // respond n'est appelé qu'au repli
	ev.h.mu.Lock()
	fallbackKeys, withOverride := 0, 0
	for key, req := range ev.h.reqs {
		if strings.HasSuffix(key, "-fallback") {
			fallbackKeys++
			if req.Model != "" || req.Provider != "" {
				t.Fatalf("le repli doit être soumis sans surcharge : %+v", req)
			}
		}
		if req.Provider == "inconnu" {
			withOverride++
		}
	}
	ev.h.mu.Unlock()
	if fallbackKeys != 1 || withOverride != 1 {
		t.Fatalf("clés de repli %d, soumissions avec surcharge %d", fallbackKeys, withOverride)
	}
	// Un seul run (rattaché à sa re-soumission), une entrée d'audit système.
	var runs int
	_ = ev.s.Pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE agent_role='developer'`).Scan(&runs)
	expect(t, "un seul run développeur", runs, 1)
	var actor, after string
	if err := ev.s.Pool.QueryRow(ctx, `SELECT actor, after::text FROM audit_log WHERE action=$1`, engine.AuditRunModelFallback).Scan(&actor, &after); err != nil {
		t.Fatalf("audit run.model_fallback absent : %v", err)
	}
	var entry struct {
		Requested map[string]string `json:"requested"`
		Served    string            `json:"served"`
		Reason    string            `json:"reason"`
	}
	if err := json.Unmarshal([]byte(after), &entry); err != nil {
		t.Fatal(err)
	}
	if actor != "system" || entry.Requested["provider"] != "inconnu" || entry.Requested["model"] != "x-1" || entry.Served == "" ||
		!strings.Contains(entry.Reason, "fournisseur") {
		t.Fatalf("audit : %s %s", actor, after)
	}
}

func TestModelFallbackHappensOnlyOnce(t *testing.T) {
	ev := newEnv(t)
	ev.agentModels = map[string]domain.ModelChoice{domain.RolePO: {Provider: "p", Model: "m2"}}
	// Hermes échoue même sans surcharge (panne du fournisseur) : pas de second repli, le run échoue.
	ev.h.rejectProvider = func(hermes.RunRequest) bool { return true }
	ctx := context.Background()
	p := ev.project()
	f, err := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Hello", "Dire bonjour")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	ev.h.mu.Lock()
	creates := ev.h.creates
	ev.h.mu.Unlock()
	expect(t, "soumissions Hermes (initiale + un seul repli)", creates, 2)
	_, _, _, _, fb, status := ev.runRow(domain.RolePO)
	if !fb || status != domain.RunFailed {
		t.Fatalf("run PO : repli=%v statut=%s", fb, status)
	}
	expect(t, "feature bloquée", ev.feature(f.ID).State, "BLOCKED")
}

func TestReworkWithoutChangeReReviewsSameCommit(t *testing.T) {
	ev := newEnv(t)
	ev.qa = func(sha string, call int) string {
		if call == 1 {
			return qaJSON("failed", sha) // échec dû à l'environnement de vérification
		}
		return qaJSON("passed", sha)
	}
	f, _ := ev.startToDev()
	ev.drain()
	tk := ev.ticket(f.ID)
	expect(t, "après QA en échec", tk.State, "CHANGES_REQUESTED")
	sha := *tk.HeadSHA
	ev.devNoChange = true
	ev.decide(domain.ApprovalRework, "approved", "")
	ev.drain()
	tk = ev.ticket(f.ID)
	expect(t, "même commit revu puis validé", tk.State, "AWAITING_PR_APPROVAL")
	expect(t, "SHA inchangé", *tk.HeadSHA, sha)
	expect(t, "revues", ev.calls["aos-reviewer"], 2)
	expect(t, "QA", ev.calls["aos-qa"], 2)
	var reason string
	_ = ev.s.Pool.QueryRow(context.Background(), `SELECT after->>'reason' FROM audit_log WHERE subject_type='ticket' AND subject_id=$1
		AND action='ticket.dev_pushed' ORDER BY id DESC LIMIT 1`, tk.ID).Scan(&reason)
	if !strings.Contains(reason, "nouvelle revue du même commit") {
		t.Fatalf("raison auditée : %q", reason)
	}
}

func TestReworkClaimingChangesWithoutCommitIsRefused(t *testing.T) {
	ev := newEnv(t)
	ev.qa = func(sha string, _ int) string { return qaJSON("failed", sha) }
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "après QA en échec", ev.ticket(f.ID).State, "CHANGES_REQUESTED")
	// Le développeur annonce hello.txt mais ne commit rien : refus, comme avant.
	ev.devHook = func(wt string) { gitRun(t, wt, "reset", "--hard", "HEAD~1") }
	ev.decide(domain.ApprovalRework, "approved", "")
	ev.drain()
	tk := ev.ticket(f.ID)
	expect(t, "bloqué", tk.State, "BLOCKED")
	if !strings.Contains(tk.StateReason, "aucun nouveau commit depuis le dernier push") {
		t.Fatalf("raison : %v", tk.StateReason)
	}
}
