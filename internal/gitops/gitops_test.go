package gitops

import (
	"context"
	"errors"
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

func TestAskpassScriptReadsTokenFile(t *testing.T) {
	dir := t.TempDir()
	tok := filepath.Join(dir, "github_token")
	_ = os.WriteFile(tok, []byte("ghs_SECRET"), 0o600)
	m := &Manager{TokenFile: tok}
	env, cleanup, err := m.authEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || strings.Contains(env[0], "ghs_SECRET") {
		t.Fatalf("env inattendu")
	}
	script := strings.TrimPrefix(env[0], "GIT_ASKPASS=")
	st, err := os.Stat(script)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("permissions du script: %v", err)
	}
	out, err := exec.Command(script, "Password for 'https://x-access-token@github.com': ").Output()
	if err != nil || strings.TrimSpace(string(out)) != "ghs_SECRET" {
		t.Fatalf("mot de passe: %q %v", out, err)
	}
	out, _ = exec.Command(script, "Username for 'https://github.com': ").Output()
	if strings.TrimSpace(string(out)) != "x-access-token" {
		t.Fatalf("utilisateur: %q", out)
	}
	b, _ := os.ReadFile(script)
	if strings.Contains(string(b), "ghs_SECRET") {
		t.Fatal("token écrit dans le script")
	}
	cleanup()
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatal("script non supprimé")
	}
	if _, _, err := (&Manager{TokenFile: "/tmp/it's"}).authEnv(); err == nil {
		t.Fatal("chemin avec quote accepté")
	}
}
