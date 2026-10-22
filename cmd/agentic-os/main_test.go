package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestAdminPasswordWritesBcrypt0600(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "admin_password_hash")
	var stdout, stderr bytes.Buffer
	code := run([]string{"admin-password", "-o", out}, strings.NewReader("un-mot-de-passe-long\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code %d: %s", code, stderr.String())
	}
	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("permissions: %v %v", st, err)
	}
	b, _ := os.ReadFile(out)
	if bcrypt.CompareHashAndPassword(bytes.TrimSpace(b), []byte("un-mot-de-passe-long")) != nil {
		t.Fatal("hash invalide")
	}
	if strings.Contains(stdout.String()+stderr.String(), "un-mot-de-passe-long") {
		t.Fatal("mot de passe affiché")
	}
	if code := run([]string{"admin-password", "-o", out}, strings.NewReader("court\n"), &stdout, &stderr); code == 0 {
		t.Fatal("mot de passe court accepté")
	}
}

func TestDoctorJSONAndExitCode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AOS_SECRETS_DIR", dir)
	t.Setenv("AOS_WORKSPACE_ROOT", filepath.Join(dir, "ws"))
	t.Setenv("AOS_DATABASE_URL_FILE", filepath.Join(dir, "dsn"))
	t.Setenv("AOS_HERMES_URL", "http://127.0.0.1:1")
	t.Setenv("AOS_GITHUB_API_URL", "http://127.0.0.1:1")
	t.Setenv("AOS_TELEGRAM_API_URL", "http://127.0.0.1:1")
	_ = os.WriteFile(filepath.Join(dir, "dsn"), []byte("postgres://x:y@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"), 0o600)
	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor"}, nil, &stdout, &stderr)
	if code == 0 {
		t.Fatal("doctor devrait échouer sans base ni secrets")
	}
	var rep Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("sortie non JSON: %v\n%s", err, stdout.String())
	}
	status := map[string]string{}
	for _, c := range rep.Checks {
		status[c.Name] = c.Status
	}
	if rep.OK || status["database"] != "fail" || status["workspace"] != "ok" || status["git"] != "ok" || status["secret:session_key"] != "fail" {
		t.Fatalf("rapport: %+v", rep)
	}
}
