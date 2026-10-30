package store

import (
	"context"
	"time"
)

// QARecording est un fichier d'enregistrement du QA navigateur (table qa_recordings).
type QARecording struct {
	ID        int64     `db:"id"`
	RunID     int64     `db:"run_id"`
	TicketID  int64     `db:"ticket_id"`
	Kind      string    `db:"kind"`
	Name      string    `db:"name"`
	Label     string    `db:"label"`
	Criterion *int      `db:"criterion"`
	SizeBytes int64     `db:"size_bytes"`
	SHA256    string    `db:"sha256"`
	CreatedAt time.Time `db:"created_at"`
	ExpiresAt time.Time `db:"expires_at"`
}

const recordingCols = `id, run_id, ticket_id, kind, name, label, criterion, size_bytes, sha256, created_at, expires_at`

// NewRecording décrit un fichier ingéré.
type NewRecording struct {
	Kind, Name, Label string
	Criterion         *int
	Size              int64
	SHA256            string
}

// InsertRecordings enregistre les fichiers d'un run (idempotent sur run_id + name).
func InsertRecordings(ctx context.Context, q Q, runID, ticketID int64, files []NewRecording, expires time.Time) error {
	for _, f := range files {
		if _, err := q.Exec(ctx, `INSERT INTO qa_recordings (run_id, ticket_id, kind, name, label, criterion, size_bytes, sha256, expires_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (run_id, name) DO NOTHING`,
			runID, ticketID, f.Kind, f.Name, f.Label, f.Criterion, f.Size, f.SHA256, expires); err != nil {
			return err
		}
	}
	return nil
}

// ListTicketRecordings renvoie les enregistrements non expirés d'un ticket (runs récents d'abord).
func ListTicketRecordings(ctx context.Context, q Q, ticketID int64) ([]QARecording, error) {
	return many[QARecording](ctx, q, `SELECT `+recordingCols+` FROM qa_recordings
		WHERE ticket_id=$1 AND expires_at > now() ORDER BY run_id DESC, name`, ticketID)
}

// GetRecording lit un enregistrement non expiré.
func GetRecording(ctx context.Context, q Q, id int64) (*QARecording, error) {
	return one[QARecording](ctx, q, `SELECT `+recordingCols+` FROM qa_recordings WHERE id=$1 AND expires_at > now()`, id)
}

// ExpiredRecordingRuns renvoie les runs dont tous les enregistrements ont expiré.
func ExpiredRecordingRuns(ctx context.Context, q Q, now time.Time) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT run_id FROM qa_recordings GROUP BY run_id HAVING max(expires_at) <= $1 ORDER BY run_id`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteRunRecordings supprime les lignes d'enregistrement d'un run.
func DeleteRunRecordings(ctx context.Context, q Q, runID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM qa_recordings WHERE run_id=$1`, runID)
	return err
}
