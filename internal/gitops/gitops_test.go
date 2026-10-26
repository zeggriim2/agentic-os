package gitops

import (
	"context"
	"errors"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const gitBin = "/usr/bin/git"

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBin, append([]string{"-c", "user.name=Test", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setupRemote crée un dépôt bare "distant" avec un commit initial sur main.
func setupRemote(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "remote.git")
	run(t, base, "init", "--bare", bare)
	seed := filepath.Join(base, "seed")
	run(t, base, "clone", bare, seed)
	if err := os.MkdirAll(filepath.Join(seed, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(seed, "main.go"), []byte("package main\n"), 0o644)
	_ = os.WriteFile(filepath.Join(seed, "main_test.go"), []byte("package main\n"), 0o644)
	_ = os.WriteFile(filepath.Join(seed, "tests", "e2e.sh"), []byte("true\n"), 0o644)
	run(t, seed, "add", "-A")
	run(t, seed, "commit", "-m", "init")
	run(t, seed, "push", "origin", "HEAD:refs/heads/main")
	return bare, base
}

func newManager(t *testing.T, bare string) *Manager {
	return &Manager{
		Git:       Runner{GitPath: gitBin, Timeout: time.Minute},
		Root:      filepath.Join(t.TempDir(), "ws"),
		RemoteURL: func(string) string { return bare },
	}
}

func commitAll(t *testing.T, dir, msg string) {
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-m", msg)
}

func TestWorktreeCleanPushAndSHA(t *testing.T) {
	bare, _ := setupRemote(t)
	m := newManager(t, bare)
	ctx := context.Background()
	wt, err := m.EnsureBranchWorktree(ctx, "demo", "o/r", "demo-F1-T1", "aos/demo-F1-T1", "main")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(m.Root, "demo", "demo-F1-T1"); wt != want {
		t.Fatalf("chemin %s ≠ %s", wt, want)
	}
	if rel := m.Relative(wt); rel != "demo/demo-F1-T1" {
		t.Fatalf("relatif: %s", rel)
	}
	br, err := m.CurrentBranch(ctx, wt)
	if err != nil || br != "aos/demo-F1-T1" {
		t.Fatalf("branche: %s %v", br, err)
	}
	if err := m.EnsureClean(ctx, wt); err != nil {
		t.Fatal(err)
	}
	// fichier non suivi → sale
	_ = os.WriteFile(filepath.Join(wt, "nouveau.txt"), []byte("x"), 0o644)
	var de *DirtyError
	if err := m.EnsureClean(ctx, wt); !errors.As(err, &de) {
		t.Fatalf("fichier non suivi non détecté: %v", err)
	}
	commitAll(t, wt, "feat")
	if err := m.EnsureClean(ctx, wt); err != nil {
		t.Fatal(err)
	}
	// modification suivie → sale
	_ = os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main // x\n"), 0o644)
	if err := m.EnsureClean(ctx, wt); err == nil {
		t.Fatal("modification non détectée")
	}
	run(t, wt, "checkout", "--", "main.go")

	head, _ := m.HeadSHA(ctx, wt)
	baseSHA, _ := m.RevParse(ctx, wt, "refs/remotes/origin/main")
	if head == baseSHA || len(head) != 40 {
		t.Fatal("HEAD devrait différer de la base")
	}
	if err := m.Push(ctx, wt, "aos/demo-F1-T1"); err != nil {
		t.Fatal(err)
	}
	clone, _ := m.ClonePath("demo")
	remote, err := m.RemoteBranchSHA(ctx, clone, "aos/demo-F1-T1")
	if err != nil || remote != head {
		t.Fatalf("SHA distant %s ≠ %s (%v)", remote, head, err)
	}
	// réutilisation idempotente du worktree
	wt2, err := m.EnsureBranchWorktree(ctx, "demo", "o/r", "demo-F1-T1", "aos/demo-F1-T1", "main")
	if err != nil || wt2 != wt {
		t.Fatalf("réutilisation: %v", err)
	}
	// worktree détaché au SHA exact
	det, err := m.EnsureDetachedWorktree(ctx, "demo", "o/r", "demo-F1-T1-review", "aos/demo-F1-T1", head)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := m.HeadSHA(ctx, det); h != head {
		t.Fatalf("détaché à %s ≠ %s", h, head)
	}
	if _, err := m.CurrentBranch(ctx, det); err == nil {
		t.Fatal("HEAD détachée attendue")
	}
	// recréation
	if _, err := m.EnsureDetachedWorktree(ctx, "demo", "o/r", "demo-F1-T1-review", "aos/demo-F1-T1", head); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveWorktree(ctx, "demo", "demo-F1-T1-review"); err != nil {
		t.Fatal(err)
	}
}

func TestDeletedTestFilesDetected(t *testing.T) {
	bare, _ := setupRemote(t)
	m := newManager(t, bare)
	ctx := context.Background()
	wt, err := m.EnsureBranchWorktree(ctx, "demo", "o/r", "demo-F1-T2", "aos/demo-F1-T2", "main")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(wt, "main_test.go"))
	_ = os.Rename(filepath.Join(wt, "tests", "e2e.sh"), filepath.Join(wt, "e2e.sh"))
	_ = os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main\n// v2\n"), 0o644)
	commitAll(t, wt, "suppression")
	del, err := m.DeletedTestFiles(ctx, wt, "refs/remotes/origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if len(del) != 2 {
		t.Fatalf("suppressions de tests attendues 2, obtenu %v", del)
	}
}

func TestPushRefusesNonFastForward(t *testing.T) {
	bare, base := setupRemote(t)
	m := newManager(t, bare)
	ctx := context.Background()
	wt, err := m.EnsureBranchWorktree(ctx, "demo", "o/r", "demo-F1-T3", "aos/demo-F1-T3", "main")
	if err != nil {
		t.Fatal(err)
	}
	// quelqu'un d'autre pousse sur la même branche
	other := filepath.Join(base, "other")
	run(t, base, "clone", bare, other)
	run(t, other, "checkout", "-b", "aos/demo-F1-T3")
	_ = os.WriteFile(filepath.Join(other, "autre.txt"), []byte("x"), 0o644)
	commitAll(t, other, "autre")
	run(t, other, "push", "origin", "aos/demo-F1-T3")

	_ = os.WriteFile(filepath.Join(wt, "local.txt"), []byte("y"), 0o644)
	commitAll(t, wt, "local")
	if err := m.Push(ctx, wt, "aos/demo-F1-T3"); !errors.Is(err, ErrNonFastForward) {
		t.Fatalf("non fast-forward attendu: %v", err)
	}
}

func TestPathValidationAndBinary(t *testing.T) {
	m := &Manager{Root: "/tmp/ws", Git: Runner{GitPath: gitBin}}
	for _, name := range []string{"..", "../x", "a/b", "", "_repo.git"} {
		if _, err := m.WorktreePath("demo", name); err == nil {
			t.Errorf("%q accepté", name)
		}
	}
	if _, err := m.WorktreePath("../demo", "x"); err == nil {
		t.Error("slug invalide accepté")
	}
	r := Runner{GitPath: "git"}
	if _, err := r.Run(context.Background(), "/", nil, "version"); err == nil {
		t.Fatal("binaire relatif accepté")
	}
	if !IsTestPath("pkg/a_test.go") || !IsTestPath("tests/x.py") || !IsTestPath("a/test/b.js") || IsTestPath("src/contest.go") || IsTestPath("latest/x.go") {
		t.Fatal("IsTestPath incorrect")
	}
}

// authGitServer sert des dépôts bare en HTTP « smart » (git http-backend) et exige
// l'authentification Basic x-access-token:<token>, comme GitHub.
func authGitServer(t *testing.T, root, token string) *httptest.Server {
	t.Helper()
	backend := filepath.Join(strings.TrimSpace(run(t, root, "--exec-path")), "git-http-backend")
	h := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "x-access-token" || p != token {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		r.Header.Set("REMOTE_USER", u)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Le token passe par un credential helper shell : pas de fichier temporaire exécutable
// (fonctionne avec /tmp en noexec, cas du conteneur), token absent de l'argv.
func TestAuthenticatedCloneAndPushWithoutTempFiles(t *testing.T) {
	if _, err := os.Stat(gitBin); err != nil {
		t.Skip("git absent")
	}
	base := t.TempDir()
	srvRoot := filepath.Join(base, "srv")
	_ = os.MkdirAll(srvRoot, 0o755)
	seed := filepath.Join(base, "seed")
	_ = os.MkdirAll(seed, 0o755)
	run(t, seed, "init", "-q")
	_ = os.WriteFile(filepath.Join(seed, "a.txt"), []byte("a\n"), 0o644)
	run(t, seed, "add", ".")
	run(t, seed, "commit", "-q", "-m", "init")
	run(t, base, "clone", "-q", "--bare", seed, filepath.Join(srvRoot, "o", "r.git"))
	run(t, filepath.Join(srvRoot, "o", "r.git"), "config", "http.receivepack", "true")

	srv := authGitServer(t, srvRoot, "ghs_SECRET")
	tokFile := filepath.Join(base, "github_token")
	_ = os.WriteFile(tokFile, []byte("ghs_SECRET\n"), 0o600)

	// TMPDIR non inscriptible : aucun fichier temporaire ne doit être nécessaire.
	ro := filepath.Join(base, "ro-tmp")
	_ = os.MkdirAll(ro, 0o500)
	t.Setenv("TMPDIR", ro)

	m := &Manager{Root: filepath.Join(base, "ws"), Git: Runner{GitPath: gitBin, Timeout: time.Minute},
		RemoteURL: func(repo string) string { return srv.URL + "/" + repo + ".git" }, TokenFile: tokFile}
	h, err := m.credentialHelper()
	if err != nil || strings.Contains(h, "ghs_SECRET") {
		t.Fatalf("helper: %v (token dans l'argv ?)", err)
	}
	ctx := context.Background()
	wt, err := m.EnsureBranchWorktree(ctx, "demo", "o/r", "demo-F1-T1", "aos/demo-F1-T1", "main")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(wt, "b.txt"), []byte("b\n"), 0o644)
	run(t, wt, "add", ".")
	run(t, wt, "commit", "-q", "-m", "feat: b")
	if err := m.Push(ctx, wt, "aos/demo-F1-T1"); err != nil {
		t.Fatalf("push authentifié: %v", err)
	}
	if got := run(t, base, "--git-dir", filepath.Join(srvRoot, "o", "r.git"), "rev-parse", "refs/heads/aos/demo-F1-T1"); got != run(t, wt, "rev-parse", "HEAD") {
		t.Fatalf("branche distante %s", got)
	}
	if entries, _ := os.ReadDir(ro); len(entries) != 0 {
		t.Fatalf("fichiers temporaires créés: %v", entries)
	}

	// Mauvais token : refusé, et le message d'erreur ne contient pas le token.
	_ = os.WriteFile(tokFile, []byte("ghs_FAUX"), 0o600)
	_ = os.WriteFile(filepath.Join(wt, "c.txt"), []byte("c\n"), 0o644)
	run(t, wt, "add", ".")
	run(t, wt, "commit", "-q", "-m", "feat: c")
	err = m.Push(ctx, wt, "aos/demo-F1-T1")
	if err == nil || strings.Contains(err.Error(), "ghs_") {
		t.Fatalf("push avec mauvais token: %v", err)
	}
	if _, err := (&Manager{TokenFile: "/tmp/it's"}).credentialHelper(); err == nil {
		t.Fatal("chemin avec quote accepté")
	}
	if _, err := (&Manager{TokenFile: "relatif"}).credentialHelper(); err == nil {
		t.Fatal("chemin relatif accepté")
	}
}
