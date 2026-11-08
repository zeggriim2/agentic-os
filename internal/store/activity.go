package store

import (
	"context"
	"encoding/json"
	"time"
)

// ActivitySubject situe une exécution d'agent : ticket (clé, titre, feature) ou feature.
type ActivitySubject struct {
	Type         string `db:"subject_type" json:"type"`
	ID           int64  `db:"subject_id" json:"id"`
	Key          string `db:"ticket_key" json:"key,omitempty"`
	Title        string `db:"title" json:"title"`
	FeatureID    *int64 `db:"feature_id" json:"feature_id"`
	FeatureTitle string `db:"feature_title" json:"feature_title"`
	Project      string `db:"project" json:"project"`
}

// ActiveRun est un run en cours, avec son dernier outil appelé.
type ActiveRun struct {
	RunID       int64           `db:"run_id"`
	Role        string          `db:"agent_role"`
	JobKind     string          `db:"job_kind"`
	Status      string          `db:"status"`
	TotalTokens int64           `db:"total_tokens"`
	StartedAt   time.Time       `db:"started_at"`
	LastEvent   *string         `db:"last_event"`
	LastData    json.RawMessage `db:"last_data"`
	LastAt      *time.Time      `db:"last_at"`
	ActivitySubject
}

// ActivityEvent est un événement de run utile au fil d'activité (outil lancé, message
// intermédiaire, fin de run).
type ActivityEvent struct {
	ID        int64           `db:"id"`
	RunID     int64           `db:"run_id"`
	Role      string          `db:"agent_role"`
	Event     string          `db:"event"`
	Data      json.RawMessage `db:"data"`
	CreatedAt time.Time       `db:"created_at"`
	ActivitySubject
}

// ActivityEvents : événements retenus pour le fil d'activité.
var ActivityEvents = []string{"tool.started", "message.interim", "run.completed", "run.failed", "run.cancelled"}

const activitySubjectSQL = `j.subject_type, j.subject_id, COALESCE(t.key, '') AS ticket_key,
	COALESCE(t.title, f.title, '') AS title, f.id AS feature_id, COALESCE(f.title, '') AS feature_title, COALESCE(p.slug, '') AS project
	FROM runs r
	JOIN jobs j ON j.id = r.job_id
	LEFT JOIN tickets t ON j.subject_type = 'ticket' AND t.id = j.subject_id
	LEFT JOIN features f ON f.id = CASE WHEN j.subject_type = 'feature' THEN j.subject_id ELSE t.feature_id END
	LEFT JOIN projects p ON p.id = COALESCE(t.project_id, f.project_id)`

// ListActiveRuns liste les runs en cours (soumis ou en cours), du plus ancien au plus récent.
func ListActiveRuns(ctx context.Context, q Q) ([]ActiveRun, error) {
	return many[ActiveRun](ctx, q, `SELECT r.id AS run_id, r.agent_role, j.kind AS job_kind, r.status, r.total_tokens, r.started_at,
		le.event AS last_event, le.data AS last_data, le.created_at AS last_at, `+activitySubjectSQL+`
		LEFT JOIN LATERAL (SELECT e.event, e.data, e.created_at FROM run_events e
			WHERE e.run_id = r.id AND e.event IN ('tool.started', 'message.interim') ORDER BY e.seq DESC LIMIT 1) le ON true
		WHERE r.status IN ('submitted', 'running')
		ORDER BY r.started_at, r.id`)
}

// ListActivity renvoie les derniers événements utiles de tous les runs (plus récents d'abord).
func ListActivity(ctx context.Context, q Q, limit int) ([]ActivityEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	return many[ActivityEvent](ctx, q, `SELECT e.id, e.run_id, r.agent_role, e.event, e.data, e.created_at, `+activitySubjectSQL+`
		JOIN run_events e ON e.run_id = r.id
		WHERE e.event = ANY($1)
		ORDER BY e.id DESC LIMIT $2`, ActivityEvents, limit)
}
