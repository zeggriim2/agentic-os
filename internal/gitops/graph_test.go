package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func allKinds() map[string]bool {
	m := map[string]bool{}
	for _, k := range AllKinds {
		m[k] = true
	}
	return m
}

// gitFlowRepo construit un dépôt « distant » GitFlow : main, develop, une feature mergée par PR,
// une branche de ticket aos/ active, une release taguée, et une vieille branche inactive.
func gitFlowRepo(t *testing.T) (remote string, old time.Time) {
	t.Helper()
	dir := t.TempDir()
	work := filepath.Join(dir, "work")
	remote = filepath.Join(dir, "remote.git")
	run(t, dir, "init", "-q", "--bare", remote)
	run(t, dir, "init", "-q", work)
	old = time.Now().Add(-90 * 24 * time.Hour)
	commit := func(msg string, when time.Time) {
		t.Helper()
		f := filepath.Join(work, "f.txt")
		b, _ := os.ReadFile(f)
		if err := os.WriteFile(f, append(b, msg+"\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, work, "add", "f.txt")
		d := when.Format(time.RFC3339)
		runEnv(t, work, []string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "commit", "-q", "-m", msg)
	}
	merge := func(branch, msg string, when time.Time) {
		t.Helper()
		d := when.Format(time.RFC3339)
		runEnv(t, work, []string{"GIT_AUTHOR_DATE=" + d, "GIT_COMMITTER_DATE=" + d}, "merge", "-q", "--no-ff", "-m", msg, branch)
	}
	now := time.Now()
	commit("init", old)
	run(t, work, "switch", "-q", "-c", "stale/experiment")
	commit("vieille expérience", old.Add(time.Hour))
	run(t, work, "switch", "-q", "main")
	run(t, work, "switch", "-q", "-c", "develop")
	commit("socle develop", now.Add(-10*24*time.Hour))
	run(t, work, "switch", "-q", "-c", "feature/export")
	commit("export csv", now.Add(-9*24*time.Hour))
	run(t, work, "switch", "-q", "develop")
	merge("feature/export", "Merge pull request #7 from o/feature/export", now.Add(-8*24*time.Hour))
	run(t, work, "switch", "-q", "-c", "release/1.0.0")
	commit("chore(release): 1.0.0 (#9)", now.Add(-7*24*time.Hour))
	run(t, work, "switch", "-q", "main")
	merge("release/1.0.0", "Merge pull request #9 from o/release/1.0.0", now.Add(-6*24*time.Hour))
	run(t, work, "tag", "-a", "v1.0.0", "-m", "1.0.0")
	run(t, work, "switch", "-q", "develop")
	// Branche mergée en squash : la pointe n'est pas un ancêtre de develop.
	run(t, work, "switch", "-q", "-c", "fix/squashed")
	if err := os.WriteFile(filepath.Join(work, "fix.txt"), []byte("correctif\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, work, "add", "fix.txt")
	d5 := now.Add(-5 * 24 * time.Hour).Format(time.RFC3339)
	runEnv(t, work, []string{"GIT_AUTHOR_DATE=" + d5, "GIT_COMMITTER_DATE=" + d5}, "commit", "-q", "-m", "correctif squashé")
	run(t, work, "switch", "-q", "develop")
	runEnv(t, work, nil, "merge", "-q", "--squash", "fix/squashed")
	d4 := now.Add(-4 * 24 * time.Hour).Format(time.RFC3339)
	runEnv(t, work, []string{"GIT_AUTHOR_DATE=" + d4, "GIT_COMMITTER_DATE=" + d4}, "commit", "-q", "-m", "fix: correctif (#11)")
	run(t, work, "switch", "-q", "-c", "aos/sandbox-F1-T1")
	commit("ticket T1", now.Add(-2*24*time.Hour))
	run(t, work, "push", "-q", "--all", remote)
	run(t, work, "push", "-q", "--tags", remote)
	return remote, old
}

func runEnv(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	c := exec.Command(gitBin, append([]string{"-c", "user.name=Test", "-c", "user.email=t@example.com"}, args...)...)
	c.Dir = dir
	c.Env = append([]string{"PATH=/usr/bin:/bin", "HOME=" + dir, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}, env...)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func graphManager(t *testing.T, remote string) *Manager {
	return &Manager{Git: Runner{GitPath: gitBin, Timeout: time.Minute}, Root: t.TempDir(),
		RemoteURL: func(string) string { return remote }}
}

func TestGraphGitFlow(t *testing.T) {
	remote, _ := gitFlowRepo(t)
	m := graphManager(t, remote)
	g, err := m.Graph(context.Background(), "sandbox", "o/r", GraphQuery{Days: 30, Kinds: allKinds(), BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]string{}
	for _, r := range g.Refs {
		refs[r.Name] = r.Kind
	}
	// feature/export et release/1.0.0 sont mergées : masquées par défaut.
	want := map[string]string{"main": KindMain, "develop": KindDevelop, "aos/sandbox-F1-T1": KindFeature}
	if len(g.Refs) != 3 || g.HiddenMerged != 3 {
		t.Fatalf("branches actives : %v (masquées %d)", g.Refs, g.HiddenMerged)
	}
	for n, k := range want {
		if refs[n] != k {
			t.Fatalf("branche %s : type %q, attendu %q (refs %v)", n, refs[n], k, refs)
		}
	}
	if _, ok := refs["stale/experiment"]; ok {
		t.Fatal("branche inactive depuis 90 jours affichée")
	}
	if g.Refs[0].Name != "main" || g.Refs[1].Name != "develop" {
		t.Fatalf("ordre des branches : %v", g.Refs)
	}
	by := map[string]GraphCommit{}
	pos := map[string]int{}
	for i, c := range g.Commits {
		by[c.Subject] = c
		pos[c.SHA] = i
	}
	// Ordre topologique : un enfant précède toujours ses parents présents.
	for i, c := range g.Commits {
		for _, p := range c.Parents {
			if j, ok := pos[p]; ok && j < i {
				t.Fatalf("parent %s avant son enfant %s", p[:7], c.SHA[:7])
			}
		}
	}
	// Les commits des branches mergées restent visibles via main/develop.
	if c := by["Merge pull request #9 from o/release/1.0.0"]; c.Branch != "main" || c.PR != 9 || len(c.Parents) != 2 {
		t.Fatalf("merge de release : %+v", c)
	}
	if c := by["Merge pull request #7 from o/feature/export"]; c.Branch != "develop" || c.PR != 7 {
		t.Fatalf("merge de feature : %+v", c)
	}
	if c := by["chore(release): 1.0.0 (#9)"]; c.SHA == "" || c.PR != 9 {
		t.Fatalf("commit de release : %+v", c)
	}
	if c := by["ticket T1"]; c.Branch != "aos/sandbox-F1-T1" || c.Kind != KindFeature {
		t.Fatalf("commit de ticket : %+v", c)
	}
	if c := by["init"]; c.SHA != "" {
		t.Fatal("commit hors fenêtre inclus alors qu'il n'est la pointe d'aucune branche")
	}
	if len(g.Tags) != 1 || g.Tags[0].Name != "v1.0.0" || g.Tags[0].SHA != by["Merge pull request #9 from o/release/1.0.0"].SHA {
		t.Fatalf("tag annoté non résolu sur le commit de merge : %+v", g.Tags)
	}
	if g.Truncated {
		t.Fatal("tronqué à tort")
	}
	g, err = m.Graph(context.Background(), "sandbox", "o/r", GraphQuery{Days: 30, Kinds: allKinds(), BaseBranch: "main", IncludeMerged: true})
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]GraphRef{}
	for _, r := range g.Refs {
		all[r.Name] = r
	}
	if r := all["release/1.0.0"]; r.Kind != KindRelease || !r.Merged {
		t.Fatalf("release mergée : %+v", r)
	}
	if r := all["feature/export"]; r.Kind != KindFeature || !r.Merged {
		t.Fatalf("feature mergée : %+v", r)
	}
	if r := all["fix/squashed"]; !r.Merged {
		t.Fatalf("branche mergée en squash non détectée : %+v", r)
	}
	if all["aos/sandbox-F1-T1"].Merged || all["main"].Merged || g.HiddenMerged != 0 {
		t.Fatalf("drapeaux merged : %+v", g.Refs)
	}
	for _, c := range g.Commits {
		if c.Subject == "chore(release): 1.0.0 (#9)" && c.Kind != KindRelease {
			t.Fatalf("commit de release attribué à %s alors que la branche est affichée", c.Branch)
		}
	}
	// La copie du moteur n'est pas touchée.
	if exists(filepath.Join(m.Root, "sandbox", "_repo.git")) {
		t.Fatal("la lecture du graphe a créé _repo.git")
	}
}

func TestGraphFiltersAndLimits(t *testing.T) {
	remote, _ := gitFlowRepo(t)
	m := graphManager(t, remote)
	ctx := context.Background()

	g, err := m.Graph(ctx, "sandbox", "o/r", GraphQuery{Days: 30, Kinds: map[string]bool{KindMain: true, KindDevelop: true}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range g.Refs {
		if r.Kind != KindMain && r.Kind != KindDevelop {
			t.Fatalf("branche %s non filtrée", r.Name)
		}
	}
	for _, c := range g.Commits {
		if c.Subject == "ticket T1" {
			t.Fatal("commit d'une branche filtrée présent")
		}
	}

	// Fenêtre courte : main (dernier commit il y a 6 jours) reste visible par sa pointe.
	g, err = m.Graph(ctx, "sandbox", "o/r", GraphQuery{Days: 1, Kinds: allKinds()})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, r := range g.Refs {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "main,develop" {
		t.Fatalf("branches actives sur 1 jour : %v", names)
	}
	if len(g.Commits) != 2 {
		t.Fatalf("pointes de main et develop attendues, reçu %d commits", len(g.Commits))
	}

	g, err = m.Graph(ctx, "sandbox", "o/r", GraphQuery{Days: 30, Kinds: allKinds(), MaxCommits: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Truncated {
		t.Fatal("troncature non signalée")
	}

	// Seconde lecture : la copie existante est mise à jour (branche supprimée à distance élaguée).
	run(t, filepath.Dir(remote), "--git-dir="+remote, "branch", "-D", "feature/export")
	g, err = m.Graph(ctx, "sandbox", "o/r", GraphQuery{Days: 30, Kinds: allKinds(), IncludeMerged: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range g.Refs {
		if r.Name == "feature/export" {
			t.Fatal("branche supprimée à distance toujours affichée")
		}
	}
}

func TestGraphRejectsUnsafeInput(t *testing.T) {
	m := graphManager(t, "/nonexistent")
	if _, err := m.Graph(context.Background(), "../x", "o/r", GraphQuery{Kinds: allKinds()}); err == nil {
		t.Fatal("slug dangereux accepté")
	}
	if _, err := m.Graph(context.Background(), "ok", "pas un dépôt", GraphQuery{Kinds: allKinds()}); err == nil {
		t.Fatal("dépôt invalide accepté")
	}
}

func TestBranchKindAndPR(t *testing.T) {
	cases := map[string]string{"main": KindMain, "master": KindMain, "trunk": KindOther, "develop": KindDevelop,
		"release/1.0.1": KindRelease, "hotfix/1.0.2": KindHotfix, "aos/p-F1-T1": KindFeature, "fix/x": KindFeature,
		"chore/back-merge-1.0.1": KindFeature, "wip": KindOther}
	for n, k := range cases {
		if got := BranchKind(n, "main"); got != k {
			t.Fatalf("%s : %s, attendu %s", n, got, k)
		}
	}
	if BranchKind("trunk", "trunk") != KindMain {
		t.Fatal("branche de base du projet non reconnue comme principale")
	}
	for s, n := range map[string]int{"Merge pull request #12 from o/b": 12, "feat: x (#20)": 20, "fix #3 typo": 0, "Merge branch 'main'": 0} {
		if got := PRFromSubject(s); got != n {
			t.Fatalf("%q : %d, attendu %d", s, got, n)
		}
	}
}
