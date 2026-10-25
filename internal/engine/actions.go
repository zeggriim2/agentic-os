package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

func jsonUnmarshal(b []byte, v any) error {
	if len(b) == 0 {
		return errors.New("vide")
	}
	return json.Unmarshal(b, v)
}

// runsToStop collecte les runs actifs à arrêter après commit.
type runRef struct {
	ID      int64
	Profile string
	Hermes  string
}

// stopRuns tente l'arrêt des runs déjà marqués « stopping » ; un échec est retenté par
// ReconcileStops, jamais présumé réussi.
func (e *Engine) stopRuns(ctx context.Context, runs []runRef) {
	for _, r := range runs {
		if _, err := e.tryStop(ctx, r.ID); err != nil {
			e.Log.Warn("arrêt du run en échec, nouvelle tentative plus tard", "run_id", r.ID, "error", err.Error())
		}
	}
}

// collectRuns marque « stopping » les runs actifs des sujets (dans la transaction
// d'annulation) et renvoie ceux à arrêter après commit.
func collectRuns(ctx context.Context, tx pgx.Tx, subjectType string, ids []int64) ([]runRef, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rs, err := store.ListActiveRunsForSubjects(ctx, tx, subjectType, ids)
	if err != nil {
		return nil, err
	}
	out := make([]runRef, 0, len(rs))
	for _, r := range rs {
		if err := requestStop(ctx, tx, r.ID, "arrêté (annulation)"); err != nil {
			return nil, err
		}
		out = append(out, runRef{ID: r.ID, Profile: r.HermesProfile, Hermes: str(r.HermesRunID)})
	}
	return out, nil
}

// FeatureAction applique start_spec | cancel | retry.
func (e *Engine) FeatureAction(ctx context.Context, id int64, action, login string) (*store.Feature, error) {
	e.Init()
	actor := audit.User(login)
	var toStop []runRef
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		f, err := store.LockFeature(ctx, tx, id)
		if err != nil {
			return err
		}
		p, err := store.GetProject(ctx, tx, f.ProjectID)
		if err != nil {
			return err
		}
		switch action {
		case "start_spec":
			if !p.Enabled {
				return userErr(422, "project_disabled", "le projet %s est désactivé", p.Slug)
			}
			if _, err := e.featureEvent(ctx, tx, f, domain.FeatureEvStartSpec, actor, ""); err != nil {
				return err
			}
			_, err = e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobPOSpec, SubjectType: "feature", SubjectID: f.ID, ProjectID: &f.ProjectID,
				IdempotencyKey: jobKey(domain.JobPOSpec, "feature", f.ID, "start")})
			return err
		case "cancel":
			if _, err := e.featureEvent(ctx, tx, f, domain.FeatureEvCancel, actor, "annulée par l'utilisateur"); err != nil {
				return err
			}
			tickets, err := store.ListTicketsByFeature(ctx, tx, f.ID)
			if err != nil {
				return err
			}
			var tids []int64
			for i := range tickets {
				tids = append(tids, tickets[i].ID)
				if domain.TicketState(tickets[i].State).Terminal() {
					continue
				}
				if _, err := e.ticketEvent(ctx, tx, &tickets[i], domain.TicketEvCancel, actor, "feature annulée"); err != nil {
					return err
				}
			}
			if toStop, err = collectRuns(ctx, tx, "feature", []int64{f.ID}); err != nil {
				return err
			}
			more, err := collectRuns(ctx, tx, "ticket", tids)
			if err != nil {
				return err
			}
			toStop = append(toStop, more...)
			if _, err := store.CancelJobs(ctx, tx, "feature", []int64{f.ID}); err != nil {
				return err
			}
			if _, err := store.CancelJobs(ctx, tx, "ticket", tids); err != nil {
				return err
			}
			if _, err := store.ExpirePendingApprovals(ctx, tx, "feature", []int64{f.ID}); err != nil {
				return err
			}
			_, err = store.ExpirePendingApprovals(ctx, tx, "ticket", tids)
			return err
		case "retry":
			prev := domain.FeatureState(str(f.PreviousState))
			to, err := domain.ResumeFeature(domain.FeatureState(f.State), prev)
			if err != nil {
				return err
			}
			if !p.Enabled {
				return userErr(422, "project_disabled", "le projet %s est désactivé", p.Slug)
			}
			nf, err := e.setFeature(ctx, tx, f, string(to), "", "", actor, "feature.retry")
			if err != nil {
				return err
			}
			kind := map[domain.FeatureState]string{domain.FeatureSpec: domain.JobPOSpec, domain.FeatureTechDesign: domain.JobTechDesign,
				domain.FeatureIssuesOpened: domain.JobOpenIssues}[to]
			_, err = e.enqueue(ctx, tx, store.NewJob{Kind: kind, SubjectType: "feature", SubjectID: f.ID, ProjectID: &f.ProjectID,
				IdempotencyKey: jobKey(kind, "feature", f.ID, fmt.Sprintf("retry-v%d", nf.Version))})
			return err
		}
		return userErr(422, "invalid_action", "action inconnue %q", action)
	})
	if err != nil {
		return nil, err
	}
	e.stopRuns(ctx, toStop)
	return store.GetFeature(ctx, e.Store.Pool, id)
}

// TicketAction applique cancel | retry.
func (e *Engine) TicketAction(ctx context.Context, id int64, action, login string) (*store.Ticket, error) {
	e.Init()
	actor := audit.User(login)
	var toStop []runRef
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		t, err := store.LockTicket(ctx, tx, id)
		if err != nil {
			return err
		}
		switch action {
		case "cancel":
			if _, err := e.ticketEvent(ctx, tx, t, domain.TicketEvCancel, actor, "annulé par l'utilisateur"); err != nil {
				return err
			}
			if toStop, err = collectRuns(ctx, tx, "ticket", []int64{t.ID}); err != nil {
				return err
			}
			if _, err := store.CancelJobs(ctx, tx, "ticket", []int64{t.ID}); err != nil {
				return err
			}
			if _, err := store.ExpirePendingApprovals(ctx, tx, "ticket", []int64{t.ID}); err != nil {
				return err
			}
			_, err = e.maybeCompleteFeature(ctx, tx, t.FeatureID)
			return err
		case "retry":
			prev := domain.TicketState(str(t.PreviousState))
			to, err := domain.ResumeTicket(domain.TicketState(t.State), prev)
			if err != nil {
				return err
			}
			p, err := store.GetProject(ctx, tx, t.ProjectID)
			if err != nil {
				return err
			}
			if !p.Enabled {
				return userErr(422, "project_disabled", "le projet %s est désactivé", p.Slug)
			}
			nt, err := e.setTicket(ctx, tx, t, to, domain.TicketEvRetry, actor, "", "")
			if err != nil {
				return err
			}
			if to == domain.TicketChangesRequested {
				return e.reworkApproval(ctx, tx, nt, "retry", "reprise après blocage", "", str(nt.HeadSHA))
			}
			kind := map[domain.TicketState]string{domain.TicketReadyForDev: domain.JobDevelop, domain.TicketDev: domain.JobDevelop,
				domain.TicketReview: domain.JobReview, domain.TicketQA: domain.JobQA, domain.TicketPROpened: domain.JobPublishPR}[to]
			_, err = e.enqueue(ctx, tx, store.NewJob{Kind: kind, SubjectType: "ticket", SubjectID: t.ID, ProjectID: &t.ProjectID,
				IdempotencyKey: jobKey(kind, "ticket", t.ID, fmt.Sprintf("retry-v%d", nt.Version))})
			return err
		}
		return userErr(422, "invalid_action", "action inconnue %q", action)
	})
	if err != nil {
		return nil, err
	}
	e.stopRuns(ctx, toStop)
	return store.GetTicket(ctx, e.Store.Pool, id)
}

// StopRun arrête un run : stop Hermes, job cancelled, sujet BLOCKED (raison « arrêté »).
func (e *Engine) StopRun(ctx context.Context, runID int64, login string) (*store.Run, error) {
	e.Init()
	r, err := store.GetRun(ctx, e.Store.Pool, runID)
	if err != nil {
		return nil, err
	}
	if domain.RunTerminal(r.Status) {
		return nil, userErr(409, "run_finished", "le run est déjà terminé")
	}
	actor := audit.User(login)
	var notice string
	// 1) arrêt demandé, job annulé et sujet bloqué atomiquement ; 2) arrêt envoyé à Hermes et
	// statut relu. Si Hermes ne confirme pas, le run reste « stopping » et ReconcileStops retente.
	err = e.Store.InTx(ctx, func(tx pgx.Tx) error {
		if err := requestStop(ctx, tx, r.ID, "arrêté par "+actor); err != nil {
			return err
		}
		j, err := store.GetJob(ctx, tx, r.JobID)
		if err != nil {
			return err
		}
		if err := store.CancelJob(ctx, tx, j.ID); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor, Action: "run.stop_requested", SubjectType: j.SubjectType, SubjectID: j.SubjectID,
			RunID: &r.ID, After: map[string]any{"run_id": r.ID, "job_id": j.ID}}); err != nil {
			return err
		}
		notice, err = e.blockSubject(ctx, tx, j, "arrêté")
		if err != nil {
			return err
		}
		if err := store.Notify(ctx, tx, "job.updated", j.ID); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "run.updated", r.ID)
	})
	if err != nil {
		return nil, err
	}
	if notice != "" {
		e.Notifier.Notify(ctx, notice)
	}
	if _, err := e.tryStop(ctx, r.ID); err != nil {
		e.Log.Warn("arrêt du run non confirmé, nouvelle tentative plus tard", "run_id", r.ID, "error", err.Error())
	}
	return store.GetRun(ctx, e.Store.Pool, runID)
}

// EnqueueSync crée un job sync_agents.
func (e *Engine) EnqueueSync(ctx context.Context, login string) (*store.Job, error) {
	var j *store.Job
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		j, err = e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobSyncAgents, SubjectType: "system", SubjectID: 0, MaxAttempts: 1,
			IdempotencyKey: fmt.Sprintf("sync_agents:%d", time.Now().UnixNano())})
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{Actor: audit.User(login), Action: "agents.sync_requested", SubjectType: "system",
			After: map[string]any{"job_id": j.ID}})
	})
	return j, err
}
