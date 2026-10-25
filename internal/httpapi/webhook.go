package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/zeggriim2/agentic-os/internal/engine"
	gh "github.com/zeggriim2/agentic-os/internal/github"
)

// githubWebhook vérifie la signature HMAC sur le corps brut AVANT tout parsing, puis
// confie la livraison au moteur (persistance du corps, déduplication, reprise sur échec).
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
	if !json.Valid(body) {
		writeError(w, 400, "invalid_json", "corps JSON invalide")
		return
	}
	status, n, err := s.Engine.ReceiveDelivery(r.Context(), delivery, event, body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	switch status {
	case engine.DeliveryProcessed:
		writeJSON(w, 200, map[string]any{"status": status, "tickets": n})
	case engine.DeliveryRetry:
		writeJSON(w, 202, map[string]any{"status": status})
	default:
		writeJSON(w, 200, map[string]any{"status": status})
	}
}
