package engine

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Un arrêt n'est jamais présumé : le run passe en « stopping » puis seul le statut relu
// chez Hermes le finalise (stopped, ou l'issue réelle s'il s'est terminé entre-temps).
// Tant que ce n'est pas confirmé, ReconcileStops retente l'arrêt.

// stopWithoutHermesGrace : délai avant de clore un run « stopping » qui n'a jamais reçu
// d'identifiant Hermes (soumission en cours au moment de l'arrêt).
const stopWithoutHermesGrace = 2 * time.Minute

// requestStop marque le run « stopping » dans la transaction de l'appelant.
func requestStop(ctx context.Context, tx pgx.Tx, runID int64, reason string) error {
	if _, err := store.RequestRunStop(ctx, tx, runID, reason); err != nil {
		return err
	}
	return store.Notify(ctx, tx, "run.updated", runID)
}

// tryStop envoie l'arrêt à Hermes puis relit le statut réel. Renvoie le statut du run après
// la tentative (stopping si l'arrêt n'est pas encore confirmé).
func (e *Engine) tryStop(ctx context.Context, runID int64) (string, error) {
	r, err := store.GetRun(ctx, e.Store.Pool, runID)
	if err != nil {
		return "", err
	}
	if r.Status != domain.RunStopping {
		return r.Status, nil
	}
	if r.HermesRunID == nil {
		if r.StopRequestedAt != nil && time.Since(*r.StopRequestedAt) > stopWithoutHermesGrace {
			if err := e.confirmStop(ctx, r, domain.RunStopped, hermes.RunStatus{}, "arrêté avant soumission à Hermes"); err != nil {
				return "", err
			}
			return domain.RunStopped, nil
		}
		return r.Status, nil
	}
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	stopErr := e.Hermes.StopRun(sctx, r.HermesProfile, *r.HermesRunID)
	cancel()
	msg := ""
	if stopErr != nil {
		msg = stopErr.Error()
	}
	if err := store.RecordStopAttempt(ctx, e.Store.Pool, r.ID, msg); err != nil {
		return "", err
	}
	gctx, gcancel := context.WithTimeout(ctx, 15*time.Second)
	st, getErr := e.Hermes.GetRun(gctx, r.HermesProfile, *r.HermesRunID)
	gcancel()
	switch {
	case errors.Is(getErr, hermes.ErrNotFound):
		return domain.RunLost, e.confirmStop(ctx, r, domain.RunLost, hermes.RunStatus{}, "run introuvable côté Hermes lors de l'arrêt")
	case getErr != nil:
		e.Log.Warn("statut Hermes illisible après arrêt : nouvelle tentative plus tard", "run_id", r.ID, "error", getErr.Error())
		return domain.RunStopping, nil
	case st.Terminal():
		final := st.NormalizedStatus()
		return final, e.confirmStop(ctx, r, final, st, "")
	}
	if stopErr != nil {
		e.Log.Warn("arrêt Hermes en échec : nouvelle tentative plus tard", "run_id", r.ID, "attempt", r.StopAttempts+1, "error", stopErr.Error())
	}
	return domain.RunStopping, nil
}

// confirmStop finalise le run avec le statut confirmé par Hermes.
func (e *Engine) confirmStop(ctx context.Context, r *store.Run, final string, st hermes.RunStatus, note string) error {
	return e.Store.InTx(ctx, func(tx pgx.Tx) error {
		cur, err := store.GetRun(ctx, tx, r.ID)
		if err != nil {
			return err
		}
		if cur.Status != domain.RunStopping {
			return nil
		}
		res := store.RunResult{Status: final, InputTokens: st.Usage.InputTokens, OutputTokens: st.Usage.OutputTokens,
			TotalTokens: st.Usage.TotalTokens, CacheReadTokens: st.Usage.CacheReadTokens,
			CacheWriteTokens: st.Usage.CacheWriteTokens, Model: st.Runtime.Model, Provider: st.Runtime.Provider, Output: st.Output,
			Error: firstNonEmpty(str(cur.Error), note, st.Error)}
		if err := store.FinishRun(ctx, tx, r.ID, res); err != nil {
			return err
		}
		j, err := store.GetJob(ctx, tx, r.JobID)
		if err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "run.stop_confirmed", SubjectType: j.SubjectType,
			SubjectID: j.SubjectID, RunID: &r.ID, After: map[string]any{"run_id": r.ID, "status": final, "stop_attempts": cur.StopAttempts}}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "run.updated", r.ID)
	})
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// ReconcileStops retente les arrêts non confirmés (toutes les 30 s).
func (e *Engine) ReconcileStops(ctx context.Context) error {
	e.Init()
	runs, err := store.ListStoppingRuns(ctx, e.Store.Pool)
	if err != nil {
		return err
	}
	for _, r := range runs {
		if _, err := e.tryStop(ctx, r.ID); err != nil {
			e.Log.Warn("réconciliation d'arrêt en échec", "run_id", r.ID, "error", err.Error())
		}
	}
	return nil
}
