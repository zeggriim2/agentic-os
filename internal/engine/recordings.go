package engine

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/recordings"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// qaBrowserSetup prépare le dossier de dépôt du QA navigateur d'un passage QA. Renvoie "" si le
// projet ne l'utilise pas ou si le dossier n'a pas pu être créé (le QA se fait alors sans navigateur).
func (e *Engine) qaBrowserSetup(p *store.Project, t *store.Ticket, j *store.Job) string {
	if !p.QABrowser || e.Recordings == "" {
		return ""
	}
	dir, err := recordings.InboxDir(e.Recordings, p.Slug, t.Key, j.ID, t.Attempt)
	if err == nil {
		err = recordings.Prepare(dir)
	}
	if err != nil {
		e.Log.Warn("dossier d'enregistrement QA indisponible", "ticket", t.Key, "error", err.Error())
		return ""
	}
	return dir
}

// frontChanged indique si le ticket modifie l'interface (front, gabarits).
func (e *Engine) frontChanged(ctx context.Context, wt, base string) bool {
	files, err := e.Git.ChangedFiles(ctx, wt, "refs/remotes/origin/"+base)
	if err != nil {
		return false
	}
	for _, f := range files {
		if strings.HasPrefix(f, "frontend/") || strings.HasPrefix(f, "templates/") || strings.HasPrefix(f, "assets/") {
			return true
		}
	}
	return false
}

// ingestQARecordings copie les enregistrements déposés par le QA dans le stockage, les référence et
// supprime le dossier de dépôt. Un échec n'empêche pas l'évaluation du QA (il est journalisé et audité).
func (e *Engine) ingestQARecordings(ctx context.Context, t *store.Ticket, runID int64, inbox string) {
	defer os.RemoveAll(inbox)
	if entries, err := os.ReadDir(inbox); err != nil || len(entries) == 0 {
		return
	}
	files, report, skipped, err := recordings.Ingest(inbox, recordings.StoreDir(e.Recordings, runID))
	if err != nil {
		e.Log.Warn("ingestion des enregistrements QA en échec", "ticket", t.Key, "run_id", runID, "error", err.Error())
		return
	}
	rows := make([]store.NewRecording, 0, len(files))
	var size int64
	for _, f := range files {
		rows = append(rows, store.NewRecording{Kind: f.Kind, Name: f.Name, Label: f.Label, Criterion: f.Criterion, Size: f.Size, SHA256: f.SHA256})
		size += f.Size
	}
	after := map[string]any{"run_id": runID, "files": len(files), "bytes": size, "skipped": skipped}
	if report != nil {
		after["scenario_ok"] = report.OK
	}
	err = e.Store.InTx(ctx, func(tx pgx.Tx) error {
		if err := store.InsertRecordings(ctx, tx, runID, t.ID, rows, time.Now().Add(recordings.Retention)); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "ticket.qa_recorded", SubjectType: "ticket",
			SubjectID: t.ID, RunID: &runID, After: after})
	})
	if err != nil {
		e.Log.Warn("référencement des enregistrements QA en échec", "ticket", t.Key, "run_id", runID, "error", err.Error())
	}
}

// PurgeRecordings supprime les enregistrements expirés (15 jours) et les dossiers de dépôt abandonnés.
func (e *Engine) PurgeRecordings(ctx context.Context) error {
	if e.Recordings == "" {
		return nil
	}
	runs, err := store.ExpiredRecordingRuns(ctx, e.Store.Pool, time.Now())
	if err != nil {
		return err
	}
	for _, id := range runs {
		if err := os.RemoveAll(recordings.StoreDir(e.Recordings, id)); err != nil {
			e.Log.Warn("purge d'enregistrement QA", "run_id", id, "error", err.Error())
			continue
		}
		if err := store.DeleteRunRecordings(ctx, e.Store.Pool, id); err != nil {
			return err
		}
	}
	_, err = recordings.PurgeInbox(e.Recordings, 24*time.Hour, time.Now())
	return err
}
