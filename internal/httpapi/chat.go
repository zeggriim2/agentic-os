package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Discussion avec les agents (1.12) : fil d'une feature et envoi de consignes / questions.

type messageView struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Author     string  `json:"author"`
	TargetRole *string `json:"target_role"`
	Body       string  `json:"body"`
	Status     string  `json:"status"`
	ReplyTo    *int64  `json:"reply_to"`
	RunID      *int64  `json:"run_id"`
	Note       *string `json:"note"`
	CreatedAt  string  `json:"created_at"`
}

func viewMessage(m *store.FeatureMessage) messageView {
	return messageView{m.ID, m.Kind, m.Author, m.TargetRole, m.Body, m.Status, m.ReplyTo, m.RunID, m.Error, ts(m.CreatedAt)}
}

// threadRunView : un passage d'agent dans le fil (résumé de son livrable, coût).
type threadRunView struct {
	ID          int64   `json:"id"`
	Role        string  `json:"agent_role"`
	JobKind     string  `json:"job_kind"`
	TicketKey   *string `json:"ticket_key"`
	Status      string  `json:"status"`
	Summary     string  `json:"summary"`
	Error       *string `json:"error"`
	FreshTokens int64   `json:"fresh_tokens"`
	TotalTokens int64   `json:"total_tokens"`
	Model       *string `json:"model"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
}

// artifactSummary extrait le champ summary d'un livrable (tous les rôles en ont un).
func artifactSummary(raw json.RawMessage) string {
	var a struct {
		Summary string `json:"summary"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &a) != nil {
		return ""
	}
	return clip(a.Summary, 600)
}

func viewThreadRun(r *store.FeatureRun) threadRunView {
	fresh := max(r.InputTokens-r.CacheReadTokens, 0) + r.OutputTokens
	var errText *string
	if r.Error != nil {
		e := clip(*r.Error, 300)
		errText = &e
	}
	return threadRunView{r.ID, r.AgentRole, r.JobKind, r.TicketKey, r.Status, artifactSummary(r.Artifact), errText, fresh,
		r.TotalTokens, r.Model, ts(r.StartedAt), tsp(r.FinishedAt)}
}

// featureThread : messages, passages d'agents et décisions de la feature (et de ses tickets), plus
// l'agent en cours pour le compositeur.
func (s *Server) featureThread(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	f, err := store.GetFeature(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	msgs, err := store.ListMessages(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	runs, err := store.ListFeatureRuns(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	approvals, err := store.ListFeatureApprovals(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var working *string
	sessions := map[string]bool{}
	for i := range runs {
		run := &runs[i]
		if run.JobKind != domain.JobAnswerQuestion && (run.Status == domain.RunSubmitted || run.Status == domain.RunRunning) {
			role := run.AgentRole
			working = &role
		}
		if run.HermesRunID != nil {
			sessions[run.AgentRole] = true
		}
	}
	closed := f.State == string(domain.FeatureDone) || f.State == string(domain.FeatureCancelled)
	writeJSON(w, 200, map[string]any{
		"feature_id": id,
		"closed":     closed,
		"working":    working, // rôle de l'agent en cours (null si aucun)
		"sessions":   sessions,
		"messages":   mapSlice(msgs, viewMessage),
		"runs":       mapSlice(runs, viewThreadRun),
		"approvals":  mapSlice(approvals, viewApproval),
	})
}

type messageReq struct {
	Kind string `json:"kind"`
	Role string `json:"role"`
	Body string `json:"body"`
}

// postMessage : consigne (kind=instruction, role facultatif) ou question (kind=question, role requis).
func (s *Server) postMessage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req messageReq
	if !decode(w, r, &req) {
		return
	}
	var (
		m   *store.FeatureMessage
		err error
	)
	switch req.Kind {
	case domain.MessageInstruction:
		m, err = s.Engine.PostInstruction(r.Context(), id, req.Role, req.Body, actorLogin(r))
	case domain.MessageQuestion:
		m, err = s.Engine.AskQuestion(r.Context(), id, req.Role, req.Body, actorLogin(r))
	default:
		writeError(w, 422, "invalid_message", "kind : instruction ou question")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 201, viewMessage(m))
}
