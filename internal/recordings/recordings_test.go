package recordings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	webm = []byte{0x1A, 0x45, 0xDF, 0xA3, 1, 2, 3}
	zip  = []byte{'P', 'K', 3, 4, 9, 9}
	png  = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0, 0}
)

func write(t *testing.T, dir, name string, b []byte) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestInboxDirValidatesNames(t *testing.T) {
	d, err := InboxDir("/r", "fraiszen", "fraiszen-F6-T4", 12, 2)
	if err != nil || d != "/r/inbox/fraiszen/fraiszen-F6-T4-j12-a2" {
		t.Fatalf("%q %v", d, err)
	}
	for _, c := range [][2]string{{"../x", "k"}, {"fz", "../k"}, {"fz", "a/b"}} {
		if _, err := InboxDir("/r", c[0], c[1], 1, 1); err == nil {
			t.Fatalf("refus attendu : %v", c)
		}
	}
}

func TestIngestAcceptsOnlyKnownValidFiles(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "store", "7")
	write(t, src, "video.webm", webm)
	write(t, src, "trace.zip", zip)
	write(t, src, "scenario.mjs", []byte("export default async () => {}"))
	write(t, src, "shots/01-recapitulatif.png", png)
	write(t, src, "shots/02-erreur.png", png)
	write(t, src, "logs/api.log", []byte("GET /api/summary 200"))
	write(t, src, "report.json", []byte(`{"ok":false,"error":"x","duration_ms":12,"shots":[
		{"file":"shots/01-recapitulatif.png","label":"Récapitulatif 2025","criterion":2},
		{"file":"shots/02-erreur.png","label":"`+strings.Repeat("é", 300)+`","criterion":-1}],
		"console_errors":["TypeError"],"failed_requests":["500 GET /api/summary"]}`))
	// Refusés : nom hors liste, faux PNG, dossier inconnu, lien symbolique, vidéo au mauvais format.
	write(t, src, "evil.sh", []byte("#!/bin/sh"))
	write(t, src, "shots/03-faux.png", []byte("pas un png"))
	write(t, src, "node_modules/x.js", []byte("x"))
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "shots", "04-lien.png")); err != nil {
		t.Fatal(err)
	}
	files, report, skipped, err := Ingest(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range files {
		names = append(names, f.Kind+":"+f.Name)
		if len(f.SHA256) != 64 || f.Size == 0 {
			t.Fatalf("empreinte/taille : %+v", f)
		}
	}
	want := "log:logs/api.log,report:report.json,scenario:scenario.mjs,screenshot:shots/01-recapitulatif.png,screenshot:shots/02-erreur.png,trace:trace.zip,video:video.webm"
	if strings.Join(names, ",") != want {
		t.Fatalf("fichiers :\n%s\nattendu :\n%s", strings.Join(names, ","), want)
	}
	if len(skipped) != 4 {
		t.Fatalf("refusés : %v", skipped)
	}
	if report == nil || report.OK || len(report.FailedRequests) != 1 {
		t.Fatalf("rapport : %+v", report)
	}
	for _, f := range files {
		switch f.Name {
		case "shots/01-recapitulatif.png":
			if f.Label != "Récapitulatif 2025" || f.Criterion == nil || *f.Criterion != 2 {
				t.Fatalf("capture 1 : %+v", f)
			}
		case "shots/02-erreur.png":
			if len([]rune(f.Label)) != 200 || f.Criterion != nil {
				t.Fatalf("capture 2 (libellé tronqué, critère invalide ignoré) : %q %v", f.Label, f.Criterion)
			}
		}
	}
	// Le stockage ne contient que les fichiers acceptés ; lecture par nom validé uniquement.
	if _, err := os.Stat(filepath.Join(dst, "evil.sh")); !os.IsNotExist(err) {
		t.Fatal("fichier refusé copié")
	}
	root := filepath.Dir(filepath.Dir(dst))
	f, err := OpenStored(root, 7, "shots/01-recapitulatif.png")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, bad := range []string{"../7/video.webm", "/etc/passwd", "shots/../../x", "Video.webm"} {
		if _, err := OpenStored(root, 7, bad); err == nil {
			t.Fatalf("nom accepté : %q", bad)
		}
	}
}

func TestIngestLimits(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	big := make([]byte, MaxReport+1)
	write(t, src, "report.json", big)
	for i := 0; i < MaxShots+3; i++ {
		write(t, src, "shots/"+strings.Repeat("a", 1)+string(rune('a'+i%26))+string(rune('a'+i/26))+".png", png)
	}
	files, _, skipped, err := Ingest(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != MaxShots || len(skipped) != 4 {
		t.Fatalf("captures retenues %d, refusées %v", len(files), skipped)
	}
}

func TestPurgeInboxRemovesOldDirsOnly(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "inbox", "fz", "fz-F1-T1-j1-a1")
	recent := filepath.Join(root, "inbox", "fz", "fz-F1-T1-j2-a1")
	for _, d := range []string{old, recent} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	n, err := PurgeInbox(root, 24*time.Hour, time.Now())
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatal("dossier récent supprimé")
	}
	if n, err := PurgeInbox(t.TempDir(), time.Hour, time.Now()); err != nil || n != 0 {
		t.Fatalf("racine vide : %d %v", n, err)
	}
}
