package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/zeggriim2/agentic-os/internal/audit"
	gh "github.com/zeggriim2/agentic-os/internal/github"
	"github.com/zeggriim2/agentic-os/internal/store"
)

type webhookPayload struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Number      int `json:"number"`
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

// githubWebhook vérifie la signature HMAC sur le corps brut AVANT tout parsing, déduplique
// X-GitHub-Delivery puis réconcilie les tickets concernés.
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		writeError(w, 400, "invalid_body", "corps illisible")
		return
	}
	secret, err := s.WebhookSecret()
	if err != nil || len(secret) == 0 {
		writeError(w, 503, "webhook_not_configured", "secret de webhook non configuré")
		return
	}
	if !gh.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		s.Log.Warn("webhook GitHub : signature invalide", "ip", clientIP(r))
		writeError(w, 401, "invalid_signature", "signature invalide ou absente")
		return
	}
	delivery := r.Header.Get("X-GitHub-Delivery")
	event := r.Header.Get("X-GitHub-Event")
	if delivery == "" || len(delivery) > 100 || event == "" || len(event) > 100 {
		writeError(w, 400, "invalid_delivery", "en-têtes de livraison manquants")
		return
	}
	ctx := r.Context()
	fresh, err := store.RecordDelivery(ctx, s.Store.Pool, delivery, event)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !fresh {
		writeJSON(w, 200, map[string]any{"status": "duplicate"})
		return
	}
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, 400, "invalid_json", "corps JSON invalide")
		return
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
		_ = store.MarkDeliveryProcessed(ctx, s.Store.Pool, delivery)
		writeJSON(w, 200, map[string]any{"status": "ignored"})
		return
	}
	n := 0
	if p.Repository.FullName != "" && s.Engine != nil {
		n, err = s.Engine.HandleGitHubEvent(ctx, p.Repository.FullName, pr, sha, branch)
		if err != nil {
			s.Log.Warn("webhook : réconciliation en échec (le poller reprendra)", "delivery", delivery, "error", err.Error())
			writeJSON(w, 200, map[string]any{"status": "accepted", "processed": false})
			return
		}
	}
	_ = store.MarkDeliveryProcessed(ctx, s.Store.Pool, delivery)
	_ = audit.Write(ctx, s.Store.Pool, audit.Entry{Actor: audit.System, Action: "webhook.github", SubjectType: "system",
		After: map[string]any{"event": event, "delivery": delivery, "tickets": n}})
	writeJSON(w, 200, map[string]any{"status": "processed", "tickets": n})
}
