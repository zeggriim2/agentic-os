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

// runAgent soumet (ou rattache) le run Hermes du job puis attend son état terminal.
// Un job qui a déjà un hermes_run_id ne resoumet jamais : il se rattache au run existant.
func (e *Engine) runAgent(ctx context.Context, j *store.Job, role string, subjectAttempt int, req hermes.RunRequest) (*agentOutcome, error) {
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
		if run, err = store.CreateRun(ctx, e.Store.Pool, j.ID, role, agent.HermesProfile); err != nil {
			return nil, err
		}
		_ = store.Notify(ctx, e.Store.Pool, "run.updated", run.ID)
	}
	profile := run.HermesProfile
	if run.HermesRunID == nil {
		if err := e.fence(ctx, "soumission du run Hermes"); err != nil {
			return nil, err
		}
		res, err := e.Hermes.CreateRun(ctx, profile, idempotencyKey(j.ID, subjectAttempt), req)
		if err != nil {
			if errors.Is(err, hermes.ErrConflict) {
				_ = store.SetRunStatus(ctx, e.Store.Pool, run.ID, domain.RunFailed, "conflit de clé d'idempotence")
				return nil, permanent(domain.ErrClassInternal, "conflit de clé d'idempotence Hermes (payload différent)")
			}
			return nil, err
		}
		if err := store.SetRunHermesID(ctx, e.Store.Pool, run.ID, res.RunID); err != nil {
			return nil, err
		}
		_ = audit.Write(ctx, e.Store.Pool, audit.Entry{Actor: "agent:" + role, Action: "run.submitted", SubjectType: j.SubjectType,
			SubjectID: j.SubjectID, After: map[string]any{"run_id": run.ID, "replayed": res.Replayed}, RunID: &run.ID})
		id := res.RunID
		run.HermesRunID = &id
		run.Status = domain.RunRunning
		_ = store.Notify(ctx, e.Store.Pool, "run.updated", run.ID)
	} else {
		e.Log.Info("rattachement au run Hermes existant", "run_id", run.ID, "job_id", j.ID)
	}
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
			return nil, permanent(domain.ErrClassProvider, "run Hermes perdu (404)")
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			failures++
			if failures >= 10 {
				return nil, retryable(hermes.ClassOf(err), "lecture du run impossible : %v", err)
			}
		case st.Terminal():
			// laisser le flux SSE se terminer (il se ferme après run.completed/run.failed)
			select {
			case <-streamDone:
			case <-time.After(2 * time.Second):
			}
			res := store.RunResult{
				Status: st.NormalizedStatus(), InputTokens: st.Usage.InputTokens, OutputTokens: st.Usage.OutputTokens,
				TotalTokens: st.Usage.TotalTokens, Model: st.Runtime.Model, Provider: st.Runtime.Provider, Output: st.Output,
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
		default:
			failures = 0
			if run.Status != domain.RunRunning {
				_ = store.SetRunStatus(ctx, e.Store.Pool, run.ID, domain.RunRunning, "")
				run.Status = domain.RunRunning
			}
		}
		if time.Now().After(deadline) {
			_ = e.Hermes.StopRun(context.Background(), profile, hid)
			_ = store.SetRunStatus(context.Background(), e.Store.Pool, run.ID, domain.RunStopped, "délai maximal du run dépassé")
			return nil, permanent(domain.ErrClassTimeout, "délai maximal du run dépassé")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
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
		return nil, permanent(domain.ErrClassInvalidOutput, "%v", err)
	}
	if _, err := e.Store.Pool.Exec(ctx, `UPDATE runs SET artifact=$2, updated_at=now() WHERE id=$1`, o.Run.ID, store.JSON(art)); err != nil {
		return nil, err
	}
	if o.Status.Usage.TotalTokens > int64(o.Agent.MaxTokensPerRun) {
		msg := fmt.Sprintf("budget dépassé : %d tokens > %d", o.Status.Usage.TotalTokens, o.Agent.MaxTokensPerRun)
		_, _ = e.Store.Pool.Exec(ctx, `UPDATE runs SET error=$2, updated_at=now() WHERE id=$1`, o.Run.ID, msg)
		return nil, permanent(domain.ErrClassBudget, "%s", msg)
	}
	return art, nil
}
