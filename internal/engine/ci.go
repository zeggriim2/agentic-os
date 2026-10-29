package engine

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// PollPRs réconcilie les tickets PR_OPENED/HUMAN_MERGE (poller de secours, toutes les 60 s).
func (e *Engine) PollPRs(ctx context.Context) error {
	e.Init()
	tickets, err := store.ListTicketsByStates(ctx, e.Store.Pool, []string{string(domain.TicketPROpened), string(domain.TicketHumanMerge)})
	if err != nil {
		return err
	}
	for _, t := range tickets {
		if err := e.ReconcilePR(ctx, t.ID); err != nil {
			e.Log.Warn("réconciliation PR en échec", "ticket_id", t.ID, "error", err.Error())
		}
	}
	features, err := store.ListFeaturesAwaitingPR(ctx, e.Store.Pool)
	if err != nil {
		return err
	}
	for _, f := range features {
		if err := e.ReconcileFeaturePR(ctx, f.ID); err != nil {
			e.Log.Warn("réconciliation PR finale en échec", "feature_id", f.ID, "error", err.Error())
		}
	}
	return nil
}

// ReconcilePR lit l'état de la PR et des checks sur le head_sha exact, puis applique
// ci_success / pr_merged / pr_closed.
func (e *Engine) ReconcilePR(ctx context.Context, ticketID int64) error {
	e.Init()
	t, err := store.GetTicket(ctx, e.Store.Pool, ticketID)
	if err != nil {
		return err
	}
	if t.PRNumber == nil || t.HeadSHA == nil {
		return nil
	}
	if t.State != string(domain.TicketPROpened) && t.State != string(domain.TicketHumanMerge) {
		return nil
	}
	p, err := store.GetProject(ctx, e.Store.Pool, t.ProjectID)
	if err != nil {
		return err
	}
	pr, err := e.GitHub.GetPR(ctx, p.Repository, *t.PRNumber)
	if err != nil {
		return err
	}
	ci := t.CIState
	var evidence any
	if pr.State == "open" {
		if pr.Head.SHA != *t.HeadSHA {
			ci = domain.CIUnverified // la PR a avancé hors d'Agentic OS : aucune conclusion sur le SHA validé
		} else {
			st, ev, err := e.GitHub.CIState(ctx, p.Repository, *t.HeadSHA, p.RequiredChecks)
			if err != nil {
				return err
			}
			ci, evidence = st, ev
		}
	}
	var notice string
	err = e.Store.InTx(ctx, func(tx pgx.Tx) error {
		lt, err := store.LockTicket(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if lt.State != t.State || lt.Version != t.Version {
			return nil
		}
		if ci != lt.CIState {
			if err := store.SetTicketCI(ctx, tx, lt.ID, ci); err != nil {
				return err
			}
			if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "ticket.ci_observed", SubjectType: "ticket", SubjectID: lt.ID,
				Before: map[string]any{"ci_state": lt.CIState}, After: map[string]any{"ci_state": ci, "checks": evidence}, SHA: *lt.HeadSHA}); err != nil {
				return err
			}
			if err := store.Notify(ctx, tx, "ticket.updated", lt.ID); err != nil {
				return err
			}
		}
		switch {
		case pr.Merged:
			if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvPRMerged, audit.System, "PR mergée"); err != nil {
				return err
			}
			notice = fmt.Sprintf("🎉 PR #%d de %s mergée.", pr.Number, lt.Key)
			done, err := e.maybeCompleteFeature(ctx, tx, lt.FeatureID)
			if err != nil || !done {
				return err
			}
			if lf, err := store.GetFeature(ctx, tx, lt.FeatureID); err == nil && lf.State == string(domain.FeatureAcceptance) {
				notice += fmt.Sprintf("\n🧪 Tous les tickets de « %s » sont intégrés dans %s : approbation de la PR finale requise, puis recette.",
					lf.Title, str(lf.FeatureBranch))
			}
			return nil
		case pr.State == "closed":
			if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvPRClosed, audit.System, "PR fermée sans merge"); err != nil {
				return err
			}
			notice = fmt.Sprintf("🚫 PR #%d de %s fermée sans merge : ticket annulé.", pr.Number, lt.Key)
			_, err = e.maybeCompleteFeature(ctx, tx, lt.FeatureID)
			return err
		case ci == domain.CISuccess && lt.State == string(domain.TicketPROpened):
			if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvCISuccess, audit.System, "CI verte sur "+short(*lt.HeadSHA)); err != nil {
				return err
			}
			notice = fmt.Sprintf("🟢 CI verte pour %s (PR #%d) : merge humain attendu.", lt.Key, pr.Number)
		}
		return nil
	})
	if err == nil && notice != "" {
		e.notifyProject(ctx, t.ProjectID, notice)
	}
	return err
}

// HandleGitHubEvent réconcilie les tickets concernés par un webhook (déjà authentifié et dédupliqué).
func (e *Engine) HandleGitHubEvent(ctx context.Context, repository string, prNumber int, sha, branch string) (int, error) {
	tickets, err := store.FindTicketsForGitHub(ctx, e.Store.Pool, repository, prNumber, sha, branch)
	if err != nil {
		return 0, err
	}
	for _, t := range tickets {
		if err := e.ReconcilePR(ctx, t.ID); err != nil {
			return 0, err
		}
	}
	features, err := store.FeaturesByPR(ctx, e.Store.Pool, repository, prNumber, branch)
	if err != nil {
		return 0, err
	}
	for _, f := range features {
		if err := e.ReconcileFeaturePR(ctx, f.ID); err != nil {
			return 0, err
		}
	}
	return len(tickets) + len(features), nil
}
