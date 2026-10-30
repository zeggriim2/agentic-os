package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
)

var recDirRe = regexp.MustCompile(`Dossier d'enregistrement \(seul endroit où écrire\) : (\S+)`)

func TestQABrowserRecordingsIngestedAndPurged(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	ev.qaBrowser = true
	ev.e.Recordings = t.TempDir()
	var inbox, instructions string
	ev.qaHook = func(req hermes.RunRequest) {
		instructions = req.Instructions
		m := recDirRe.FindStringSubmatch(req.Instructions)
		if m == nil {
			t.Fatalf("consignes navigateur absentes : %s", req.Instructions)
		}
		inbox = m[1]
		files := map[string][]byte{
			"video.webm":           {0x1A, 0x45, 0xDF, 0xA3, 0},
			"trace.zip":            {'P', 'K', 3, 4, 0},
			"shots/01-accueil.png": {0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0},
			"report.json":          []byte(`{"ok":true,"duration_ms":900,"shots":[{"file":"shots/01-accueil.png","label":"Accueil","criterion":1}]}`),
			"scenario.mjs":         []byte("export default async () => {}"),
			"evil.sh":              []byte("#!/bin/sh"),
		}
		for name, b := range files {
			p := filepath.Join(inbox, name)
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	f, _ := ev.startToDev()
	ev.drain()
	tk := ev.ticket(f.ID)
	expect(t, "ticket validé", tk.State, "AWAITING_PR_APPROVAL")
	if !strings.Contains(instructions, "facultatif") || !strings.Contains(inbox, filepath.Join(ev.e.Recordings, "inbox", "demo", tk.Key+"-j")) {
		t.Fatalf("dossier %q ; consignes : %s", inbox, instructions)
	}
	if _, err := os.Stat(inbox); !os.IsNotExist(err) {
		t.Fatal("le dossier de dépôt doit être supprimé après ingestion")
	}
	recs, err := store.ListTicketRecordings(ctx, ev.s.Pool, tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, r := range recs {
		kinds = append(kinds, r.Kind+":"+r.Name)
		if r.Kind == "screenshot" && (r.Label != "Accueil" || r.Criterion == nil || *r.Criterion != 1) {
			t.Fatalf("capture : %+v", r)
		}
	}
	if strings.Join(kinds, ",") != "report:report.json,scenario:scenario.mjs,screenshot:shots/01-accueil.png,trace:trace.zip,video:video.webm" {
		t.Fatalf("enregistrements : %v", kinds)
	}
	runID := recs[0].RunID
	if _, err := os.Stat(filepath.Join(ev.e.Recordings, "store", strconv.FormatInt(runID, 10), "video.webm")); err != nil {
		t.Fatal("vidéo absente du stockage")
	}
	if _, err := os.Stat(filepath.Join(ev.e.Recordings, "store", strconv.FormatInt(runID, 10), "evil.sh")); !os.IsNotExist(err) {
		t.Fatal("fichier hors liste copié")
	}
	var n int
	if err := ev.s.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action='ticket.qa_recorded' AND subject_id=$1`, tk.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit : %d %v", n, err)
	}

	// Purge : rien tant que non expiré, puis fichiers et lignes supprimés.
	if err := ev.e.PurgeRecordings(ctx); err != nil {
		t.Fatal(err)
	}
	if recs, _ := store.ListTicketRecordings(ctx, ev.s.Pool, tk.ID); len(recs) != 5 {
		t.Fatalf("purge prématurée : %d", len(recs))
	}
	if _, err := ev.s.Pool.Exec(ctx, `UPDATE qa_recordings SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if err := ev.e.PurgeRecordings(ctx); err != nil {
		t.Fatal(err)
	}
	if err := ev.s.Pool.QueryRow(ctx, `SELECT count(*) FROM qa_recordings`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("lignes après purge : %d %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(ev.e.Recordings, "store", strconv.FormatInt(runID, 10))); !os.IsNotExist(err) {
		t.Fatal("dossier du run non purgé")
	}
}

func TestQAWithoutBrowserHasNoRecordingRules(t *testing.T) {
	ev := newEnv(t)
	ev.e.Recordings = t.TempDir()
	var instructions string
	ev.qaHook = func(req hermes.RunRequest) { instructions = req.Instructions }
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "ticket validé", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")
	if instructions == "" || strings.Contains(instructions, "aos-qa-browser") {
		t.Fatalf("projet sans QA navigateur : %s", instructions)
	}
	if entries, _ := os.ReadDir(filepath.Join(ev.e.Recordings, "inbox")); len(entries) != 0 {
		t.Fatal("aucun dossier de dépôt attendu")
	}
}
