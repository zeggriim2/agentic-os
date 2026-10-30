package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsAndValidation(t *testing.T) {
	for _, k := range []string{"AOS_HTTP_ADDR", "AOS_WORKERS", "AOS_LEASE_SECONDS", "AOS_MAX_REWORK", "AOS_WORKSPACE_ROOT", "AOS_RECORDINGS_ROOT",
		"AOS_SECRETS_DIR", "AOS_GIT_BINARY", "AOS_PUBLIC_TZ", "AOS_HERMES_URL"} {
		t.Setenv(k, "")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != "127.0.0.1:8788" || c.Workers != 2 || c.LeaseSeconds != 60 || c.MaxRework != 3 {
		t.Fatalf("défauts inattendus: %+v", c)
	}
	t.Setenv("AOS_WORKERS", "abc")
	if _, err := Load(); err == nil {
		t.Fatal("AOS_WORKERS invalide accepté")
	}
	t.Setenv("AOS_WORKERS", "1")
	t.Setenv("AOS_WORKSPACE_ROOT", "relatif")
	if _, err := Load(); err == nil {
		t.Fatal("racine relative acceptée")
	}
}

func TestSecrets(t *testing.T) {
	dir := t.TempDir()
	s := Secrets{Dir: dir}
	if err := os.MkdirAll(filepath.Join(dir, "hermes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hermes", "aos-po.key"), []byte("k-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k, err := s.HermesKey("aos-po")
	if err != nil || k != "k-123" {
		t.Fatalf("clé: %q %v", k, err)
	}
	if _, err := s.HermesKey("../x"); err == nil {
		t.Fatal("profil invalide accepté")
	}
	if _, err := s.Read("../etc/passwd"); err == nil {
		t.Fatal("traversée acceptée")
	}
	_ = os.WriteFile(filepath.Join(dir, "session_key"), []byte("court"), 0o600)
	if _, err := s.SessionKey(); err == nil {
		t.Fatal("clé de session courte acceptée")
	}
	_ = os.WriteFile(filepath.Join(dir, "github_token"), []byte("ghp_secretvalue"), 0o600)
	_, err = s.Read("absent")
	if err == nil || strings.Contains(err.Error(), "ghp_") {
		t.Fatal("erreur inattendue")
	}
}

func TestHermesProfilesListsOnlyValidKeyFiles(t *testing.T) {
	dir := t.TempDir()
	s := Secrets{Dir: dir}
	if got, err := s.HermesProfiles(); err != nil || len(got) != 0 {
		t.Fatalf("sans dossier hermes : %v %v", got, err)
	}
	_ = os.MkdirAll(filepath.Join(dir, "hermes", "sub.key"), 0o700)
	for _, f := range []string{"aos-qa.key", "aos-fz-developer.key", "Bad.key", "notes.txt", ".hidden.key"} {
		_ = os.WriteFile(filepath.Join(dir, "hermes", f), []byte("k"), 0o600)
	}
	got, err := s.HermesProfiles()
	if err != nil || strings.Join(got, ",") != "aos-fz-developer,aos-qa" {
		t.Fatalf("profils : %v %v", got, err)
	}
}
