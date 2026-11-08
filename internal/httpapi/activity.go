package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zeggriim2/agentic-os/internal/store"
)

// Fil d'activité des agents (1.11) : runs en cours avec leur dernier pas, et derniers événements
// utiles de tous les runs. Les aperçus sont tronqués : le détail complet reste sur la page du run.
const activityTextMax = 160

type activityItem struct {
	ID      int64                 `json:"id"`
	RunID   int64                 `json:"run_id"`
	Role    string                `json:"role"`
	Kind    string                `json:"kind"` // tool | message | done | failed | stopped
	Tool    string                `json:"tool,omitempty"`
	Text    string                `json:"text"`
	At      string                `json:"at"`
	Subject store.ActivitySubject `json:"subject"`
}

type activeRunView struct {
	RunID       int64                 `json:"run_id"`
	Role        string                `json:"role"`
	JobKind     string                `json:"job_kind"`
	Status      string                `json:"status"`
	TotalTokens int64                 `json:"total_tokens"`
	StartedAt   string                `json:"started_at"`
	Last        *activityItem         `json:"last"`
	Subject     store.ActivitySubject `json:"subject"`
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// describeEvent traduit un événement Hermes en ligne lisible du fil d'activité.
func describeEvent(event string, data json.RawMessage) (kind, tool, text string) {
	var d struct {
		Tool    string `json:"tool"`
		Preview string `json:"preview"`
		Text    string `json:"text"`
		Error   string `json:"error"`
	}
	_ = json.Unmarshal(data, &d)
	switch event {
	case "tool.started":
		return "tool", d.Tool, clip(d.Preview, activityTextMax)
	case "message.interim":
		return "message", "", clip(d.Text, activityTextMax*2)
	case "run.completed":
		return "done", "", "a terminé son travail"
	case "run.failed":
		if d.Error != "" {
			return "failed", "", "a échoué : " + clip(d.Error, activityTextMax)
		}
		return "failed", "", "a échoué"
	default:
		return "stopped", "", "a été arrêté"
	}
}

func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := store.ListActiveRuns(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	evs, err := store.ListActivity(ctx, s.Store.Pool, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	running := make([]activeRunView, 0, len(runs))
	for _, ar := range runs {
		v := activeRunView{RunID: ar.RunID, Role: ar.Role, JobKind: ar.JobKind, Status: ar.Status, TotalTokens: ar.TotalTokens,
			StartedAt: ts(ar.StartedAt), Subject: ar.ActivitySubject}
		if ar.LastEvent != nil && ar.LastAt != nil {
			kind, tool, text := describeEvent(*ar.LastEvent, ar.LastData)
			v.Last = &activityItem{RunID: ar.RunID, Role: ar.Role, Kind: kind, Tool: tool, Text: text, At: ts(*ar.LastAt), Subject: ar.ActivitySubject}
		}
		running = append(running, v)
	}
	items := make([]activityItem, 0, len(evs))
	for _, e := range evs {
		kind, tool, text := describeEvent(e.Event, e.Data)
		items = append(items, activityItem{ID: e.ID, RunID: e.RunID, Role: e.Role, Kind: kind, Tool: tool, Text: text, At: ts(e.CreatedAt), Subject: e.ActivitySubject})
	}
	writeJSON(w, 200, map[string]any{"running": running, "events": items})
}
