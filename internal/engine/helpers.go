package engine

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// featureEvent applique un événement à une feature (verrou optimiste + audit + notification).
func (e *Engine) featureEvent(ctx context.Context, tx pgx.Tx, f *store.Feature, ev domain.FeatureEvent, actor, reason string) (*store.Feature, error) {
	to, err := domain.ApplyFeature(domain.FeatureState(f.State), ev)
	if err != nil {
		return nil, err
	}
	prev := ""
	if to == domain.FeatureBlocked {
		prev = f.State
	}
	return e.setFeature(ctx, tx, f, string(to), prev, reason, actor, "feature."+string(ev))
}

func (e *Engine) setFeature(ctx context.Context, tx pgx.Tx, f *store.Feature, to, prev, reason, actor, action string) (*store.Feature, error) {
	nf, err := store.SetFeatureState(ctx, tx, f.ID, f.Version, to, prev, reason)
	if err != nil {
		return nil, err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor, Action: action, SubjectType: "feature", SubjectID: f.ID,
		Before: map[string]any{"state": f.State, "version": f.Version},
		After:  map[string]any{"state": nf.State, "version": nf.Version, "reason": reason}}); err != nil {
		return nil, err
	}
	if err := store.Notify(ctx, tx, "feature.updated", f.ID); err != nil {
		return nil, err
	}
	return nf, store.Notify(ctx, tx, "overview.invalidate", 0)
}

// ticketEvent applique un événement à un ticket.
func (e *Engine) ticketEvent(ctx context.Context, tx pgx.Tx, t *store.Ticket, ev domain.TicketEvent, actor, reason string) (*store.Ticket, error) {
	to, err := domain.ApplyTicket(domain.TicketState(t.State), ev)
	if err != nil {
		return nil, err
	}
	return e.setTicket(ctx, tx, t, to, ev, actor, reason, "")
}

func (e *Engine) setTicket(ctx context.Context, tx pgx.Tx, t *store.Ticket, to domain.TicketState, ev domain.TicketEvent, actor, reason, sha string) (*store.Ticket, error) {
	prev := ""
	if to == domain.TicketBlocked {
		prev = t.State
	}
	nt, err := store.SetTicketState(ctx, tx, t.ID, t.Version, string(to), prev, reason)
	if err != nil {
		return nil, err
	}
	if sha == "" && t.HeadSHA != nil {
		sha = *t.HeadSHA
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor, Action: "ticket." + string(ev), SubjectType: "ticket", SubjectID: t.ID,
		Before: map[string]any{"state": t.State, "version": t.Version},
		After:  map[string]any{"state": nt.State, "version": nt.Version, "reason": reason}, SHA: sha}); err != nil {
		return nil, err
	}
	if err := store.Notify(ctx, tx, "ticket.updated", t.ID); err != nil {
		return nil, err
	}
	return nt, store.Notify(ctx, tx, "overview.invalidate", 0)
}

// enqueue crée un job idempotent.
func (e *Engine) enqueue(ctx context.Context, tx pgx.Tx, j store.NewJob) (*store.Job, error) {
	job, created, err := store.EnqueueJob(ctx, tx, j)
	if err != nil {
		return nil, err
	}
	if created {
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "job.enqueued", SubjectType: j.SubjectType, SubjectID: j.SubjectID,
			After: map[string]any{"job_id": job.ID, "kind": job.Kind}}); err != nil {
			return nil, err
		}
		if err := store.Notify(ctx, tx, "job.updated", job.ID); err != nil {
			return nil, err
		}
	}
	return job, nil
}

func jobKey(kind, subjectType string, id int64, discriminator string) string {
	return fmt.Sprintf("%s:%s:%d:%s", kind, subjectType, id, discriminator)
}

// createApproval crée une approbation pending (idempotent par kind/sujet).
func (e *Engine) createApproval(ctx context.Context, tx pgx.Tx, a store.NewApproval) (*store.Approval, error) {
	ap, created, err := store.CreateApproval(ctx, tx, a)
	if err != nil {
		return nil, err
	}
	if created {
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "approval.requested", SubjectType: a.SubjectType, SubjectID: a.SubjectID,
			After: map[string]any{"approval_id": ap.ID, "kind": ap.Kind, "effect": ap.Effect}}); err != nil {
			return nil, err
		}
		if err := store.Notify(ctx, tx, "approval.updated", ap.ID); err != nil {
			return nil, err
		}
	}
	return ap, nil
}

// maybeCompleteFeature passe la feature à DONE si tous ses tickets sont terminaux.
func (e *Engine) maybeCompleteFeature(ctx context.Context, tx pgx.Tx, featureID int64) (bool, error) {
	f, err := store.LockFeature(ctx, tx, featureID)
	if err != nil {
		return false, err
	}
	if f.State != string(domain.FeatureReadyForDev) && f.State != string(domain.FeatureInDev) {
		return false, nil
	}
	tickets, err := store.ListTicketsByFeature(ctx, tx, featureID)
	if err != nil {
		return false, err
	}
	for _, t := range tickets {
		if !domain.TicketState(t.State).Terminal() {
			return false, nil
		}
	}
	_, err = e.featureEvent(ctx, tx, f, domain.FeatureEvAllTicketsDone, audit.System, "tous les tickets sont terminés")
	return err == nil, err
}

// projectOf renvoie le projet d'un job (nil pour sync_agents).
func (e *Engine) projectFor(ctx context.Context, q store.Q, j *store.Job) (*store.Project, error) {
	switch j.SubjectType {
	case "feature":
		f, err := store.GetFeature(ctx, q, j.SubjectID)
		if err != nil {
			return nil, err
		}
		return store.GetProject(ctx, q, f.ProjectID)
	case "ticket":
		t, err := store.GetTicket(ctx, q, j.SubjectID)
		if err != nil {
			return nil, err
		}
		return store.GetProject(ctx, q, t.ProjectID)
	}
	return nil, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
