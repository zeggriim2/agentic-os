package gitops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

// Types de branches du graphe GitFlow.
const (
	KindMain    = "main"
	KindDevelop = "develop"
	KindFeature = "feature"
	KindRelease = "release"
	KindHotfix  = "hotfix"
	KindOther   = "other"
)

// AllKinds liste les types dans l'ordre de priorité d'attribution des commits.
var AllKinds = []string{KindMain, KindDevelop, KindRelease, KindHotfix, KindFeature, KindOther}

var featurePrefixes = []string{"feature/", "feat/", "fix/", "bugfix/", "aos/", "chore/", "ci/", "docs/", "test/", "tests/", "ops/", "refactor/", "perf/", "build/"}

// BranchKind classe une branche selon les conventions GitFlow ; base est la branche de base du projet.
func BranchKind(name, base string) string {
	switch {
	case name == "main" || name == "master" || (base != "" && name == base && base != "develop"):
		return KindMain
	case name == "develop" || name == "dev":
		return KindDevelop
	case strings.HasPrefix(name, "release/"):
		return KindRelease
	case strings.HasPrefix(name, "hotfix/"):
		return KindHotfix
	}
	for _, p := range featurePrefixes {
		if strings.HasPrefix(name, p) {
			return KindFeature
		}
	}
	return KindOther
}

// GraphQuery paramètre la lecture du graphe.
type GraphQuery struct {
	Days       int             // fenêtre : commits et branches actives des N derniers jours
	Kinds      map[string]bool // types de branches à inclure
	MaxCommits int
	BaseBranch string
	// IncludeMerged : inclure les branches déjà intégrées à main/develop (non supprimées).
	IncludeMerged bool
	Now           time.Time
}

// GraphRef est une branche distante retenue.
type GraphRef struct {
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	SHA    string    `json:"sha"`
	Date   time.Time `json:"date"`
	Merged bool      `json:"merged"` // pointe déjà contenue dans main ou develop
}

// GraphTag est un tag pointant sur un commit du graphe.
type GraphTag struct {
	Name string `json:"name"`
	SHA  string `json:"sha"`
}

// GraphCommit est un commit du graphe, dans l'ordre topologique (enfants avant parents).
type GraphCommit struct {
	SHA     string    `json:"sha"`
	Parents []string  `json:"parents"`
	Date    time.Time `json:"date"`
	Author  string    `json:"author"`
	Subject string    `json:"subject"`
	Branch  string    `json:"branch"` // branche à laquelle le commit est attribué (premier parent)
	Kind    string    `json:"kind"`
	PR      int       `json:"pr,omitempty"` // PR déduite du message (merge ou squash GitHub)
}

// Graph est le résultat d'une lecture.
type Graph struct {
	Since     time.Time     `json:"since"`
	Refs      []GraphRef    `json:"refs"`
	Tags      []GraphTag    `json:"tags"`
	Commits   []GraphCommit `json:"commits"`
	Truncated bool          `json:"truncated"`
	// HiddenMerged : branches mergées masquées (IncludeMerged=false).
	HiddenMerged int `json:"hidden_merged"`
}

var (
	prMergeRe  = regexp.MustCompile(`^Merge pull request #(\d+)\b`)
	prSquashRe = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	graphMu    sync.Mutex
)

// PRFromSubject extrait le numéro de PR d'un message de merge ou de squash GitHub.
func PRFromSubject(s string) int {
	for _, re := range []*regexp.Regexp{prMergeRe, prSquashRe} {
		if m := re.FindStringSubmatch(s); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// graphClonePath : copie bare dédiée à la lecture du graphe, distincte de _repo.git utilisée par le
// moteur (aucune contention de verrous Git avec les fetch/push des tickets).
func (m *Manager) graphClonePath(slug string) (string, error) {
	if !domain.ValidSlug(slug) {
		return "", ErrUnsafePath
	}
	return m.under(slug, "_graph.git")
}

// Graph met à jour la copie dédiée (branches et tags) puis lit le graphe des branches actives.
// Lecture seule vis-à-vis du dépôt distant.
func (m *Manager) Graph(ctx context.Context, slug, repo string, q GraphQuery) (*Graph, error) {
	if !domain.ValidRepository(repo) {
		return nil, fmt.Errorf("%w: dépôt invalide", ErrGit)
	}
	if q.Days <= 0 {
		q.Days = 30
	}
	if q.MaxCommits <= 0 {
		q.MaxCommits = 400
	}
	if q.Now.IsZero() {
		q.Now = time.Now()
	}
	clone, err := m.graphClonePath(slug)
	if err != nil {
		return nil, err
	}
	graphMu.Lock()
	defer graphMu.Unlock()

	url := m.RemoteURL(repo)
	if !exists(filepath.Join(clone, "HEAD")) {
		if err := os.MkdirAll(clone, 0o750); err != nil {
			return nil, err
		}
		if _, err := m.Git.Run(ctx, clone, nil, "init", "--quiet", "--bare"); err != nil {
			return nil, err
		}
		if _, err := m.Git.Run(ctx, clone, nil, "remote", "add", "origin", url); err != nil {
			return nil, err
		}
	} else if _, err := m.Git.Run(ctx, clone, nil, "remote", "set-url", "origin", url); err != nil {
		return nil, err
	}
	if _, err := m.runAuth(ctx, clone, "fetch", "--quiet", "--no-tags", "--prune", "--force", "origin",
		"+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"); err != nil {
		return nil, err
	}

	since := q.Now.Add(-time.Duration(q.Days) * 24 * time.Hour)
	out, err := m.Git.Run(ctx, clone, nil, "for-each-ref",
		"--format=%(refname)%09%(objectname)%09%(*objectname)%09%(committerdate:unix)", "refs/remotes/origin", "refs/tags")
	if err != nil {
		return nil, err
	}
	g := &Graph{Since: since, Refs: []GraphRef{}, Tags: []GraphTag{}, Commits: []GraphCommit{}}
	var tags []GraphTag
	var mainlines []string
	// Pas de TrimSpace global : un tag annoté en dernière ligne finit par un champ date vide.
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 4 {
			continue
		}
		if name, ok := strings.CutPrefix(f[0], "refs/tags/"); ok {
			sha := f[1]
			if f[2] != "" {
				sha = f[2] // tag annoté : commit pointé
			}
			tags = append(tags, GraphTag{Name: name, SHA: sha})
			continue
		}
		name := strings.TrimPrefix(f[0], "refs/remotes/origin/")
		if name == "HEAD" {
			continue
		}
		kind := BranchKind(name, q.BaseBranch)
		if kind == KindMain || kind == KindDevelop {
			mainlines = append(mainlines, f[0])
		}
		if !q.Kinds[kind] {
			continue
		}
		ts, _ := strconv.ParseInt(f[3], 10, 64)
		date := time.Unix(ts, 0).UTC()
		// Branches actives : modifiées dans la fenêtre ; main et develop sont toujours montrées.
		if date.Before(since) && kind != KindMain && kind != KindDevelop {
			continue
		}
		g.Refs = append(g.Refs, GraphRef{Name: name, Kind: kind, SHA: f[1], Date: date})
	}
	// Branches mergées : pointe atteignable depuis main ou develop.
	merged := map[string]bool{}
	for _, ml := range mainlines {
		out, err := m.Git.Run(ctx, clone, nil, "for-each-ref", "--format=%(refname)", "--merged", ml, "refs/remotes/origin")
		if err != nil {
			return nil, err
		}
		for _, r := range strings.Fields(out) {
			merged[strings.TrimPrefix(r, "refs/remotes/origin/")] = true
		}
	}
	// Merge en squash ou rebase : la pointe n'est pas un ancêtre, mais son contenu est déjà
	// intégré (la fusionner dans main ou develop ne change pas l'arbre). Un conflit = non intégrée.
	mlTrees := map[string]string{}
	for _, ml := range mainlines {
		tree, err := m.Git.Run(ctx, clone, nil, "rev-parse", ml+"^{tree}")
		if err != nil {
			return nil, err
		}
		mlTrees[ml] = strings.TrimSpace(tree)
	}
	for _, r := range g.Refs {
		if r.Kind == KindMain || r.Kind == KindDevelop || merged[r.Name] {
			continue
		}
		for _, ml := range mainlines {
			out, err := m.Git.Run(ctx, clone, nil, "merge-tree", "--write-tree", "--no-messages", ml, "refs/remotes/origin/"+r.Name)
			if err == nil && strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]) == mlTrees[ml] {
				merged[r.Name] = true
				break
			}
		}
	}
	kept := g.Refs[:0]
	for _, r := range g.Refs {
		if r.Kind != KindMain && r.Kind != KindDevelop && merged[r.Name] {
			r.Merged = true
			if !q.IncludeMerged {
				g.HiddenMerged++
				continue
			}
		}
		kept = append(kept, r)
	}
	g.Refs = kept
	if len(g.Refs) == 0 {
		return g, nil
	}
	sort.SliceStable(g.Refs, func(i, j int) bool {
		pi, pj := kindRank(g.Refs[i].Kind), kindRank(g.Refs[j].Kind)
		if pi != pj {
			return pi < pj
		}
		return g.Refs[i].Date.After(g.Refs[j].Date)
	})

	const format = "--format=%H%x1f%P%x1f%ct%x1f%an%x1f%s%x1e"
	args := []string{"log", "--topo-order", format, "--since=" + since.UTC().Format(time.RFC3339), "-n", strconv.Itoa(q.MaxCommits + 1)}
	for _, r := range g.Refs {
		args = append(args, "refs/remotes/origin/"+r.Name)
	}
	out, err = m.Git.Run(ctx, clone, nil, append(args, "--")...)
	if err != nil {
		return nil, err
	}
	g.Commits = parseLog(out)
	if len(g.Commits) > q.MaxCommits {
		g.Commits, g.Truncated = g.Commits[:q.MaxCommits], true
	}
	// Pointe d'une branche plus ancienne que la fenêtre (main/develop inactives) : ajoutée en fin.
	seen := map[string]bool{}
	for _, c := range g.Commits {
		seen[c.SHA] = true
	}
	var missing []string
	for _, r := range g.Refs {
		if !seen[r.SHA] {
			missing = append(missing, r.SHA)
			seen[r.SHA] = true
		}
	}
	if len(missing) > 0 {
		out, err = m.Git.Run(ctx, clone, nil, append([]string{"log", "--no-walk", format}, append(missing, "--")...)...)
		if err != nil {
			return nil, err
		}
		g.Commits = append(g.Commits, parseLog(out)...)
	}
	labelCommits(g)
	for _, t := range tags {
		if seen[t.SHA] {
			g.Tags = append(g.Tags, t)
		}
	}
	return g, nil
}

func kindRank(k string) int {
	for i, v := range AllKinds {
		if v == k {
			return i
		}
	}
	return len(AllKinds)
}

func parseLog(out string) []GraphCommit {
	var cs []GraphCommit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.Trim(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) != 5 {
			continue
		}
		ts, _ := strconv.ParseInt(f[2], 10, 64)
		parents := strings.Fields(f[1])
		if parents == nil {
			parents = []string{}
		}
		cs = append(cs, GraphCommit{SHA: f[0], Parents: parents, Date: time.Unix(ts, 0).UTC(), Author: f[3],
			Subject: f[4], PR: PRFromSubject(f[4])})
	}
	return cs
}

// labelCommits attribue chaque commit à une branche en suivant les premiers parents depuis
// chaque pointe, par ordre de priorité (main, develop, release, hotfix, feature, autres).
func labelCommits(g *Graph) {
	idx := make(map[string]int, len(g.Commits))
	for i, c := range g.Commits {
		idx[c.SHA] = i
	}
	for _, r := range g.Refs {
		sha := r.SHA
		for {
			i, ok := idx[sha]
			if !ok || g.Commits[i].Branch != "" {
				break
			}
			g.Commits[i].Branch, g.Commits[i].Kind = r.Name, r.Kind
			if len(g.Commits[i].Parents) == 0 {
				break
			}
			sha = g.Commits[i].Parents[0]
		}
	}
	for i := range g.Commits {
		if g.Commits[i].Kind == "" {
			g.Commits[i].Kind = KindOther
		}
	}
}
