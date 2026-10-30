// Package gitops exécute git (binaire absolu, argv explicite, env minimal, timeouts) pour
// gérer clones, worktrees, vérifications et push. Le token n'apparaît jamais dans l'URL,
// l'argv, l'environnement ni les logs : il est lu depuis son fichier par un credential helper shell.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

// Erreurs typées.
var (
	ErrNonFastForward = errors.New("push refusé (non fast-forward)")
	ErrUnsafePath     = errors.New("chemin de workspace invalide")
	ErrGit            = errors.New("échec git")
)

// Runner exécute le binaire git.
type Runner struct {
	GitPath   string
	Timeout   time.Duration
	MaxOutput int
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.limit - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

// Run exécute git avec un environnement minimal ; extraEnv est ajouté tel quel.
func (r Runner) Run(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	if !filepath.IsAbs(r.GitPath) {
		return "", fmt.Errorf("%w: binaire git non absolu", ErrGit)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	limit := r.MaxOutput
	if limit <= 0 {
		limit = 1 << 20
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "credential.helper=", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, r.GitPath, full...)
	cmd.Dir = dir
	cmd.Env = append([]string{
		"PATH=/usr/bin:/bin",
		"HOME=/nonexistent",
		"LANG=C",
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_OPTIONAL_LOCKS=0",
	}, extraEnv...)
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = limit, 64<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.buf.String(), fmt.Errorf("%w: délai dépassé (git %s)", ErrGit, safeArg(args))
	}
	if err != nil {
		return stdout.buf.String(), &CmdError{Args: safeArg(args), Stderr: strings.TrimSpace(stderr.buf.String())}
	}
	return stdout.buf.String(), nil
}

func safeArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// CmdError est un échec de commande git (stderr borné, sans secret : aucun secret n'est passé à git).
type CmdError struct {
	Args   string
	Stderr string
}

func (e *CmdError) Error() string {
	s := e.Stderr
	if len(s) > 500 {
		s = s[:500]
	}
	return fmt.Sprintf("git %s: %s", e.Args, s)
}

func (e *CmdError) Unwrap() error { return ErrGit }

// Manager gère clones et worktrees sous Root.
type Manager struct {
	Git       Runner
	Root      string
	RemoteURL func(repo string) string
	// TokenFile : fichier du token bot ; vide = pas d'authentification (dépôts locaux de test).
	TokenFile string
}

// GitHubRemote construit https://github.com/<repo>.git (sans token).
func GitHubRemote(webURL string) func(string) string {
	return func(repo string) string { return strings.TrimRight(webURL, "/") + "/" + repo + ".git" }
}

func (m *Manager) under(parts ...string) (string, error) {
	root := filepath.Clean(m.Root)
	if !filepath.IsAbs(root) {
		return "", ErrUnsafePath
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "/\\\x00") || strings.Contains(p, "..") {
			return "", ErrUnsafePath
		}
	}
	full := filepath.Join(append([]string{root}, parts...)...)
	rel, err := filepath.Rel(root, full)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", ErrUnsafePath
	}
	return full, nil
}

// ClonePath renvoie <root>/<slug>/_repo.git.
func (m *Manager) ClonePath(slug string) (string, error) {
	if !domain.ValidSlug(slug) {
		return "", ErrUnsafePath
	}
	return m.under(slug, "_repo.git")
}

// WorktreePath renvoie <root>/<slug>/<name> (name = clé de ticket, éventuellement suffixée).
func (m *Manager) WorktreePath(slug, name string) (string, error) {
	if !domain.ValidSlug(slug) || name == "_repo.git" {
		return "", ErrUnsafePath
	}
	return m.under(slug, name)
}

// Relative renvoie un chemin relatif à la racine (pour affichage).
func (m *Manager) Relative(p string) string {
	rel, err := filepath.Rel(filepath.Clean(m.Root), p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel
}

// credentialHelper renvoie le helper Git qui fournit le token. C'est une commande shell
// (préfixe « ! ») exécutée par Git : aucun fichier temporaire exécutable n'est nécessaire,
// ce qui fonctionne avec un /tmp monté noexec et un système de fichiers en lecture seule.
// Le token est lu depuis son fichier au moment de la demande : il n'apparaît jamais dans
// l'argv, l'environnement, l'URL ni les logs (seul le chemin du fichier est passé).
func (m *Manager) credentialHelper() (string, error) {
	if !filepath.IsAbs(m.TokenFile) || strings.ContainsAny(m.TokenFile, "'\n\r\x00") {
		return "", fmt.Errorf("%w: chemin du token invalide", ErrGit)
	}
	return `!f() { test "$1" = get || exit 0; echo username=x-access-token; printf 'password='; tr -d '\r\n' < '` +
		m.TokenFile + `'; echo; }; f`, nil
}

func (m *Manager) runAuth(ctx context.Context, dir string, args ...string) (string, error) {
	if m.TokenFile == "" {
		return m.Git.Run(ctx, dir, nil, args...)
	}
	h, err := m.credentialHelper()
	if err != nil {
		return "", err
	}
	// Run commence par « -c credential.helper= » (vide la liste des helpers hérités) ;
	// celui-ci est ajouté après et devient le seul helper actif.
	return m.Git.Run(ctx, dir, nil, append([]string{"-c", "credential.helper=" + h}, args...)...)
}

// EnsureClone crée (ou met à jour l'URL de) la copie bare du dépôt.
func (m *Manager) EnsureClone(ctx context.Context, slug, repo string) (string, error) {
	if !domain.ValidRepository(repo) {
		return "", fmt.Errorf("%w: dépôt invalide", ErrGit)
	}
	clone, err := m.ClonePath(slug)
	if err != nil {
		return "", err
	}
	url := m.RemoteURL(repo)
	if _, err := os.Stat(filepath.Join(clone, "HEAD")); err == nil {
		if _, err := m.Git.Run(ctx, clone, nil, "remote", "set-url", "origin", url); err != nil {
			return "", err
		}
		return clone, nil
	}
	if err := os.MkdirAll(filepath.Dir(clone), 0o750); err != nil {
		return "", err
	}
	if _, err := m.runAuth(ctx, filepath.Dir(clone), "clone", "--bare", "--no-tags", "--", url, clone); err != nil {
		return "", err
	}
	return clone, nil
}

// FetchBranch récupère refs/heads/<branch> dans refs/remotes/origin/<branch>.
// Renvoie false si la branche n'existe pas à distance.
func (m *Manager) FetchBranch(ctx context.Context, clone, branch string) (bool, error) {
	if !domain.ValidBranch(branch) {
		return false, fmt.Errorf("%w: branche invalide", ErrGit)
	}
	_, err := m.runAuth(ctx, clone, "fetch", "--no-tags", "--prune", "origin",
		"+refs/heads/"+branch+":refs/remotes/origin/"+branch)
	if err != nil {
		var ce *CmdError
		if errors.As(err, &ce) && strings.Contains(ce.Stderr, "couldn't find remote ref") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// RevParse résout une référence en SHA.
func (m *Manager) RevParse(ctx context.Context, dir, ref string) (string, error) {
	out, err := m.Git.Run(ctx, dir, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RemoteBranchSHA lit le SHA distant via ls-remote.
func (m *Manager) RemoteBranchSHA(ctx context.Context, clone, branch string) (string, error) {
	if !domain.ValidBranch(branch) {
		return "", fmt.Errorf("%w: branche invalide", ErrGit)
	}
	out, err := m.runAuth(ctx, clone, "ls-remote", "origin", "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	f := strings.Fields(out)
	if len(f) == 0 {
		return "", nil
	}
	return f[0], nil
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// EnsureBranchWorktree crée (ou réutilise) le worktree de développement <root>/<slug>/<key>
// sur la branche, à partir de la branche distante si elle existe, sinon de la base.
func (m *Manager) EnsureBranchWorktree(ctx context.Context, slug, repo, key, branch, base string) (string, error) {
	clone, err := m.EnsureClone(ctx, slug, repo)
	if err != nil {
		return "", err
	}
	path, err := m.WorktreePath(slug, key)
	if err != nil {
		return "", err
	}
	if !domain.ValidBranch(branch) || !domain.ValidBranch(base) {
		return "", fmt.Errorf("%w: branche invalide", ErrGit)
	}
	if exists(path) {
		cur, err := m.CurrentBranch(ctx, path)
		if err != nil {
			return "", err
		}
		if cur != branch {
			return "", fmt.Errorf("%w: worktree existant sur %q au lieu de %q", ErrGit, cur, branch)
		}
		return path, nil
	}
	if ok, err := m.FetchBranch(ctx, clone, base); err != nil {
		return "", err
	} else if !ok {
		return "", fmt.Errorf("%w: branche de base %q absente", ErrGit, base)
	}
	start := "refs/remotes/origin/" + base
	if ok, err := m.FetchBranch(ctx, clone, branch); err != nil {
		return "", err
	} else if ok {
		start = "refs/remotes/origin/" + branch
	}
	_, _ = m.Git.Run(ctx, clone, nil, "worktree", "prune")
	if _, err := m.Git.Run(ctx, clone, nil, "worktree", "add", "-B", branch, "--", path, start); err != nil {
		return "", err
	}
	return path, nil
}

// EnsureDetachedWorktree (re)crée un worktree détaché au SHA exact.
func (m *Manager) EnsureDetachedWorktree(ctx context.Context, slug, repo, name, branch, sha string) (string, error) {
	if !domain.ValidSHA(sha) {
		return "", fmt.Errorf("%w: SHA invalide", ErrGit)
	}
	clone, err := m.EnsureClone(ctx, slug, repo)
	if err != nil {
		return "", err
	}
	path, err := m.WorktreePath(slug, name)
	if err != nil {
		return "", err
	}
	if _, err := m.FetchBranch(ctx, clone, branch); err != nil {
		return "", err
	}
	if exists(path) {
		if _, err := m.Git.Run(ctx, clone, nil, "worktree", "remove", "--force", "--", path); err != nil {
			if err := os.RemoveAll(path); err != nil {
				return "", err
			}
		}
	}
	_, _ = m.Git.Run(ctx, clone, nil, "worktree", "prune")
	if _, err := m.Git.Run(ctx, clone, nil, "worktree", "add", "--detach", "--", path, sha); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveWorktree supprime un worktree.
func (m *Manager) RemoveWorktree(ctx context.Context, slug, name string) error {
	clone, err := m.ClonePath(slug)
	if err != nil {
		return err
	}
	path, err := m.WorktreePath(slug, name)
	if err != nil {
		return err
	}
	if !exists(path) {
		return nil
	}
	_, err = m.Git.Run(ctx, clone, nil, "worktree", "remove", "--force", "--", path)
	return err
}

// Porcelain renvoie `git status --porcelain` incluant les fichiers non suivis.
func (m *Manager) Porcelain(ctx context.Context, path string) (string, error) {
	return m.Git.Run(ctx, path, nil, "status", "--porcelain=v1", "--untracked-files=all")
}

// EnsureClean échoue si le worktree contient des modifications ou des fichiers non suivis.
func (m *Manager) EnsureClean(ctx context.Context, path string) error {
	out, err := m.Porcelain(ctx, path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "" {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) > 10 {
			lines = append(lines[:10], "…")
		}
		return &DirtyError{Entries: lines}
	}
	return nil
}

// DirtyError indique un worktree non propre.
type DirtyError struct{ Entries []string }

func (e *DirtyError) Error() string {
	return "worktree non propre : " + strings.Join(e.Entries, ", ")
}

// HeadSHA renvoie le SHA de HEAD.
func (m *Manager) HeadSHA(ctx context.Context, path string) (string, error) {
	return m.RevParse(ctx, path, "HEAD")
}

// CurrentBranch renvoie la branche courante (erreur si HEAD détachée).
func (m *Manager) CurrentBranch(ctx context.Context, path string) (string, error) {
	out, err := m.Git.Run(ctx, path, nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsTestPath reconnaît un fichier de test (*_test.*, tests/, test/).
func IsTestPath(p string) bool {
	p = filepath.ToSlash(p)
	base := filepath.Base(p)
	if i := strings.Index(base, "_test."); i > 0 {
		return true
	}
	for _, seg := range strings.Split(filepath.Dir(p), "/") {
		if seg == "tests" || seg == "test" {
			return true
		}
	}
	return false
}

// DeletedTestFiles liste les fichiers de test supprimés (renommages comptés comme suppressions)
// entre baseRef et HEAD.
func (m *Manager) DeletedTestFiles(ctx context.Context, path, baseRef string) ([]string, error) {
	out, err := m.Git.Run(ctx, path, nil, "diff", "--name-status", "--no-renames", "-z", baseRef+"...HEAD", "--")
	if err != nil {
		return nil, err
	}
	var deleted []string
	parts := strings.Split(out, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		if strings.HasPrefix(parts[i], "D") && IsTestPath(parts[i+1]) {
			deleted = append(deleted, parts[i+1])
		}
	}
	return deleted, nil
}

// EnsureRemoteBranch crée à distance la branche à partir de la pointe actuelle de origin/<from>,
// si elle n'existe pas encore (jamais de force : une branche existante est conservée telle quelle).
// Renvoie le SHA distant de la branche et created=true si elle vient d'être poussée.
func (m *Manager) EnsureRemoteBranch(ctx context.Context, slug, repo, branch, from string) (string, bool, error) {
	if !domain.ValidBranch(branch) || !domain.ValidBranch(from) || branch == from {
		return "", false, fmt.Errorf("%w: branche invalide", ErrGit)
	}
	clone, err := m.EnsureClone(ctx, slug, repo)
	if err != nil {
		return "", false, err
	}
	if sha, err := m.RemoteBranchSHA(ctx, clone, branch); err != nil {
		return "", false, err
	} else if sha != "" {
		if _, err := m.FetchBranch(ctx, clone, branch); err != nil {
			return "", false, err
		}
		return sha, false, nil
	}
	if ok, err := m.FetchBranch(ctx, clone, from); err != nil {
		return "", false, err
	} else if !ok {
		return "", false, fmt.Errorf("%w: branche de base %q absente", ErrGit, from)
	}
	sha, err := m.RevParse(ctx, clone, "refs/remotes/origin/"+from)
	if err != nil {
		return "", false, err
	}
	if _, err := m.runAuth(ctx, clone, "push", "origin", sha+":refs/heads/"+branch); err != nil {
		return "", false, err
	}
	got, err := m.RemoteBranchSHA(ctx, clone, branch)
	if err != nil {
		return "", false, err
	}
	if got != sha {
		return "", false, fmt.Errorf("%w: branche %s relue à %q au lieu de %s", ErrGit, branch, got, sha)
	}
	if _, err := m.FetchBranch(ctx, clone, branch); err != nil {
		return "", false, err
	}
	return sha, true, nil
}

// Push pousse la branche sans jamais forcer. Non fast-forward → ErrNonFastForward.
func (m *Manager) Push(ctx context.Context, path, branch string) error {
	if !domain.ValidBranch(branch) {
		return fmt.Errorf("%w: branche invalide", ErrGit)
	}
	_, err := m.runAuth(ctx, path, "push", "origin", "refs/heads/"+branch+":refs/heads/"+branch)
	if err != nil {
		var ce *CmdError
		if errors.As(err, &ce) && (strings.Contains(ce.Stderr, "non-fast-forward") ||
			strings.Contains(ce.Stderr, "fetch first") || strings.Contains(ce.Stderr, "[rejected]")) {
			return ErrNonFastForward
		}
		return err
	}
	return nil
}

// ChangedFiles liste les fichiers modifiés entre baseRef et HEAD (renommages : nouveau chemin).
func (m *Manager) ChangedFiles(ctx context.Context, path, baseRef string) ([]string, error) {
	out, err := m.Git.Run(ctx, path, nil, "diff", "--name-only", "-z", baseRef+"...HEAD", "--")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}
