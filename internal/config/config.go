// Package config lit la configuration (variables d'environnement) et les secrets (fichiers).
// Les secrets ne sont jamais lus depuis l'environnement ni journalisés.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

// Config regroupe la configuration non secrète.
type Config struct {
	HTTPAddr        string
	DatabaseURLFile string
	SecretsDir      string
	WorkspaceRoot   string
	RecordingsRoot  string
	HermesURL       string
	Workers         int
	LeaseSeconds    int
	MaxRework       int
	AdminLogin      string
	GitBinary       string
	GitHubAPIURL    string
	GitHubWebURL    string
	TelegramAPIURL  string
	CookieSecure    bool
}

// Lease renvoie la durée de lease.
func (c Config) Lease() time.Duration { return time.Duration(c.LeaseSeconds) * time.Second }

// Load lit la configuration depuis l'environnement.
func Load() (Config, error) {
	c := Config{
		HTTPAddr:        env("AOS_HTTP_ADDR", "127.0.0.1:8788"),
		DatabaseURLFile: os.Getenv("AOS_DATABASE_URL_FILE"),
		SecretsDir:      env("AOS_SECRETS_DIR", "/run/secrets/agentic-os"),
		WorkspaceRoot:   env("AOS_WORKSPACE_ROOT", "/home/hermes/agentic-os-data/workspaces"),
		RecordingsRoot:  env("AOS_RECORDINGS_ROOT", "/home/hermes/agentic-os-data/recordings"),
		HermesURL:       strings.TrimRight(env("AOS_HERMES_URL", "http://127.0.0.1:8642"), "/"),
		AdminLogin:      env("AOS_ADMIN_LOGIN", "admin"),
		GitBinary:       env("AOS_GIT_BINARY", "/usr/bin/git"),
		GitHubAPIURL:    strings.TrimRight(env("AOS_GITHUB_API_URL", "https://api.github.com"), "/"),
		GitHubWebURL:    strings.TrimRight(env("AOS_GITHUB_WEB_URL", "https://github.com"), "/"),
		TelegramAPIURL:  strings.TrimRight(env("AOS_TELEGRAM_API_URL", "https://api.telegram.org"), "/"),
		CookieSecure:    os.Getenv("AOS_COOKIE_SECURE") == "1",
	}
	var err error
	if c.Workers, err = envInt("AOS_WORKERS", 2, 1, 32); err != nil {
		return c, err
	}
	if c.LeaseSeconds, err = envInt("AOS_LEASE_SECONDS", 60, 6, 3600); err != nil {
		return c, err
	}
	if c.MaxRework, err = envInt("AOS_MAX_REWORK", 3, 0, 20); err != nil {
		return c, err
	}
	if !filepath.IsAbs(c.WorkspaceRoot) || !filepath.IsAbs(c.SecretsDir) || !filepath.IsAbs(c.GitBinary) || !filepath.IsAbs(c.RecordingsRoot) {
		return c, errors.New("AOS_WORKSPACE_ROOT, AOS_RECORDINGS_ROOT, AOS_SECRETS_DIR et AOS_GIT_BINARY doivent être des chemins absolus")
	}
	c.WorkspaceRoot = filepath.Clean(c.WorkspaceRoot)
	c.RecordingsRoot = filepath.Clean(c.RecordingsRoot)
	return c, nil
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def, min, max int) (int, error) {
	v := strings.TrimSpace(os.Getenv(k))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s doit être un entier entre %d et %d", k, min, max)
	}
	return n, nil
}

// Secrets lit des fichiers dans le dossier des secrets.
type Secrets struct {
	Dir string
}

// ErrSecretMissing indique un secret absent.
var ErrSecretMissing = errors.New("secret absent")

// Path renvoie le chemin d'un secret (nom validé).
func (s Secrets) Path(name string) (string, error) {
	if name == "" || strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("nom de secret invalide")
	}
	return filepath.Join(s.Dir, filepath.FromSlash(name)), nil
}

// Read lit un secret (espaces de fin supprimés). Le contenu n'apparaît jamais dans l'erreur.
func (s Secrets) Read(name string) (string, error) {
	p, err := s.Path(name)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrSecretMissing, name)
		}
		return "", fmt.Errorf("lecture du secret %s impossible", name)
	}
	if len(b) > 64*1024 {
		return "", fmt.Errorf("secret %s trop volumineux", name)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%w: %s (vide)", ErrSecretMissing, name)
	}
	return v, nil
}

// HermesKey lit la clé API d'un profil Hermes : <secrets>/hermes/<profil>.key.
func (s Secrets) HermesKey(profile string) (string, error) {
	if !domain.ValidProfile(profile) {
		return "", fmt.Errorf("profil Hermes invalide")
	}
	return s.Read("hermes/" + profile + ".key")
}

// HermesProfiles liste les profils Hermes utilisables : ceux dont la clé <secrets>/hermes/<profil>.key
// existe (nom validé, tri alphabétique). Aucune clé n'est lue.
func (s Secrets) HermesProfiles() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.Dir, "hermes"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture des profils Hermes impossible")
	}
	out := []string{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".key")
		if ok && e.Type().IsRegular() && domain.ValidProfile(name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// SessionKey lit la clé de session (32 octets minimum).
func (s Secrets) SessionKey() ([]byte, error) {
	v, err := s.Read("session_key")
	if err != nil {
		return nil, err
	}
	if len(v) < 32 {
		return nil, errors.New("session_key doit contenir au moins 32 octets")
	}
	return []byte(v), nil
}

// DatabaseURL lit le DSN depuis AOS_DATABASE_URL_FILE (ou AOS_DATABASE_URL pour les outils de test).
func (c Config) DatabaseURL() (string, error) {
	if c.DatabaseURLFile == "" {
		if v := os.Getenv("AOS_DATABASE_URL"); v != "" {
			return v, nil
		}
		return "", errors.New("AOS_DATABASE_URL_FILE non défini")
	}
	b, err := os.ReadFile(c.DatabaseURLFile)
	if err != nil {
		return "", errors.New("lecture du fichier DSN impossible")
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", errors.New("fichier DSN vide")
	}
	return v, nil
}
