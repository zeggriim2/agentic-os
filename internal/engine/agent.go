package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// agentOutcome est le résultat terminal d'un run Hermes.
type agentOutcome struct {
	Run    *store.Run
	Agent  *store.Agent
	Status hermes.RunStatus
}

// idempotencyKey = aos-job-<job_id>-a<attempt du sujet>.
func idempotencyKey(jobID int64, attempt int) string {
	return fmt.Sprintf("aos-job-%d-a%d", jobID, attempt)
}

// fallbackKey est la clé de la re-soumission sans surcharge de modèle (dérivée, donc rejouable).
func fallbackKey(jobID int64, attempt int) string {
	return idempotencyKey(jobID, attempt) + "-fallback"
}

// AuditRunModelFallback est l'action d'audit écrite lors d'un repli sur le modèle par défaut du profil.
const AuditRunModelFallback = "run.model_fallback"

// resolveModel applique l'ordre de résolution : modèle du projet pour le rôle → modèle souhaité de
// l'agent (sans fournisseur) → aucun (défaut du profil Hermes).
func resolveModel(p *store.Project, role string, agent *store.Agent) domain.ModelChoice {
	desired := ""
	if agent != nil {
		desired = agent.DesiredModel
	}
	return p.ModelFor(role, desired)
}

// requestedOf relit la surcharge persistée sur le run (vide si aucune).
func requestedOf(run *store.Run) domain.ModelChoice {
	var m domain.ModelChoice
	if run.RequestedModel != nil {
		m.Model = *run.RequestedModel
	}
	if run.RequestedProvider != nil {
		m.Provider = *run.RequestedProvider
	}
	return m
}

// submit soumet le run à Hermes et persiste l'identifiant renvoyé.
func (e *Engine) submit(ctx context.Context, j *store.Job, run *store.Run, role, key string, req hermes.RunRequest) error {
	if err := e.fence(ctx, "soumission du run Hermes"); err != nil {
		return err
	}
	res, err := e.Hermes.CreateRun(ctx, run.HermesProfile, key, req)
	if err != nil {
		if errors.Is(err, hermes.ErrConflict) {
			_ = store.SetRunStatus(ctx, e.Store.Pool, run.ID, domain.RunFailed, "conflit de clé d'idempotence")
			return permanent(domain.ErrClassInternal, "conflit de clé d'idempotence Hermes (payload différent)")
		}
		return err
	}
	if run.ModelFallback {
		if err := store.MarkRunModelFallback(ctx, e.Store.Pool, run.ID, res.RunID); err != nil {
			return err
		}
	} else if err := store.SetRunHermesID(ctx, e.Store.Pool, run.ID, res.RunID); err != nil {
		return err
	}
	_ = audit.Write(ctx, e.Store.Pool, audit.Entry{Actor: "agent:" + role, Action: "run.submitted", SubjectType: j.SubjectType,
		SubjectID: j.SubjectID, After: map[string]any{"run_id": run.ID, "replayed": res.Replayed, "model_fallback": run.ModelFallback,
			"requested_model": req.Model, "requested_provider": req.Provider}, RunID: &run.ID})
	id := res.RunID
	run.HermesRunID = &id
	run.Status = domain.RunRunning
	_ = store.Notify(ctx, e.Store.Pool, "run.updated", run.ID)
	return nil
}

// runAgent soumet (ou rattache) le run Hermes du job puis attend son état terminal.
// Un job qui a déjà un hermes_run_id ne resoumet jamais : il se rattache au run existant.
func (e *Engine) runAgent(ctx context.Context, j *store.Job, p *store.Project, role string, subjectAttempt int, req hermes.RunRequest) (*agentOutcome, error) {
	agent, err := store.GetAgent(ctx, e.Store.Pool, role)
	if err != nil {
		return nil, err
	}
	if !agent.Enabled {
		return nil, permanent(domain.ErrClassPolicy, "agent %s désactivé", role)
	}
	run, err := store.LatestRunForJob(ctx, e.Store.Pool, j.ID)
	if err != nil {
		return nil, err
	}
	if run != nil && domain.RunTerminal(run.Status) && run.HermesRunID == nil {
		run = nil
	}
	if run == nil {
		// Profil propre au projet s'il en définit un (image et montages du projet), sinon celui de l'agent.
		if run, err = store.CreateRun(ctx, e.Store.Pool, j.ID, role, p.ProfileFor(role, agent.HermesProfile)); err != nil {
			return nil, err
		}
		// Surcharge de modèle figée à la création : la clé d'idempotence couvre ce corps exact.
		if m := resolveModel(p, role, agent); !m.Empty() {
			if err := store.SetRunRequestedModel(ctx, e.Store.Pool, run.ID, m); err != nil {
				return nil, err
			}
			run.RequestedModel, run.RequestedProvider = &m.Model, &m.Provider
		}
		_ = store.Notify(ctx, e.Store.Pool, "run.updated", run.ID)
	}
	profile := run.HermesProfile
	requested := requestedOf(run)
	if run.HermesRunID == nil {
		key := idempotencyKey(j.ID, subjectAttempt)
		if run.ModelFallback {
			key = fallbackKey(j.ID, subjectAttempt)
		} else {
			req.Model, req.Provider = requested.Model, requested.Provider
		}
		if err := e.submit(ctx, j, run, role, key, req); err != nil {
			return nil, err
		}
	} else {
		e.Log.Info("rattachement au run Hermes existant", "run_id", run.ID, "job_id", j.ID)
	}
	for {
		st, err := e.awaitRun(ctx, run, profile)
		if err != nil {
			return nil, err
		}
		// Repli (une seule fois) : fournisseur demandé non résolu par Hermes → re-soumission sans
		// surcharge, sous une clé dérivée, le run gardant la surcharge demandée pour l'affichage.
		if !requested.Empty() && !run.ModelFallback && hermes.IsProviderResolutionFailure(st) {
			e.Log.Warn("modèle demandé refusé par Hermes, repli sur le défaut du profil", "run_id", run.ID,
				"requested_provider", requested.Provider, "requested_model", requested.Model, "error", st.Error)
			run.ModelFallback = true
			req.Model, req.Provider = "", ""
			if err := e.submit(ctx, j, run, role, fallbackKey(j.ID, subjectAttempt), req); err != nil {
				return nil, err
			}
			_ = audit.Write(ctx, e.Store.Pool, audit.Entry{Actor: audit.System, Action: AuditRunModelFallback, SubjectType: "run",
				SubjectID: run.ID, RunID: &run.ID, After: map[string]any{
					"requested": map[string]string{"provider": requested.Provider, "model": requested.Model},
					"served":    "défaut du profil " + profile,
					"reason":    truncate(st.Error, 500),
				}})
			continue
		}
		res := store.RunResult{
			Status: st.NormalizedStatus(), InputTokens: st.Usage.InputTokens, OutputTokens: st.Usage.OutputTokens,
			TotalTokens: st.Usage.TotalTokens, CacheReadTokens: st.Usage.CacheReadTokens,
			CacheWriteTokens: st.Usage.CacheWriteTokens, Model: st.Runtime.Model, Provider: st.Runtime.Provider, Output: st.Output,
			Error: st.Error,
		}
		if err := store.FinishRun(ctx, e.Store.Pool, run.ID, res); err != nil {
			return nil, err
		}
		_ = store.Notify(ctx, e.Store.Pool, "run.updated", run.ID)
		fresh, err := store.GetRun(ctx, e.Store.Pool, run.ID)
		if err != nil {
			return nil, err
		}
		return &agentOutcome{Run: fresh, Agent: agent, Status: st}, nil
	}
}

// awaitRun suit le run Hermes (flux SSE best effort, le statut fait foi) jusqu'à son état terminal.
func (e *Engine) awaitRun(ctx context.Context, run *store.Run, profile string) (hermes.RunStatus, error) {
	hid := *run.HermesRunID

	// flux d'événements (best effort) ; le statut fait foi.
	sctx, stopStream := context.WithCancel(ctx)
	streamDone := make(chan struct{})
	go func() {
		defer close(streamDone)
		_ = e.Hermes.StreamEvents(sctx, profile, hid, func(ev hermes.Event) error {
			if ev.IsDelta() {
				return nil
			}
			if ins, err := store.InsertRunEvent(sctx, e.Store.Pool, run.ID, ev.Seq, ev.Event, ev.Raw); err == nil && ins {
				_ = store.NotifyRunEvent(sctx, e.Store.Pool, run.ID, ev.Seq)
			}
			return nil
		})
	}()
	defer func() { stopStream(); <-streamDone }()

	deadline := time.Now().Add(e.RunTimeout)
	failures := 0
	for {
		st, err := e.Hermes.GetRun(ctx, profile, hid)
		switch {
		case errors.Is(err, hermes.ErrNotFound):
			_ = store.SetRunStatus(context.Background(), e.Store.Pool, run.ID, domain.RunLost, "run introuvable côté Hermes")
			_ = store.Notify(ctx, e.Store.Pool, "run.updated", run.ID)
			return hermes.RunStatus{}, permanent(domain.ErrClassProvider, "run Hermes perdu (404)")
		case err != nil:
			if ctx.Err() != nil {
				return hermes.RunStatus{}, ctx.Err()
			}
			failures++
			if failures >= 10 {
				return hermes.RunStatus{}, retryable(hermes.ClassOf(err), "lecture du run impossible : %v", err)
			}
		case st.Terminal():
			// laisser le flux SSE se terminer (il se ferme après run.completed/run.failed)
			select {
			case <-streamDone:
			case <-time.After(2 * time.Second):
			}
			return st, nil
		default:
			failures = 0
			if run.Status != domain.RunRunning {
				_ = store.SetRunStatus(ctx, e.Store.Pool, run.ID, domain.RunRunning, "")
				run.Status = domain.RunRunning
			}
		}
		if time.Now().After(deadline) {
			bg := context.Background()
			if _, err := store.RequestRunStop(bg, e.Store.Pool, run.ID, "délai maximal du run dépassé"); err == nil {
				_, _ = e.tryStop(bg, run.ID)
			}
			return hermes.RunStatus{}, permanent(domain.ErrClassTimeout, "délai maximal du run dépassé")
		}
		select {
		case <-ctx.Done():
			return hermes.RunStatus{}, ctx.Err()
		case <-time.After(e.PollInterval):
		}
	}
}

// evaluate applique les règles fail-closed : run completed non partiel/interrompu, livrable valide,
// budget. Le livrable valide est stocké même si le budget est dépassé (puis rejeté).
func (e *Engine) evaluate(ctx context.Context, o *agentOutcome, parse func(string) (any, error)) (any, error) {
	if err := domain.RunOutcomeAcceptable(o.Status.NormalizedStatus(), o.Status.Partial, o.Status.Interrupted); err != nil {
		if o.Status.NormalizedStatus() == domain.RunCompleted {
			return nil, permanent(domain.ErrClassInvalidOutput, "%v", err)
		}
		return nil, permanent(domain.ErrClassProvider, "run Hermes terminé en %s", o.Status.Status)
	}
	art, err := parse(o.Status.Output)
	if err != nil {
		_, _ = e.Store.Pool.Exec(ctx, `UPDATE runs SET error=$2, updated_at=now() WHERE id=$1`, o.Run.ID, truncate(err.Error(), 2000))
		_ = store.Notify(ctx, e.Store.Pool, "run.updated", o.Run.ID)
		return nil, permanent(domain.ErrClassInvalidOutput, "%v", err)
	}
	if _, err := e.Store.Pool.Exec(ctx, `UPDATE runs SET artifact=$2, updated_at=now() WHERE id=$1`, o.Run.ID, store.JSON(art)); err != nil {
		return nil, err
	}
	// Le run est déjà notifié « terminé » : renotifier pour que la vue en direct affiche le livrable.
	_ = store.Notify(ctx, e.Store.Pool, "run.updated", o.Run.ID)
	if o.Status.Usage.TotalTokens > int64(o.Agent.MaxTokensPerRun) {
		msg := fmt.Sprintf("budget dépassé : %d tokens > %d", o.Status.Usage.TotalTokens, o.Agent.MaxTokensPerRun)
		_, _ = e.Store.Pool.Exec(ctx, `UPDATE runs SET error=$2, updated_at=now() WHERE id=$1`, o.Run.ID, msg)
		return nil, permanent(domain.ErrClassBudget, "%s", msg)
	}
	return art, nil
}
