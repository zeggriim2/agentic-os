package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// CorrectionRequest : retour de recette humaine sur une feature en branche de feature.
type CorrectionRequest struct {
	Title       string
	Description string
	Criteria    []string
}

// defaultCorrectionCriterion est utilisé quand l'humain ne précise aucun critère.
const defaultCorrectionCriterion = "Le problème décrit est corrigé et vérifié dans l'application, sans régression sur la feature."

// RequestCorrection ajoute un ticket de correction à une feature en recette (ou bloquée depuis la
// recette) : la feature repart en développement sur sa branche de feature ; le ticket suit le cycle
// normal (dev, revue, QA, PR vers la branche de feature, merge humain). Quand tous les tickets sont
// de nouveau terminés, la feature revient en recette : la PR finale déjà ouverte est mise à jour
// (elle suit la branche toute seule), sinon une nouvelle approbation de PR finale est demandée.
func (e *Engine) RequestCorrection(ctx context.Context, featureID int64, req CorrectionRequest, login string) (*store.Ticket, error) {
	e.Init()
	title, desc := strings.TrimSpace(req.Title), strings.TrimSpace(req.Description)
	if len(req.Criteria) > domain.MaxCorrectionCriteria {
		return nil, userErr(422, "invalid_correction", "au plus %d critères", domain.MaxCorrectionCriteria)
	}
	criteria := make([]string, 0, len(req.Criteria))
	for _, c := range req.Criteria {
		if c = strings.TrimSpace(c); c != "" {
			criteria = append(criteria, c)
		}
	}
	if msg, ok := domain.ValidCorrection(title, desc, criteria); !ok {
		return nil, userErr(422, "invalid_correction", "%s", msg)
	}
	if len(criteria) == 0 {
		criteria = []string{defaultCorrectionCriterion}
	}
	actor := audit.User(login)
	var ticket *store.Ticket
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		f, err := store.LockFeature(ctx, tx, featureID)
		if err != nil {
			return err
		}
		st := domain.FeatureState(f.State)
		fromBlockedAcceptance := st == domain.FeatureBlocked && str(f.PreviousState) == string(domain.FeatureAcceptance)
		if st != domain.FeatureAcceptance && !fromBlockedAcceptance {
			return userErr(409, "correction_not_allowed", "une correction se demande pendant la recette (feature %s)", f.State)
		}
		if f.IntegrationMode != domain.IntegrationFeatureBranch || str(f.FeatureBranch) == "" {
			return userErr(409, "correction_not_allowed", "la correction en recette requiert le mode branche de feature")
		}
		p, err := store.GetProject(ctx, tx, f.ProjectID)
		if err != nil {
			return err
		}
		tickets, err := store.ListTicketsByFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		seq := 0
		for _, t := range tickets {
			seq = max(seq, t.Seq)
		}
		seq++
		// Une PR finale pas encore publiée (approbation en attente) n'a plus d'objet : elle sera
		// redemandée au retour en recette, sur la branche corrigée.
		expired, err := store.ExpirePendingApprovals(ctx, tx, "feature", []int64{f.ID})
		if err != nil {
			return err
		}
		body := desc + fmt.Sprintf("\n\n_Correction demandée en recette par %s, sur la branche de feature %s._", login, *f.FeatureBranch)
		ticket, err = store.InsertTicket(ctx, tx, store.TicketInput{FeatureID: f.ID, ProjectID: p.ID, Key: ticketKey(p.Slug, f.ID, seq),
			Seq: seq, Title: title, Body: body, Acceptance: criteria, Origin: "correction"})
		if err != nil {
			return err
		}
		nf, err := e.featureEvent(ctx, tx, f, domain.FeatureEvCorrectionRequested, actor, "correction demandée en recette : "+shorten(title, 120))
		if err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor, Action: "feature.correction_ticket_added", SubjectType: "feature", SubjectID: f.ID,
			After: map[string]any{"ticket_id": ticket.ID, "ticket_key": ticket.Key, "title": title, "criteria": len(criteria),
				"feature_pr": f.FeaturePRNumber, "approvals_expired": expired, "state": nf.State}}); err != nil {
			return err
		}
		if _, err := e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobOpenCorrection, SubjectType: "ticket", SubjectID: ticket.ID, ProjectID: &p.ID,
			IdempotencyKey: jobKey(domain.JobOpenCorrection, "ticket", ticket.ID, "v1")}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "ticket.updated", ticket.ID)
	})
	if err != nil {
		return nil, err
	}
	f, _ := store.GetFeature(ctx, e.Store.Pool, featureID)
	if f != nil {
		e.notifyProject(ctx, f.ProjectID, fmt.Sprintf("🔁 Correction demandée en recette sur « %s » : %s (%s).", f.Title, ticket.Title, ticket.Key))
	}
	return ticket, nil
}

// handleOpenCorrection crée l'issue du ticket de correction puis le rend prêt (le feu vert de la
// feature couvre ses corrections : même dépôt, même branche de feature, PR des tickets en draft).
func (e *Engine) handleOpenCorrection(ctx context.Context, j *store.Job, owner string) error {
	t, err := store.GetTicket(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if t.State != string(domain.TicketPlanned) {
		return e.skip(ctx, j, owner, "ticket de correction déjà lancé ou annulé")
	}
	f, err := store.GetFeature(ctx, e.Store.Pool, t.FeatureID)
	if err != nil {
		return err
	}
	p, err := store.GetProject(ctx, e.Store.Pool, t.ProjectID)
	if err != nil {
		return err
	}
	if t.IssueNumber == nil {
		var criteria []string
		_ = jsonUnmarshal(t.Acceptance, &criteria)
		if err := e.fence(ctx, "création d'issue"); err != nil {
			return err
		}
		is, created, err := e.GitHub.EnsureIssue(ctx, p.Repository, t.Key, t.Title,
			issueBody(domain.DesignTicket{Title: t.Title, Body: t.Body, AcceptanceCriteria: criteria}, f.Title+" (correction de recette)"))
		if err != nil {
			return err
		}
		if err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			if err := store.SetTicketIssue(ctx, tx, t.ID, is.Number, is.HTMLURL); err != nil {
				return err
			}
			return audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "ticket.issue_linked", SubjectType: "ticket", SubjectID: t.ID,
				After: map[string]any{"issue_number": is.Number, "issue_url": is.HTMLURL, "created": created}})
		}); err != nil {
			return err
		}
	}
	return e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		lt, err := store.LockTicket(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if lt.State != string(domain.TicketPlanned) {
			return nil
		}
		if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvReady, audit.System, "correction de recette"); err != nil {
			return err
		}
		return e.advanceProject(ctx, tx, lt.ProjectID)
	})
}

// handleRefreshFeaturePR met à jour la PR finale ouverte après des corrections (nouveaux tickets
// intégrés, Closes de leurs issues). Une PR fermée entre-temps est traitée par ReconcileFeaturePR.
func (e *Engine) handleRefreshFeaturePR(ctx context.Context, j *store.Job, owner string) error {
	f, err := store.GetFeature(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if f.State != string(domain.FeatureAcceptance) || f.FeaturePRNumber == nil {
		return e.skip(ctx, j, owner, "feature hors recette ou sans PR finale")
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	tickets, err := store.ListTicketsByFeature(ctx, e.Store.Pool, f.ID)
	if err != nil {
		return err
	}
	if err := e.fence(ctx, "mise à jour de la PR finale"); err != nil {
		return err
	}
	pr, err := e.GitHub.UpdatePR(ctx, p.Repository, *f.FeaturePRNumber, featurePRTitle(f), featurePRBody(f, tickets))
	if err != nil {
		return err
	}
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "feature.pr_refreshed", SubjectType: "feature", SubjectID: f.ID,
			After: map[string]any{"pr_number": pr.Number, "head_sha": pr.Head.SHA}, SHA: pr.Head.SHA})
	})
	if err == nil {
		e.notifyProject(ctx, f.ProjectID, fmt.Sprintf("🧪 Corrections intégrées dans %s : « %s » est de nouveau en recette (PR finale #%d à jour). %s",
			str(f.FeatureBranch), f.Title, pr.Number, pr.HTMLURL))
	}
	return err
}
