package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Livraisons GitHub : le corps vérifié par HMAC est conservé ; chaque livraison passe par
// received → processing → processed | failed. Une livraison en échec est retraitée avec un
// backoff exponentiel (RetryDeliveries) ou immédiatement si GitHub la redélivre.

// MaxDeliveryAttempts borne les reprises automatiques d'une livraison.
const MaxDeliveryAttempts = 8

// Résultats de ReceiveDelivery.
const (
	DeliveryProcessed = "processed"
	DeliveryDuplicate = "duplicate"
	DeliveryRetry     = "retry_scheduled"
	DeliveryFailed    = "failed"
)

type webhookPayload struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	PullRequest *struct {
		Number int `json:"number"`
		Head   struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
	} `json:"pull_request"`
	CheckRun *struct {
		HeadSHA    string `json:"head_sha"`
		CheckSuite struct {
			HeadBranch string `json:"head_branch"`
		} `json:"check_suite"`
	} `json:"check_run"`
	CheckSuite *struct {
		HeadSHA    string `json:"head_sha"`
		HeadBranch string `json:"head_branch"`
	} `json:"check_suite"`
	SHA string `json:"sha"`
}

// ReceiveDelivery enregistre une livraison authentifiée puis la traite. Une livraison déjà
// traitée (ou en cours) est un doublon ; une redélivrance d'une livraison en échec est
// retraitée immédiatement.
func (e *Engine) ReceiveDelivery(ctx context.Context, deliveryID, event string, body []byte) (string, int, error) {
	e.Init()
	if _, err := store.InsertDelivery(ctx, e.Store.Pool, deliveryID, event, body); err != nil {
		return "", 0, err
	}
	d, err := store.ClaimDelivery(ctx, e.Store.Pool, deliveryID)
	if err != nil {
		return "", 0, err
	}
	if d == nil {
		return DeliveryDuplicate, 0, nil
	}
	return e.processDelivery(ctx, d)
}

// RetryDeliveries retraite les livraisons échues (toutes les 30 s), chacune au plus une
// fois par passe.
func (e *Engine) RetryDeliveries(ctx context.Context) error {
	e.Init()
	var seen []int64
	for i := 0; i < 50; i++ {
		d, err := store.ClaimDueDelivery(ctx, e.Store.Pool, seen)
		if err != nil || d == nil {
			return err
		}
		seen = append(seen, d.ID)
		if _, _, err := e.processDelivery(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) processDelivery(ctx context.Context, d *store.Delivery) (string, int, error) {
	n, perr := e.applyDelivery(ctx, d.Event, d.Payload)
	bg := context.Background() // l'issue doit être enregistrée même si la requête est annulée
	if perr == nil {
		if err := store.FinishDelivery(bg, e.Store.Pool, d.ID, true, "", nil); err != nil {
			return "", 0, err
		}
		_ = audit.Write(bg, e.Store.Pool, audit.Entry{Actor: audit.System, Action: "webhook.github", SubjectType: "system",
			After: map[string]any{"event": d.Event, "delivery": d.DeliveryID, "tickets": n, "attempt": d.Attempts}})
		return DeliveryProcessed, n, nil
	}
	var next *time.Time
	result := DeliveryFailed
	if retry, ok := perr.(*deliveryError); !ok || retry.retryable {
		if d.Attempts < MaxDeliveryAttempts {
			t := time.Now().Add(e.deliveryBackoff(d.Attempts))
			next, result = &t, DeliveryRetry
		}
	}
	e.Log.Warn("traitement de webhook en échec", "delivery", d.DeliveryID, "attempt", d.Attempts, "retry", next != nil, "error", perr.Error())
	if err := store.FinishDelivery(bg, e.Store.Pool, d.ID, false, perr.Error(), next); err != nil {
		return "", 0, err
	}
	if next == nil {
		_ = audit.Write(bg, e.Store.Pool, audit.Entry{Actor: audit.System, Action: "webhook.github_failed", SubjectType: "system",
			After: map[string]any{"event": d.Event, "delivery": d.DeliveryID, "attempts": d.Attempts, "error": truncate(perr.Error(), 300)}})
	}
	return result, 0, nil
}

func (e *Engine) deliveryBackoff(attempt int) time.Duration {
	if attempt > 10 {
		attempt = 10
	}
	d := e.Backoff * time.Duration(1<<attempt)
	if d > time.Hour {
		d = time.Hour
	}
	return d
}

type deliveryError struct {
	msg       string
	retryable bool
}

func (e *deliveryError) Error() string { return e.msg }

// applyDelivery extrait la cible de l'événement et réconcilie les tickets concernés.
func (e *Engine) applyDelivery(ctx context.Context, event string, body []byte) (int, error) {
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return 0, &deliveryError{msg: "corps JSON invalide"}
	}
	var pr int
	var sha, branch string
	switch event {
	case "pull_request":
		if p.PullRequest != nil {
			pr, sha, branch = p.PullRequest.Number, p.PullRequest.Head.SHA, p.PullRequest.Head.Ref
		}
	case "check_run":
		if p.CheckRun != nil {
			sha, branch = p.CheckRun.HeadSHA, p.CheckRun.CheckSuite.HeadBranch
		}
	case "check_suite":
		if p.CheckSuite != nil {
			sha, branch = p.CheckSuite.HeadSHA, p.CheckSuite.HeadBranch
		}
	case "status":
		sha = p.SHA
	default:
		return 0, nil // événement ignoré
	}
	if p.Repository.FullName == "" {
		return 0, nil
	}
	n, err := e.HandleGitHubEvent(ctx, p.Repository.FullName, pr, sha, branch)
	if err != nil {
		return 0, &deliveryError{msg: fmt.Sprintf("réconciliation : %v", err), retryable: true}
	}
	return n, nil
}
