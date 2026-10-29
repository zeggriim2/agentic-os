// Package github est un client REST GitHub minimal (token bot) et la vérification des webhooks.
package github

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

// TokenSource fournit le token bot (lu dans un fichier à chaque appel).
type TokenSource func() (string, error)

// Client appelle l'API REST GitHub.
type Client struct {
	APIURL string
	HTTP   *http.Client
	Token  TokenSource
}

// New construit un client.
func New(apiURL string, token TokenSource) *Client {
	return &Client{APIURL: strings.TrimRight(apiURL, "/"), Token: token, HTTP: &http.Client{Timeout: 30 * time.Second}}
}

// Error est une erreur GitHub classée.
type Error struct {
	Status int
	Class  string
	Msg    string
}

func (e *Error) Error() string { return fmt.Sprintf("github: HTTP %d: %s", e.Status, e.Msg) }

// ErrNotFound indique une ressource absente.
var ErrNotFound = errors.New("github: ressource introuvable")

// Is supporte errors.Is(err, ErrNotFound).
func (e *Error) Is(t error) bool { return t == ErrNotFound && e.Status == http.StatusNotFound }

// ClassOf renvoie la classe d'erreur.
func ClassOf(err error) string {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Class
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return domain.ErrClassTimeout
	}
	return domain.ErrClassExternal
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	tok, err := c.Token()
	if err != nil {
		return &Error{Class: domain.ErrClassPolicy, Msg: "token GitHub indisponible"}
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.APIURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "agentic-os")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Class: domain.ErrClassExternal, Msg: "GitHub injoignable"}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		e := &Error{Status: resp.StatusCode, Class: domain.ErrClassExternal, Msg: http.StatusText(resp.StatusCode)}
		var m struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &m) == nil && m.Message != "" {
			e.Msg = truncate(m.Message, 300)
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			e.Class = domain.ErrClassPolicy
		}
		return e
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return &Error{Status: resp.StatusCode, Class: domain.ErrClassExternal, Msg: "réponse GitHub illisible"}
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func repoPath(repo string) (string, error) {
	if !domain.ValidRepository(repo) {
		return "", &Error{Class: domain.ErrClassPolicy, Msg: "dépôt invalide"}
	}
	parts := strings.SplitN(repo, "/", 2)
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}

// Marker renvoie le marqueur caché d'un ticket.
func Marker(key string) string { return "<!-- agentic-os:ticket=" + key + " -->" }

// Issue est une issue relue.
type Issue struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	State   string `json:"state"`
	PR      *struct {
		URL string `json:"url"`
	} `json:"pull_request,omitempty"`
}

// FindIssueByMarker cherche une issue portant le marqueur du ticket.
func (c *Client) FindIssueByMarker(ctx context.Context, repo, key string) (*Issue, error) {
	if !domain.ValidTicketKey(key) {
		return nil, &Error{Class: domain.ErrClassPolicy, Msg: "clé de ticket invalide"}
	}
	if !domain.ValidRepository(repo) {
		return nil, &Error{Class: domain.ErrClassPolicy, Msg: "dépôt invalide"}
	}
	// is:issue est obligatoire : GitHub refuse désormais une recherche sans type (HTTP 422
	// « Query must include 'is:issue' or 'is:pull-request' »).
	q := fmt.Sprintf(`repo:%s is:issue "agentic-os:ticket=%s" in:body`, repo, key)
	var res struct {
		Items []Issue `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/search/issues?q="+url.QueryEscape(q), nil, &res); err != nil {
		return nil, err
	}
	for i := range res.Items {
		it := res.Items[i]
		if it.PR == nil && strings.Contains(it.Body, Marker(key)) {
			return &it, nil
		}
	}
	return nil, nil
}

// GetIssue relit une issue.
func (c *Client) GetIssue(ctx context.Context, repo string, number int) (Issue, error) {
	var is Issue
	p, err := repoPath(repo)
	if err != nil {
		return is, err
	}
	err = c.do(ctx, http.MethodGet, fmt.Sprintf("%s/issues/%d", p, number), nil, &is)
	return is, err
}

// EnsureIssue recherche l'issue par marqueur avant création, puis relit l'issue créée.
func (c *Client) EnsureIssue(ctx context.Context, repo, key, title, body string) (Issue, bool, error) {
	if found, err := c.FindIssueByMarker(ctx, repo, key); err != nil {
		return Issue{}, false, err
	} else if found != nil {
		return *found, false, nil
	}
	p, err := repoPath(repo)
	if err != nil {
		return Issue{}, false, err
	}
	fullBody := strings.TrimRight(body, "\n") + "\n\n" + Marker(key)
	var created Issue
	if err := c.do(ctx, http.MethodPost, p+"/issues", map[string]any{"title": truncate(title, 250), "body": fullBody}, &created); err != nil {
		return Issue{}, false, err
	}
	if created.Number <= 0 {
		return Issue{}, false, &Error{Class: domain.ErrClassExternal, Msg: "numéro d'issue absent"}
	}
	back, err := c.GetIssue(ctx, repo, created.Number)
	if err != nil {
		return Issue{}, true, err
	}
	if back.Number != created.Number || back.HTMLURL == "" || !strings.Contains(back.Body, Marker(key)) {
		return Issue{}, true, &Error{Class: domain.ErrClassExternal, Msg: "relecture de l'issue incohérente"}
	}
	return back, true, nil
}

// BranchSHA lit le SHA distant d'une branche.
func (c *Client) BranchSHA(ctx context.Context, repo, branch string) (string, error) {
	p, err := repoPath(repo)
	if err != nil {
		return "", err
	}
	if !domain.ValidBranch(branch) {
		return "", &Error{Class: domain.ErrClassPolicy, Msg: "branche invalide"}
	}
	var res struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := c.do(ctx, http.MethodGet, p+"/branches/"+url.PathEscape(branch), nil, &res); err != nil {
		return "", err
	}
	if !domain.ValidSHA(res.Commit.SHA) {
		return "", &Error{Class: domain.ErrClassExternal, Msg: "SHA de branche invalide"}
	}
	return res.Commit.SHA, nil
}

// PR est une pull request relue.
type PR struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	Merged  bool   `json:"merged"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

// GetPR relit une PR.
func (c *Client) GetPR(ctx context.Context, repo string, number int) (PR, error) {
	var pr PR
	p, err := repoPath(repo)
	if err != nil {
		return pr, err
	}
	err = c.do(ctx, http.MethodGet, fmt.Sprintf("%s/pulls/%d", p, number), nil, &pr)
	return pr, err
}

// EnsureDraftPR réutilise la PR ouverte de la head ou crée une PR draft, puis relit
// number/head.sha/base.ref/draft.
func (c *Client) EnsureDraftPR(ctx context.Context, repo, head, base, title, body string) (PR, bool, error) {
	p, err := repoPath(repo)
	if err != nil {
		return PR{}, false, err
	}
	if !domain.ValidBranch(head) || !domain.ValidBranch(base) {
		return PR{}, false, &Error{Class: domain.ErrClassPolicy, Msg: "branche invalide"}
	}
	owner := strings.SplitN(repo, "/", 2)[0]
	var open []PR
	q := url.Values{"state": {"open"}, "head": {owner + ":" + head}, "base": {base}}
	if err := c.do(ctx, http.MethodGet, p+"/pulls?"+q.Encode(), nil, &open); err != nil {
		return PR{}, false, err
	}
	number := 0
	created := false
	for _, pr := range open {
		if pr.Head.Ref == head && pr.Base.Ref == base {
			number = pr.Number
			break
		}
	}
	if number == 0 {
		var pr PR
		err := c.do(ctx, http.MethodPost, p+"/pulls", map[string]any{
			"title": truncate(title, 250), "head": head, "base": base, "body": body, "draft": true,
		}, &pr)
		if err != nil {
			return PR{}, false, err
		}
		number = pr.Number
		created = true
	}
	if number <= 0 {
		return PR{}, created, &Error{Class: domain.ErrClassExternal, Msg: "numéro de PR absent"}
	}
	back, err := c.GetPR(ctx, repo, number)
	if err != nil {
		return PR{}, created, err
	}
	if back.Number != number || back.Head.Ref != head || back.Base.Ref != base || back.State != "open" || !back.Draft {
		return PR{}, created, &Error{Class: domain.ErrClassPolicy, Msg: "la PR relue doit être ouverte, draft et correspondre exactement aux branches approuvées"}
	}
	return back, created, nil
}

// RetargetPR change la branche cible d'une PR ouverte (PATCH base), puis relit la PR : elle doit
// rester ouverte, sur la même head, avec la nouvelle base. Sans effet si la base est déjà la bonne.
func (c *Client) RetargetPR(ctx context.Context, repo string, number int, base string) (PR, error) {
	p, err := repoPath(repo)
	if err != nil {
		return PR{}, err
	}
	if number <= 0 || !domain.ValidBranch(base) {
		return PR{}, &Error{Class: domain.ErrClassPolicy, Msg: "PR ou branche invalide"}
	}
	cur, err := c.GetPR(ctx, repo, number)
	if err != nil {
		return PR{}, err
	}
	if cur.State != "open" {
		return PR{}, &Error{Class: domain.ErrClassPolicy, Msg: fmt.Sprintf("PR #%d non ouverte : cible inchangée", number)}
	}
	if cur.Base.Ref != base {
		if err := c.do(ctx, http.MethodPatch, fmt.Sprintf("%s/pulls/%d", p, number), map[string]any{"base": base}, nil); err != nil {
			return PR{}, err
		}
	}
	back, err := c.GetPR(ctx, repo, number)
	if err != nil {
		return PR{}, err
	}
	if back.Number != number || back.Base.Ref != base || back.Head.Ref != cur.Head.Ref || back.State != "open" {
		return PR{}, &Error{Class: domain.ErrClassExternal, Msg: fmt.Sprintf("PR #%d relue : cible %q au lieu de %q", number, back.Base.Ref, base)}
	}
	return back, nil
}

// CheckEvidence décrit l'observation d'un check requis.
type CheckEvidence struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	State  string `json:"state"`
	URL    string `json:"url,omitempty"`
}

// CIState agrège check-runs et statuts de commit pour le SHA exact.
// success seulement si chaque check requis est completed/success ; un check requis terminé
// autrement → failure ; absent ou en cours → pending. Sans check requis → unverified.
func (c *Client) CIState(ctx context.Context, repo, sha string, required []string) (string, []CheckEvidence, error) {
	if len(required) == 0 {
		return domain.CIUnverified, nil, nil
	}
	p, err := repoPath(repo)
	if err != nil {
		return "", nil, err
	}
	if !domain.ValidSHA(sha) {
		return "", nil, &Error{Class: domain.ErrClassPolicy, Msg: "SHA invalide"}
	}
	var runs struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HeadSHA    string `json:"head_sha"`
			HTMLURL    string `json:"html_url"`
		} `json:"check_runs"`
	}
	if err := c.do(ctx, http.MethodGet, p+"/commits/"+sha+"/check-runs?per_page=100", nil, &runs); err != nil {
		return "", nil, err
	}
	var status struct {
		Statuses []struct {
			Context   string `json:"context"`
			State     string `json:"state"`
			TargetURL string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := c.do(ctx, http.MethodGet, p+"/commits/"+sha+"/status", nil, &status); err != nil {
		return "", nil, err
	}
	overall := domain.CISuccess
	var ev []CheckEvidence
	for _, name := range required {
		state := ""
		e := CheckEvidence{Name: name}
		var bestID int64 = -1
		for _, r := range runs.CheckRuns {
			if r.Name != name || (r.HeadSHA != "" && r.HeadSHA != sha) || r.ID < bestID {
				continue
			}
			bestID = r.ID
			e.Source, e.URL = "check_run", r.HTMLURL
			switch {
			case r.Status != "completed":
				state = domain.CIPending
			case r.Conclusion == "success":
				state = domain.CISuccess
			default:
				state = domain.CIFailure
			}
		}
		if state == "" {
			for _, s := range status.Statuses {
				if s.Context != name {
					continue
				}
				e.Source, e.URL = "status", s.TargetURL
				switch s.State {
				case "success":
					state = domain.CISuccess
				case "failure", "error":
					state = domain.CIFailure
				default:
					state = domain.CIPending
				}
				break // l'API renvoie le statut le plus récent en premier
			}
		}
		if state == "" {
			state = domain.CIPending
			e.Source = "absent"
		}
		e.State = state
		ev = append(ev, e)
		if state == domain.CIFailure {
			overall = domain.CIFailure
		} else if state == domain.CIPending && overall != domain.CIFailure {
			overall = domain.CIPending
		}
	}
	return overall, ev, nil
}

// RepoAccess vérifie que le token a accès au dépôt (doctor).
func (c *Client) RepoAccess(ctx context.Context, repo string) error {
	p, err := repoPath(repo)
	if err != nil {
		return err
	}
	var out json.RawMessage
	return c.do(ctx, http.MethodGet, p, nil, &out)
}

// Check vérifie le token (GET /rate_limit) pour le health check.
func (c *Client) Check(ctx context.Context) error {
	var out json.RawMessage
	return c.do(ctx, http.MethodGet, "/rate_limit", nil, &out)
}

// VerifySignature vérifie X-Hub-Signature-256 (sha256=<hex>) sur le corps brut, en temps constant.
func VerifySignature(secret []byte, body []byte, header string) bool {
	if len(secret) == 0 || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil || len(got) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// Sign calcule la signature (utilisé par les tests et outils).
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
