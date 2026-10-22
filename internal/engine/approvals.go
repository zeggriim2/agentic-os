package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// UserError est une erreur à présenter à l'utilisateur (code snake_case + message FR).
type UserError struct {
	Status  int
	Code    string
	Message string
}

func (e *UserError) Error() string { return e.Message }

func userErr(status int, code, format string, a ...any) error {
	return &UserError{Status: status, Code: code, Message: fmt.Sprintf(format, a...)}
}

// DecideApproval enregistre la décision (transaction 1) puis exécute son effet (transaction 2).
// Une décision répétée renvoie store.ErrAlreadyDecided.
func (e *Engine) DecideApproval(ctx context.Context, id int64, decision, comment, login string) (*store.Approval, error) {
	e.Init()
	if decision != domain.ApprovalApproved && decision != domain.ApprovalRejected {
		return nil, userErr(422, "invalid_decision", "décision invalide (approved ou rejected)")
	}
	comment = strings.TrimSpace(comment)
	if decision == domain.ApprovalRejected && comment == "" {
		return nil, userErr(422, "comment_required", "un commentaire est obligatoire pour rejeter")
	}
	if len(comment) > 10000 {
		return nil, userErr(422, "comment_too_long", "commentaire trop long")
	}
	actor := audit.User(login)
	var ap *store.Approval
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		ap, err = store.DecideApproval(ctx, tx, id, decision, actor, comment)
		if err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor, Action: "approval." + decision, SubjectType: ap.SubjectType, SubjectID: ap.SubjectID,
			After: map[string]any{"approval_id": ap.ID, "kind": ap.Kind, "comment": comment}}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "approval.updated", ap.ID)
	})
	if err != nil {
		return nil, err
	}
	var result map[string]any
	var ok bool
	var notices []string
	execErr := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		var err error
		result, ok, notices, err = e.executeApproval(ctx, tx, ap, actor)
		if err != nil {
			return err
		}
		if err := store.MarkApprovalExecuted(ctx, tx, ap.ID, ok, result); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "approval.executed", SubjectType: ap.SubjectType, SubjectID: ap.SubjectID,
			After: map[string]any{"approval_id": ap.ID, "ok": ok, "result": result}}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "approval.updated", ap.ID)
	})
	if execErr != nil {
		e.Log.Warn("exécution d'approbation en échec", "approval_id", ap.ID, "error", execErr.Error())
		_ = e.Store.InTx(ctx, func(tx pgx.Tx) error {
			res := map[string]any{"error": truncate(execErr.Error(), 500)}
			if err := store.MarkApprovalExecuted(ctx, tx, ap.ID, false, res); err != nil {
				return err
			}
			if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "approval.execution_failed", SubjectType: ap.SubjectType,
				SubjectID: ap.SubjectID, After: map[string]any{"approval_id": ap.ID, "error": res["error"]}}); err != nil {
				return err
			}
			return store.Notify(ctx, tx, "approval.updated", ap.ID)
		})
	}
	for _, n := range notices {
		e.Notifier.Notify(ctx, n)
	}
	return store.GetApproval(ctx, e.Store.Pool, ap.ID)
}

func (e *Engine) executeApproval(ctx context.Context, tx pgx.Tx, ap *store.Approval, actor string) (map[string]any, bool, []string, error) {
	approved := str(ap.Decision) == domain.ApprovalApproved
	comment := str(ap.DecisionComment)
	disc := fmt.Sprintf("approval-%d", ap.ID)
	switch ap.SubjectType {
	case "feature":
		f, err := store.LockFeature(ctx, tx, ap.SubjectID)
		if err != nil {
			return nil, false, nil, err
		}
		p, err := store.GetProject(ctx, tx, f.ProjectID)
		if err != nil {
			return nil, false, nil, err
		}
		next := func(ev domain.FeatureEvent, kind, feedback string) (map[string]any, bool, []string, error) {
			nf, err := e.featureEvent(ctx, tx, f, ev, actor, comment)
			if err != nil {
				return nil, false, nil, err
			}
			j, err := e.enqueue(ctx, tx, store.NewJob{Kind: kind, SubjectType: "feature", SubjectID: f.ID, ProjectID: &f.ProjectID,
				IdempotencyKey: jobKey(kind, "feature", f.ID, disc), Payload: jobPayload{Feedback: feedback}})
			if err != nil {
				return nil, false, nil, err
			}
			return map[string]any{"state": nf.State, "job_id": j.ID}, true, nil, nil
		}
		switch ap.Kind {
		case domain.ApprovalSpecReview:
			if approved {
				return next(domain.FeatureEvSpecApproved, domain.JobTechDesign, "")
			}
			return next(domain.FeatureEvSpecRejected, domain.JobPOSpec, comment)
		case domain.ApprovalDesignReview:
			if approved {
				return next(domain.FeatureEvDesignApproved, domain.JobOpenIssues, "")
			}
			return next(domain.FeatureEvDesignRejected, domain.JobTechDesign, comment)
		case domain.ApprovalDevGate:
			tickets, err := store.ListTicketsByFeature(ctx, tx, f.ID)
			if err != nil {
				return nil, false, nil, err
			}
			if !approved {
				nf, err := e.featureEvent(ctx, tx, f, domain.FeatureEvDevGateRejected, actor, comment)
				if err != nil {
					return nil, false, nil, err
				}
				for i := range tickets {
					if _, err := e.ticketEvent(ctx, tx, &tickets[i], domain.TicketEvCancel, actor, "feu vert refusé"); err != nil {
						return nil, false, nil, err
					}
				}
				return map[string]any{"state": nf.State}, true, nil, nil
			}
			nf, err := e.featureEvent(ctx, tx, f, domain.FeatureEvDevGateApproved, actor, comment)
			if err != nil {
				return nil, false, nil, err
			}
			var jobs []int64
			for i := range tickets {
				t := &tickets[i]
				if t.State != string(domain.TicketPlanned) {
					continue
				}
				if _, err := e.ticketEvent(ctx, tx, t, domain.TicketEvReady, actor, ""); err != nil {
					return nil, false, nil, err
				}
				j, err := e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobDevelop, SubjectType: "ticket", SubjectID: t.ID, ProjectID: &p.ID,
					IdempotencyKey: jobKey(domain.JobDevelop, "ticket", t.ID, disc)})
				if err != nil {
					return nil, false, nil, err
				}
				jobs = append(jobs, j.ID)
			}
			return map[string]any{"state": nf.State, "jobs": jobs}, true, nil, nil
		}
	case "ticket":
		t, err := store.LockTicket(ctx, tx, ap.SubjectID)
		if err != nil {
			return nil, false, nil, err
		}
		switch ap.Kind {
		case domain.ApprovalRework:
			if !approved {
				nt, err := e.ticketEvent(ctx, tx, t, domain.TicketEvReworkRejected, actor, comment)
				if err != nil {
					return nil, false, nil, err
				}
				if _, err := e.maybeCompleteFeature(ctx, tx, t.FeatureID); err != nil {
					return nil, false, nil, err
				}
				return map[string]any{"state": nt.State}, true, nil, nil
			}
			if _, err := domain.ApplyTicketRework(domain.TicketState(t.State), t.Attempt, e.MaxRework); err != nil {
				if !errors.Is(err, domain.ErrTransition) || t.State != string(domain.TicketChangesRequested) {
					return nil, false, nil, err
				}
				nt, err := e.ticketEvent(ctx, tx, t, domain.TicketEvReworkExhausted, actor, "nombre maximal de reworks atteint")
				if err != nil {
					return nil, false, nil, err
				}
				return map[string]any{"state": nt.State, "error": "nombre maximal de reworks atteint"}, false,
					[]string{fmt.Sprintf("⛔ Ticket %s bloqué : nombre maximal de reworks atteint.", t.Key)}, nil
			}
			nt, err := e.setTicket(ctx, tx, t, domain.TicketDev, domain.TicketEvReworkApproved, actor, comment, "")
			if err != nil {
				return nil, false, nil, err
			}
			if err := store.SetTicketAttempt(ctx, tx, t.ID, t.Attempt+1); err != nil {
				return nil, false, nil, err
			}
			feedback := detailString(ap, "findings")
			if comment != "" {
				feedback += "\nCommentaire de l'humain : " + comment
			}
			j, err := e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobDevelop, SubjectType: "ticket", SubjectID: t.ID, ProjectID: &t.ProjectID,
				IdempotencyKey: jobKey(domain.JobDevelop, "ticket", t.ID, disc), Payload: jobPayload{Feedback: feedback}})
			if err != nil {
				return nil, false, nil, err
			}
			return map[string]any{"state": nt.State, "attempt": t.Attempt + 1, "job_id": j.ID}, true, nil, nil
		case domain.ApprovalPublishPR:
			if !approved {
				nt, err := e.ticketEvent(ctx, tx, t, domain.TicketEvPRRejected, actor, comment)
				if err != nil {
					return nil, false, nil, err
				}
				if _, err := e.maybeCompleteFeature(ctx, tx, t.FeatureID); err != nil {
					return nil, false, nil, err
				}
				return map[string]any{"state": nt.State}, true, nil, nil
			}
			if want := detailString(ap, "sha"); want == "" || want != str(t.HeadSHA) {
				return nil, false, nil, errors.New("le SHA approuvé ne correspond plus au head_sha du ticket")
			}
			nt, err := e.ticketEvent(ctx, tx, t, domain.TicketEvPRApproved, actor, comment)
			if err != nil {
				return nil, false, nil, err
			}
			j, err := e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobPublishPR, SubjectType: "ticket", SubjectID: t.ID, ProjectID: &t.ProjectID,
				IdempotencyKey: jobKey(domain.JobPublishPR, "ticket", t.ID, disc)})
			if err != nil {
				return nil, false, nil, err
			}
			return map[string]any{"state": nt.State, "job_id": j.ID}, true, nil, nil
		}
	}
	return nil, false, nil, fmt.Errorf("approbation %s/%s non prise en charge", ap.SubjectType, ap.Kind)
}

func detailString(ap *store.Approval, key string) string {
	m := map[string]any{}
	_ = jsonUnmarshal(ap.Details, &m)
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}
