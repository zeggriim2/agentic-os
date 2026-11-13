package store

import (
	"context"
	"errors"
	"time"
)

// FeatureMessage est un message du fil d'une feature : consigne, question ou réponse d'agent.
type FeatureMessage struct {
	ID         int64     `db:"id"`
	FeatureID  int64     `db:"feature_id"`
	Kind       string    `db:"kind"`
	Author     string    `db:"author"`
	TargetRole *string   `db:"target_role"`
	Body       string    `db:"body"`
	Status     string    `db:"status"`
	ReplyTo    *int64    `db:"reply_to"`
	RunID      *int64    `db:"run_id"`
	Error      *string   `db:"error"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

const messageCols = `id, feature_id, kind, author, target_role, body, status, reply_to, run_id, error, created_at, updated_at`

// NewMessage décrit un message à créer.
type NewMessage struct {
	FeatureID  int64
	Kind       string
	Author     string
	TargetRole string // vide = aucun
	Body       string
	Status     string
	ReplyTo    *int64
	RunID      *int64
}

// InsertMessage crée un message.
func InsertMessage(ctx context.Context, q Q, m NewMessage) (*FeatureMessage, error) {
	return one[FeatureMessage](ctx, q, `INSERT INTO feature_messages (feature_id, kind, author, target_role, body, status, reply_to, run_id)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7,$8) RETURNING `+messageCols,
		m.FeatureID, m.Kind, m.Author, m.TargetRole, m.Body, m.Status, m.ReplyTo, m.RunID)
}

// GetMessage lit un message.
func GetMessage(ctx context.Context, q Q, id int64) (*FeatureMessage, error) {
	return one[FeatureMessage](ctx, q, `SELECT `+messageCols+` FROM feature_messages WHERE id=$1`, id)
}

// ListMessages liste les messages d'une feature, du plus ancien au plus récent.
func ListMessages(ctx context.Context, q Q, featureID int64) ([]FeatureMessage, error) {
	return many[FeatureMessage](ctx, q, `SELECT `+messageCols+` FROM feature_messages WHERE feature_id=$1 ORDER BY id`, featureID)
}

// SetMessageStatus change l'état d'un message (run et erreur facultatifs, conservés si nil/vide).
func SetMessageStatus(ctx context.Context, q Q, id int64, status string, runID *int64, errMsg string) error {
	_, err := q.Exec(ctx, `UPDATE feature_messages SET status=$2, run_id=COALESCE($3, run_id), error=NULLIF($4,''), updated_at=now()
		WHERE id=$1`, id, status, runID, truncate(errMsg, 2000))
	return err
}

// InstructionsForRun renvoie les consignes à joindre à un run : celles en attente pour la feature et
// celles déjà rattachées à ce run (re-soumission), dans l'ordre d'écriture. Une consigne adressée à
// un rôle n'est jointe qu'aux runs de ce rôle.
func InstructionsForRun(ctx context.Context, q Q, featureID, runID int64, role string) ([]FeatureMessage, error) {
	return many[FeatureMessage](ctx, q, `SELECT `+messageCols+` FROM feature_messages
		WHERE feature_id=$1 AND kind='instruction' AND (target_role IS NULL OR target_role=$3)
		  AND (status='pending' OR (status='consumed' AND run_id=$2))
		ORDER BY id`, featureID, runID, role)
}

// ConsumeInstructions rattache des consignes en attente à un run.
func ConsumeInstructions(ctx context.Context, q Q, ids []int64, runID int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.Exec(ctx, `UPDATE feature_messages SET status='consumed', run_id=$2, updated_at=now()
		WHERE id = ANY($1) AND status='pending'`, ids, runID)
	return err
}

// FeatureOfSubject renvoie la feature d'un sujet de job (feature, ticket ; 0 sinon).
func FeatureOfSubject(ctx context.Context, q Q, subjectType string, subjectID int64) (int64, error) {
	switch subjectType {
	case "feature":
		return subjectID, nil
	case "ticket":
		var id int64
		err := q.QueryRow(ctx, `SELECT feature_id FROM tickets WHERE id=$1`, subjectID).Scan(&id)
		return id, err
	}
	return 0, nil
}

// featureRunsSQL : runs des jobs d'une feature (sur la feature ou l'un de ses tickets).
const featureRunsSQL = `FROM runs r JOIN jobs j ON j.id = r.job_id
	LEFT JOIN tickets t ON j.subject_type = 'ticket' AND t.id = j.subject_id
	WHERE ((j.subject_type = 'feature' AND j.subject_id = $1) OR t.feature_id = $1)`

// ActiveFeatureRun renvoie le run d'agent en cours sur la feature (hors réponses aux questions), ou nil.
func ActiveFeatureRun(ctx context.Context, q Q, featureID int64) (*Run, error) {
	r, err := one[Run](ctx, q, `SELECT `+prefixed("r", runCols)+` `+featureRunsSQL+`
		AND r.status IN ('submitted','running') AND r.hermes_run_id IS NOT NULL AND j.kind <> 'answer_question'
		ORDER BY r.id DESC LIMIT 1`, featureID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// LatestRoleRun renvoie le dernier run terminé d'un rôle sur la feature (réponses aux questions
// comprises : elles prolongent la même session), ou nil.
func LatestRoleRun(ctx context.Context, q Q, featureID int64, role string) (*Run, error) {
	r, err := one[Run](ctx, q, `SELECT `+prefixed("r", runCols)+` `+featureRunsSQL+`
		AND r.agent_role = $2 AND r.status IN ('completed','failed','stopped') AND r.hermes_run_id IS NOT NULL
		ORDER BY r.id DESC LIMIT 1`, featureID, role)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// RoleRunning indique si un run du rôle est en cours sur la feature.
func RoleRunning(ctx context.Context, q Q, featureID int64, role string) (bool, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) `+featureRunsSQL+` AND r.agent_role = $2 AND r.status IN ('submitted','running')`,
		featureID, role).Scan(&n)
	return n > 0, err
}

// FeatureRun est un run de la feature ou de l'un de ses tickets, avec son contexte pour le fil.
type FeatureRun struct {
	Run
	JobKind   string  `db:"job_kind"`
	TicketKey *string `db:"ticket_key"`
}

// ListFeatureRuns liste tous les runs d'une feature (feature et tickets), du plus ancien au plus récent.
func ListFeatureRuns(ctx context.Context, q Q, featureID int64) ([]FeatureRun, error) {
	return many[FeatureRun](ctx, q, `SELECT `+prefixed("r", runCols)+`, j.kind AS job_kind, t.key AS ticket_key `+featureRunsSQL+`
		ORDER BY r.id`, featureID)
}

// ListFeatureApprovals liste les approbations de la feature et de ses tickets.
func ListFeatureApprovals(ctx context.Context, q Q, featureID int64) ([]Approval, error) {
	return many[Approval](ctx, q, `SELECT `+prefixed("a", approvalCols)+` FROM approvals a
		LEFT JOIN tickets t ON a.subject_type = 'ticket' AND t.id = a.subject_id
		WHERE (a.subject_type = 'feature' AND a.subject_id = $1) OR t.feature_id = $1 ORDER BY a.id`, featureID)
}
