package httpapi

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Modèle par défaut des profils Hermes `aos-*` (1.8.0). Hermes n'expose pas d'écriture du modèle d'un
// profil par son API : Agentic OS enregistre le modèle voulu (profile_models) et le script de l'hôte
// deploy/agents/sync-profile-models.sh l'applique. Le modèle effectif est relu par l'API Hermes
// (GET /p/<profil>/api/model/options : provider/model courants du profil).

const (
	profileObsTTL     = 30 * time.Second
	profileObsTimeout = 15 * time.Second
	// profileSyncStale : au-delà, le script de l'hôte est considéré comme absent ou arrêté.
	profileSyncStale = 3 * time.Minute
)

type profileObservation struct {
	provider, model, err string
	at                   time.Time
}

type profileObsCache struct {
	mu sync.Mutex
	m  map[string]profileObservation
}

func (c *profileObsCache) get(profile string) (profileObservation, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.m[profile]
	return o, ok && time.Since(o.at) < profileObsTTL
}

func (c *profileObsCache) put(profile string, o profileObservation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]profileObservation{}
	}
	c.m[profile] = o
}

func (c *profileObsCache) forget(profile string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, profile)
}

// managedProfile : seuls les profils d'Agentic OS sont modifiables (jamais dev-*, default…).
func managedProfile(name string) bool {
	return domain.ValidProfile(name) && strings.HasPrefix(name, "aos-") && len(name) <= 64
}

func (s *Server) managedProfiles() ([]string, error) {
	if s.HermesProfiles == nil {
		return nil, nil
	}
	names, err := s.HermesProfiles()
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, n := range names {
		if managedProfile(n) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out, nil
}

// observeProfiles lit en parallèle le modèle courant de chaque profil (cache 30 s).
func (s *Server) observeProfiles(ctx context.Context, profiles []string) map[string]profileObservation {
	out := map[string]profileObservation{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range profiles {
		if o, ok := s.profileObs.get(p); ok {
			out[p] = o
			continue
		}
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			o := profileObservation{at: time.Now()}
			if s.Models == nil {
				o.err = "catalogue Hermes non configuré"
			} else {
				cctx, cancel := context.WithTimeout(ctx, profileObsTimeout)
				opts, err := s.Models.ModelOptions(cctx, p)
				cancel()
				if err != nil {
					o.err = err.Error()
				} else {
					o.provider, o.model = opts.Provider, opts.Model
				}
			}
			if o.err == "" {
				s.profileObs.put(p, o)
			}
			mu.Lock()
			out[p] = o
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return out
}

type modelRef struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type profileUsage struct {
	Project string `json:"project"` // vide = profil par défaut du rôle (tous projets sans surcharge)
	Role    string `json:"role"`
}

type profileModelView struct {
	Profile       string         `json:"profile"`
	Role          string         `json:"role"`
	Usages        []profileUsage `json:"usages"`
	Current       *modelRef      `json:"current"`
	CurrentError  string         `json:"current_error,omitempty"`
	Desired       *modelRef      `json:"desired"`
	State         string         `json:"state"` // "" (aucune demande) | PENDING | APPLIED | FAILED
	Error         *string        `json:"error"`
	Revision      int            `json:"revision"`
	RequestedBy   string         `json:"requested_by,omitempty"`
	RequestedAt   *string        `json:"requested_at"`
	AppliedAt     *string        `json:"applied_at"`
	MatchesWanted bool           `json:"matches_desired"`
}

type profileMismatch struct {
	Project   string   `json:"project"`
	Role      string   `json:"role"`
	Profile   string   `json:"profile"`
	Requested modelRef `json:"requested"`
	Current   modelRef `json:"current"`
}

func tsPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := ts(*t)
	return &v
}

// listProfileModels : GET /api/profile-models — modèle courant (Hermes) et voulu (Agentic OS) de chaque
// profil aos-*, usages, état du script de l'hôte, et rôles dont le fournisseur demandé par le projet
// diffère du défaut du profil (Hermes résout d'abord ce défaut : s'il est indisponible, le run échoue).
func (s *Server) listProfileModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	profiles, err := s.managedProfiles()
	if err != nil {
		writeError(w, 503, "profiles_unavailable", "liste des profils Hermes indisponible")
		return
	}
	rows, err := store.ListProfileModels(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	agents, err := store.ListAgents(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	projects, err := store.AllProjects(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	lastSeen, host, err := store.ProfileSyncLastSeen(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	byProfile := map[string]store.ProfileModel{}
	for _, pm := range rows {
		byProfile[pm.Profile] = pm
	}
	usages := map[string][]profileUsage{}
	defaults := map[string]string{}
	for _, a := range agents {
		defaults[a.Role] = a.HermesProfile
		usages[a.HermesProfile] = append(usages[a.HermesProfile], profileUsage{Role: a.Role})
	}
	for _, p := range projects {
		for _, role := range domain.Roles {
			if v := p.AgentProfiles[role]; v != "" {
				usages[v] = append(usages[v], profileUsage{Project: p.Slug, Role: role})
			}
		}
	}
	obs := s.observeProfiles(ctx, profiles)
	items := make([]profileModelView, 0, len(profiles))
	for _, name := range profiles {
		v := profileModelView{Profile: name, Role: profileRole(name), Usages: usages[name]}
		if v.Usages == nil {
			v.Usages = []profileUsage{}
		}
		if o := obs[name]; o.err != "" {
			v.CurrentError = o.err
		} else if o.provider != "" || o.model != "" {
			v.Current = &modelRef{o.provider, o.model}
		}
		if pm, ok := byProfile[name]; ok {
			v.Desired = &modelRef{pm.DesiredProvider, pm.DesiredModel}
			v.State, v.Error, v.Revision, v.RequestedBy = pm.State, pm.Error, pm.Revision, pm.RequestedBy
			v.RequestedAt, v.AppliedAt = tsPtr(&pm.RequestedAt), tsPtr(pm.AppliedAt)
			v.MatchesWanted = v.Current != nil && *v.Current == *v.Desired
		}
		items = append(items, v)
	}
	mismatches := []profileMismatch{}
	for _, p := range projects {
		if !p.Enabled {
			continue
		}
		for _, role := range domain.Roles {
			req, ok := p.AgentModels[role]
			if !ok || req.Empty() {
				continue
			}
			prof := p.ProfileFor(role, defaults[role])
			o, seen := obs[prof]
			if !seen || o.err != "" || o.provider == "" || o.provider == req.Provider {
				continue
			}
			mismatches = append(mismatches, profileMismatch{Project: p.Slug, Role: role, Profile: prof,
				Requested: modelRef{req.Provider, req.Model}, Current: modelRef{o.provider, o.model}})
		}
	}
	sync := map[string]any{"last_seen_at": tsPtr(lastSeen), "host": host,
		"stale": lastSeen == nil || time.Since(*lastSeen) > profileSyncStale}
	writeJSON(w, 200, map[string]any{"items": items, "mismatches": mismatches, "sync": sync, "next_cursor": nil})
}

type profileModelReq struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// requestProfileModel : PUT /api/profile-models/{profile} — enregistre le modèle voulu (révision suivante,
// PENDING). Profil aos-* existant, modèle présent dans le catalogue Hermes (fournisseurs authentifiés).
func (s *Server) requestProfileModel(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "profile")
	if !managedProfile(name) {
		writeError(w, 422, "invalid_profile", "seuls les profils aos-* sont modifiables par Agentic OS")
		return
	}
	var req profileModelReq
	if !decode(w, r, &req) {
		return
	}
	req.Provider, req.Model = trim(req.Provider), trim(req.Model)
	if !domain.ValidProviderName(req.Provider) || !domain.ValidModelName(req.Model) {
		writeError(w, 422, "invalid_model", "fournisseur et modèle requis (noms Hermes)")
		return
	}
	profiles, err := s.managedProfiles()
	if err != nil {
		writeError(w, 503, "profiles_unavailable", "liste des profils Hermes indisponible")
		return
	}
	if !slices.Contains(profiles, name) {
		writeError(w, 404, "unknown_profile", "profil "+name+" : aucune clé secrets/hermes/"+name+".key")
		return
	}
	if ue := s.checkModelsAvailable(r.Context(), map[string]domain.ModelChoice{name: {Provider: req.Provider, Model: req.Model}}); ue != nil {
		s.failUser(w, r, ue)
		return
	}
	var pm *store.ProfileModel
	err = s.Store.InTx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if pm, err = store.RequestProfileModel(r.Context(), tx, name, req.Provider, req.Model, actorLogin(r)); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, audit.Entry{Actor: audit.User(actorLogin(r)), Action: "profile_model.requested",
			SubjectType: "system", After: map[string]any{"profile": name, "provider": req.Provider, "model": req.Model, "revision": pm.Revision}})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.profileObs.forget(name)
	writeJSON(w, 200, profileModelView{Profile: name, Role: profileRole(name), Usages: []profileUsage{},
		Desired: &modelRef{pm.DesiredProvider, pm.DesiredModel}, State: pm.State, Revision: pm.Revision,
		RequestedBy: pm.RequestedBy, RequestedAt: tsPtr(&pm.RequestedAt)})
}
