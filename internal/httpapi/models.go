package httpapi

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// ModelCatalog lit le catalogue des modèles d'un profil Hermes (GET /p/<profil>/api/model/options).
type ModelCatalog interface {
	ModelOptions(ctx context.Context, profile string) (hermes.ModelOptions, error)
}

// modelCatalogTTL borne la durée du cache mémoire du catalogue (il change rarement).
const modelCatalogTTL = 60 * time.Second

// modelProviderView est un fournisseur du catalogue exposé à l'UI : aucun champ secret, aucun avertissement
// interne, seulement ce qu'une liste déroulante affiche.
type modelProviderView struct {
	Provider  string   `json:"provider"`
	Name      string   `json:"name"`
	IsCurrent bool     `json:"is_current"`
	Models    []string `json:"models"`
}

func viewModelProviders(o hermes.ModelOptions) []modelProviderView {
	out := []modelProviderView{}
	for _, p := range o.Authenticated() {
		models := slices.Clone(p.Models)
		sort.Strings(models)
		out = append(out, modelProviderView{Provider: p.Slug, Name: p.Name, IsCurrent: p.IsCurrent, Models: models})
	}
	return out
}

// catalogProfile choisit le profil qui sert à lire le catalogue : celui de l'agent PO, sinon le premier
// profil dont la clé existe. Le catalogue reflète l'hôte Hermes, il est le même pour tous les profils.
func (s *Server) catalogProfile(ctx context.Context) string {
	if a, err := store.GetAgent(ctx, s.Store.Pool, domain.RolePO); err == nil && a.HermesProfile != "" {
		return a.HermesProfile
	}
	if s.HermesProfiles != nil {
		if names, err := s.HermesProfiles(); err == nil && len(names) > 0 {
			return names[0]
		}
	}
	return ""
}

// modelCatalog renvoie le catalogue (cache 60 s). Erreur = Hermes injoignable ou non configuré.
func (s *Server) modelCatalog(ctx context.Context) (hermes.ModelOptions, error) {
	s.modelsMu.Lock()
	defer s.modelsMu.Unlock()
	if s.modelsCache != nil && time.Since(s.modelsAt) < modelCatalogTTL {
		return *s.modelsCache, nil
	}
	if s.Models == nil {
		return hermes.ModelOptions{}, &hermes.Error{Class: domain.ErrClassProvider, Msg: "catalogue des modèles non configuré"}
	}
	profile := s.catalogProfile(ctx)
	if profile == "" {
		return hermes.ModelOptions{}, &hermes.Error{Class: domain.ErrClassProvider, Msg: "aucun profil Hermes pour lire le catalogue"}
	}
	o, err := s.Models.ModelOptions(ctx, profile)
	if err != nil {
		return hermes.ModelOptions{}, err
	}
	s.modelsCache, s.modelsAt = &o, time.Now()
	return o, nil
}

// listHermesModels : GET /api/hermes/models — fournisseurs authentifiés et leurs modèles, pour les
// listes déroulantes du formulaire projet.
func (s *Server) listHermesModels(w http.ResponseWriter, r *http.Request) {
	o, err := s.modelCatalog(r.Context())
	if err != nil {
		writeError(w, 503, "hermes_unreachable", "catalogue des modèles Hermes indisponible : "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": viewModelProviders(o), "next_cursor": nil})
}

// validateAgentModels normalise rôle → {provider, model} : rôles connus, noms valides ; une entrée
// vide (provider et model vides) vaut « défaut » et disparaît.
func validateAgentModels(in map[string]domain.ModelChoice) (map[string]domain.ModelChoice, string, string) {
	out := map[string]domain.ModelChoice{}
	for role, m := range in {
		m.Provider, m.Model = trim(m.Provider), trim(m.Model)
		switch {
		case !slices.Contains(domain.Roles, role):
			return nil, "invalid_agent_models", "rôle inconnu dans agent_models : " + role
		case m.Empty():
			continue
		case m.Provider == "" || m.Model == "":
			return nil, "invalid_agent_models", "fournisseur et modèle sont requis ensemble pour " + role
		case !domain.ValidProviderName(m.Provider):
			return nil, "invalid_agent_models", "fournisseur invalide pour " + role
		case !domain.ValidModelName(m.Model):
			return nil, "invalid_agent_models", "modèle invalide pour " + role
		}
		out[role] = m
	}
	return out, "", ""
}

// checkModelsAvailable refuse un modèle absent du catalogue Hermes (fournisseurs authentifiés). Hermes
// injoignable → refus explicite plutôt qu'une acceptation aveugle.
func (s *Server) checkModelsAvailable(ctx context.Context, models map[string]domain.ModelChoice) *userError {
	if len(models) == 0 {
		return nil
	}
	o, err := s.modelCatalog(ctx)
	if err != nil {
		return &userError{503, "hermes_unreachable", "catalogue des modèles Hermes indisponible, réglage refusé : " + err.Error()}
	}
	for role, m := range models {
		if !o.Has(m.Provider, m.Model) {
			known := []string{}
			for _, p := range o.Authenticated() {
				known = append(known, p.Slug)
			}
			sort.Strings(known)
			return &userError{422, "invalid_agent_models", "modèle " + m.Provider + "/" + m.Model + " (" + role +
				") absent du catalogue Hermes (fournisseurs authentifiés : " + strings.Join(known, ", ") + ")"}
		}
	}
	return nil
}
