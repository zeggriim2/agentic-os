// Package audit écrit le journal d'audit en ajout seul (INSERT uniquement ; un trigger
// PostgreSQL refuse tout UPDATE/DELETE/TRUNCATE).
package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execer est implémenté par une transaction pgx ou un pool.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Entry est une entrée d'audit.
type Entry struct {
	Actor       string // user:<login> | system | agent:<role>
	Action      string
	SubjectType string
	SubjectID   int64
	Before      any
	After       any
	RunID       *int64
	SHA         string
}

// System est l'acteur système.
const System = "system"

// User renvoie l'acteur d'un utilisateur.
func User(login string) string { return "user:" + login }

// Agent renvoie l'acteur d'un agent.
func Agent(role string) string { return "agent:" + role }

func enc(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// Write ajoute une entrée (dans la transaction fournie).
func Write(ctx context.Context, q Execer, e Entry) error {
	var subj *int64
	if e.SubjectID != 0 {
		subj = &e.SubjectID
	}
	var sha *string
	if e.SHA != "" {
		sha = &e.SHA
	}
	_, err := q.Exec(ctx, `INSERT INTO audit_log (actor, action, subject_type, subject_id, before, after, run_id, sha)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, e.Actor, e.Action, e.SubjectType, subj, enc(e.Before), enc(e.After), e.RunID, sha)
	return err
}
