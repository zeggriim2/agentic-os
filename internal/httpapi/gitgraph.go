package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeggriim2/agentic-os/internal/gitops"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// GitGraphSource lit le graphe Git d'un projet (gitops.Manager en production).
type GitGraphSource interface {
	Graph(ctx context.Context, slug, repo string, q gitops.GraphQuery) (*gitops.Graph, error)
}

const gitGraphTTL = 30 * time.Second

type gitGraphCache struct {
	mu      sync.Mutex
	entries map[string]gitGraphEntry
}

type gitGraphEntry struct {
	at time.Time
	g  *gitops.Graph
}

type gitLink struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

type gitTicket struct {
	ID    int64  `json:"id"`
	Key   string `json:"key"`
	State string `json:"state"`
}

type gitRefView struct {
	gitops.GraphRef
	URL    string     `json:"url"`
	Ticket *gitTicket `json:"ticket,omitempty"`
	PR     *gitLink   `json:"pr,omitempty"`
}

type gitTagView struct {
	gitops.GraphTag
	URL string `json:"url"`
}

type gitCommitView struct {
	SHA     string    `json:"sha"`
	Parents []string  `json:"parents"`
	Date    time.Time `json:"date"`
	Author  string    `json:"author"`
	Subject string    `json:"subject"`
	Branch  string    `json:"branch"`
	Kind    string    `json:"kind"`
	URL     string    `json:"url"`
	PR      *gitLink  `json:"pr,omitempty"`
}

// parseGitGraphQuery valide days (1..365, défaut 30) et kinds (liste, défaut : tous).
func parseGitGraphQuery(r *http.Request) (days int, kinds map[string]bool, merged bool, key string, err error) {
	days = 30
	if v := r.URL.Query().Get("days"); v != "" {
		days, err = strconv.Atoi(v)
		if err != nil || days < 1 || days > 365 {
			return 0, nil, false, "", errors.New("days : entier entre 1 et 365")
		}
	}
	kinds = map[string]bool{}
	raw := r.URL.Query().Get("kinds")
	if raw == "" {
		for _, k := range gitops.AllKinds {
			kinds[k] = true
		}
	} else {
		for _, k := range strings.Split(raw, ",") {
			ok := false
			for _, a := range gitops.AllKinds {
				ok = ok || a == k
			}
			if !ok {
				return 0, nil, false, "", fmt.Errorf("kinds : type inconnu %q", k)
			}
			kinds[k] = true
		}
	}
	parts := []string{}
	for _, k := range gitops.AllKinds {
		if kinds[k] {
			parts = append(parts, k)
		}
	}
	switch r.URL.Query().Get("merged") {
	case "", "0":
	case "1":
		merged = true
	default:
		return 0, nil, false, "", errors.New("merged : 0 ou 1")
	}
	return days, kinds, merged, strconv.Itoa(days) + "|" + strings.Join(parts, ",") + "|" + strconv.FormatBool(merged), nil
}

func (s *Server) projectGitGraph(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	days, kinds, merged, qkey, err := parseGitGraphQuery(r)
	if err != nil {
		writeError(w, 400, "invalid_query", err.Error())
		return
	}
	p, err := store.GetProject(r.Context(), s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if s.GitGraph == nil {
		writeError(w, 503, "git_unavailable", "lecture Git non configurée")
		return
	}
	cacheKey := strconv.FormatInt(p.ID, 10) + "|" + p.Repository + "|" + p.BaseBranch + "|" + qkey
	s.gitCache.mu.Lock()
	if s.gitCache.entries == nil {
		s.gitCache.entries = map[string]gitGraphEntry{}
	}
	e, hit := s.gitCache.entries[cacheKey]
	s.gitCache.mu.Unlock()
	refresh := r.URL.Query().Get("refresh") == "1"
	g := e.g
	if !hit || refresh || time.Since(e.at) > gitGraphTTL {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		g, err = s.GitGraph.Graph(ctx, p.Slug, p.Repository, gitops.GraphQuery{Days: days, Kinds: kinds, BaseBranch: p.BaseBranch, IncludeMerged: merged})
		if err != nil {
			// Le détail (stderr Git) est journalisé, jamais renvoyé : il peut contenir des URL internes.
			s.Log.Warn("graphe git", "project", p.Slug, "error", err.Error())
			writeError(w, 502, "git_error", "lecture du dépôt impossible ; réessayez dans un instant")
			return
		}
		s.gitCache.mu.Lock()
		s.gitCache.entries[cacheKey] = gitGraphEntry{at: time.Now(), g: g}
		s.gitCache.mu.Unlock()
	}

	tickets, err := store.ListTicketsByProject(r.Context(), s.Store.Pool, p.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byBranch := map[string]*store.Ticket{}
	for i := range tickets {
		if b := tickets[i].Branch; b != nil {
			byBranch[*b] = &tickets[i]
		}
	}
	web := strings.TrimRight(s.GitHubWebURL, "/") + "/" + p.Repository
	prLink := func(n int) *gitLink {
		if n <= 0 {
			return nil
		}
		return &gitLink{Number: n, URL: fmt.Sprintf("%s/pull/%d", web, n)}
	}
	refs := make([]gitRefView, 0, len(g.Refs))
	for _, ref := range g.Refs {
		v := gitRefView{GraphRef: ref, URL: web + "/tree/" + ref.Name}
		if t := byBranch[ref.Name]; t != nil {
			v.Ticket = &gitTicket{ID: t.ID, Key: t.Key, State: t.State}
			if t.PRNumber != nil {
				v.PR = prLink(*t.PRNumber)
			}
		}
		refs = append(refs, v)
	}
	tags := make([]gitTagView, 0, len(g.Tags))
	for _, t := range g.Tags {
		tags = append(tags, gitTagView{GraphTag: t, URL: web + "/releases/tag/" + t.Name})
	}
	commits := make([]gitCommitView, 0, len(g.Commits))
	for _, c := range g.Commits {
		commits = append(commits, gitCommitView{SHA: c.SHA, Parents: c.Parents, Date: c.Date, Author: c.Author,
			Subject: c.Subject, Branch: c.Branch, Kind: c.Kind, URL: web + "/commit/" + c.SHA, PR: prLink(c.PR)})
	}
	writeJSON(w, 200, map[string]any{
		"project":       viewProject(p),
		"days":          days,
		"since":         g.Since,
		"truncated":     g.Truncated,
		"merged":        merged,
		"hidden_merged": g.HiddenMerged,
		"refs":          refs,
		"tags":          tags,
		"commits":       commits,
	})
}
