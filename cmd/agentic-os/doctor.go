package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/zeggriim2/agentic-os/internal/config"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Check est un point de contrôle du doctor.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail,omitempty"`
}

// Report est la sortie JSON du doctor.
type Report struct {
	OK       bool    `json:"ok"`
	Version  string  `json:"version"`
	Revision string  `json:"revision"`
	Checks   []Check `json:"checks"`
}

func (r *Report) add(name, status, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail})
	if status == "fail" {
		r.OK = false
	}
}

func doctor(ctx context.Context, cfg config.Config) *Report {
	rep := &Report{OK: true, Version: version, Revision: revision}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	d := buildDeps(cfg, nopLogger())

	// secrets indispensables
	if _, err := d.secrets.SessionKey(); err != nil {
		rep.add("secret:session_key", "fail", "absent ou < 32 octets")
	} else {
		rep.add("secret:session_key", "ok", "")
	}
	if _, err := d.secrets.Read("admin_password_hash"); err != nil {
		rep.add("secret:admin_password_hash", "fail", "absent (utilisez `agentic-os admin-password`)")
	} else {
		rep.add("secret:admin_password_hash", "ok", "")
	}
	if _, err := d.secrets.Read("github_webhook_secret"); err != nil {
		rep.add("secret:github_webhook_secret", "warn", "absent : webhooks refusés, le poller (60 s) prend le relais")
	} else {
		rep.add("secret:github_webhook_secret", "ok", "")
	}

	// base et migrations
	profiles := []string{"aos-po", "aos-tech-lead", "aos-developer", "aos-reviewer", "aos-qa"}
	var repos []string
	s, err := openStore(ctx, cfg)
	if err != nil {
		rep.add("database", "fail", err.Error())
		rep.add("migrations", "fail", "base injoignable")
	} else {
		defer s.Close()
		rep.add("database", "ok", "")
		v, err := s.SchemaVersion(ctx)
		switch {
		case err != nil:
			rep.add("migrations", "fail", err.Error())
		case v != store.ExpectedSchemaVersion():
			rep.add("migrations", "fail", "schéma non à jour : lancez `agentic-os migrate`")
		default:
			rep.add("migrations", "ok", "")
			if agents, err := store.ListAgents(ctx, s.Pool); err == nil {
				profiles = profiles[:0]
				seen := map[string]bool{}
				for _, a := range agents {
					if a.Enabled && !seen[a.HermesProfile] {
						seen[a.HermesProfile] = true
						profiles = append(profiles, a.HermesProfile)
					}
				}
			}
			// Profils propres aux projets actifs (ex. aos-fraiszen-developer).
			if extra, err := store.ProjectAgentProfiles(ctx, s.Pool); err == nil {
				seen := map[string]bool{}
				for _, p := range profiles {
					seen[p] = true
				}
				for _, p := range extra {
					if !seen[p] {
						seen[p] = true
						profiles = append(profiles, p)
					}
				}
			}
			if ps, err := store.ListProjects(ctx, s.Pool, store.Page{Limit: 200}); err == nil {
				for _, p := range ps {
					if p.Enabled {
						repos = append(repos, p.Repository)
					}
				}
			}
		}
	}

	// Hermes par profil
	for _, p := range profiles {
		if _, err := d.secrets.HermesKey(p); err != nil {
			rep.add("hermes:"+p, "fail", "clé absente ("+filepath.Join("hermes", p+".key")+")")
			continue
		}
		if err := d.hermes.Capabilities(ctx, p); err != nil {
			rep.add("hermes:"+p, "fail", err.Error())
		} else {
			rep.add("hermes:"+p, "ok", "")
		}
	}

	// GitHub
	if _, err := d.secrets.Read("github_token"); err != nil {
		rep.add("github:token", "fail", "secret github_token absent")
	} else if err := d.github.Check(ctx); err != nil {
		rep.add("github:token", "fail", err.Error())
	} else {
		rep.add("github:token", "ok", "")
		for _, r := range repos {
			if err := d.github.RepoAccess(ctx, r); err != nil {
				rep.add("github:"+r, "fail", err.Error())
			} else {
				rep.add("github:"+r, "ok", "")
			}
		}
	}

	// Telegram (non bloquant)
	if err := d.telegram.Check(ctx); err != nil {
		rep.add("telegram", "warn", err.Error())
	} else {
		rep.add("telegram", "ok", "")
	}

	// Workspace et git
	if err := os.MkdirAll(cfg.WorkspaceRoot, 0o750); err != nil {
		rep.add("workspace", "fail", "racine non créable")
	} else if f, err := os.CreateTemp(cfg.WorkspaceRoot, ".aos-doctor-*"); err != nil {
		rep.add("workspace", "fail", "racine non inscriptible")
	} else {
		f.Close()
		os.Remove(f.Name())
		rep.add("workspace", "ok", cfg.WorkspaceRoot)
	}
	cmd := exec.CommandContext(ctx, cfg.GitBinary, "--version")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if out, err := cmd.Output(); err != nil {
		rep.add("git", "fail", cfg.GitBinary+" introuvable")
	} else {
		rep.add("git", "ok", string(trimNL(out)))
	}
	return rep
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}
