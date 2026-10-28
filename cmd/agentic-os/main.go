// Commande agentic-os : serve | migrate | doctor | admin-password.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // base de fuseaux embarquée (l'image alpine n'a pas tzdata)

	"golang.org/x/crypto/bcrypt"

	"github.com/zeggriim2/agentic-os/internal/config"
	"github.com/zeggriim2/agentic-os/internal/engine"
	gh "github.com/zeggriim2/agentic-os/internal/github"
	"github.com/zeggriim2/agentic-os/internal/gitops"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/httpapi"
	"github.com/zeggriim2/agentic-os/internal/notify"
	"github.com/zeggriim2/agentic-os/internal/store"
	"github.com/zeggriim2/agentic-os/web"
)

// Renseignés par -ldflags.
var (
	version  = "dev"
	revision = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage : agentic-os <serve|migrate|doctor|admin-password [-o fichier]|version>")
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	log := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).With("version", version)
	slog.SetDefault(log)
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "%s %s\n", version, revision)
		return 0
	case "admin-password":
		return adminPassword(args[1:], stdin, stdout, stderr)
	}
	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration invalide", "error", err.Error())
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "serve":
		return serve(ctx, cfg, log)
	case "migrate":
		return migrate(ctx, cfg, stdout, log)
	case "doctor":
		rep := doctor(ctx, cfg)
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
		if !rep.OK {
			return 1
		}
		return 0
	}
	usage(stderr)
	return 2
}

// ------------------------------------------------------------------ admin-password

func adminPassword(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	out := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-o", "--output":
			if i+1 >= len(args) {
				usage(stderr)
				return 2
			}
			out = args[i+1]
			i++
		default:
			usage(stderr)
			return 2
		}
	}
	if out == "" {
		dir := os.Getenv("AOS_SECRETS_DIR")
		if dir == "" {
			dir = "/run/secrets/agentic-os"
		}
		out = filepath.Join(dir, "admin_password_hash")
	}
	line, err := bufio.NewReader(io.LimitReader(stdin, 4096)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(stderr, "lecture du mot de passe impossible")
		return 1
	}
	pw := strings.TrimRight(line, "\r\n")
	if len(pw) < 12 {
		fmt.Fprintln(stderr, "mot de passe trop court (12 caractères minimum)")
		return 1
	}
	if len(pw) > 72 {
		fmt.Fprintln(stderr, "mot de passe trop long (72 octets maximum pour bcrypt)")
		return 1
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		fmt.Fprintln(stderr, "hachage impossible")
		return 1
	}
	if err := writeFile0600(out, append(hash, '\n')); err != nil {
		fmt.Fprintf(stderr, "écriture de %s impossible : %v\n", out, err)
		return 1
	}
	fmt.Fprintf(stdout, "hash bcrypt écrit dans %s (0600)\n", out)
	return 0
}

func writeFile0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".aos-tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ------------------------------------------------------------------ migrate

func openStore(ctx context.Context, cfg config.Config) (*store.Store, error) {
	dsn, err := cfg.DatabaseURL()
	if err != nil {
		return nil, err
	}
	return store.Open(ctx, dsn)
}

func migrate(ctx context.Context, cfg config.Config, stdout io.Writer, log *slog.Logger) int {
	s, err := openStore(ctx, cfg)
	if err != nil {
		log.Error("base de données", "error", err.Error())
		return 1
	}
	defer s.Close()
	applied, err := s.Migrate(ctx)
	if err != nil {
		log.Error("migrations en échec", "error", err.Error())
		return 1
	}
	v, _ := s.SchemaVersion(ctx)
	if applied == nil {
		applied = []int{}
	}
	_ = json.NewEncoder(stdout).Encode(map[string]any{"applied": applied, "schema_version": v})
	return 0
}

// ------------------------------------------------------------------ dépendances

type deps struct {
	secrets  config.Secrets
	hermes   *hermes.Client
	github   *gh.Client
	telegram *notify.Telegram
	git      *gitops.Manager
}

func buildDeps(cfg config.Config, log *slog.Logger) deps {
	sec := config.Secrets{Dir: cfg.SecretsDir}
	tokenFile, _ := sec.Path("github_token")
	return deps{
		secrets:  sec,
		hermes:   hermes.New(cfg.HermesURL, sec),
		github:   gh.New(cfg.GitHubAPIURL, func() (string, error) { return sec.Read("github_token") }),
		telegram: notify.New(cfg.TelegramAPIURL, func() (string, error) { return sec.Read("telegram_bot_token") }, func() (string, error) { return sec.Read("telegram_chat_id") }, log),
		git: &gitops.Manager{
			Git:       gitops.Runner{GitPath: cfg.GitBinary, Timeout: 10 * time.Minute, MaxOutput: 1 << 20},
			Root:      cfg.WorkspaceRoot,
			RemoteURL: gitops.GitHubRemote(cfg.GitHubWebURL),
			TokenFile: tokenFile,
		},
	}
}

// ------------------------------------------------------------------ serve

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) int {
	host, _, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		log.Error("AOS_HTTP_ADDR invalide")
		return 2
	}
	if ip := net.ParseIP(host); (ip == nil || !ip.IsLoopback()) && os.Getenv("AOS_ALLOW_NON_LOOPBACK") != "1" {
		log.Error("refus d'écouter hors loopback (AOS_HTTP_ADDR doit être 127.0.0.1:<port>)")
		return 2
	}
	s, err := openStore(ctx, cfg)
	if err != nil {
		log.Error("base de données", "error", err.Error())
		return 1
	}
	defer s.Close()
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != store.ExpectedSchemaVersion() {
		log.Error("schéma non à jour : lancez `agentic-os migrate`", "schema_version", v, "expected", store.ExpectedSchemaVersion())
		return 1
	}
	d := buildDeps(cfg, log)
	sessionKey, err := d.secrets.SessionKey()
	if err != nil {
		log.Error("session_key absente ou trop courte (32 octets minimum)", "error", err.Error())
		return 1
	}
	if err := os.MkdirAll(cfg.WorkspaceRoot, 0o750); err != nil {
		log.Error("racine des workspaces inaccessible", "error", err.Error())
		return 1
	}
	eng := &engine.Engine{
		Store: s, Hermes: d.hermes, GitHub: d.github, Git: d.git, Notifier: d.telegram, Log: log,
		Workers: cfg.Workers, Lease: cfg.Lease(), MaxRework: cfg.MaxRework,
	}
	eng.Init()
	hub := httpapi.NewHub()
	api := &httpapi.Server{
		Store: s, Engine: eng, Log: log, Hub: hub, Web: web.Dist(), AdminLogin: cfg.AdminLogin,
		PasswordHash:  func() (string, error) { return d.secrets.Read("admin_password_hash") },
		SessionKey:    sessionKey,
		WebhookSecret: func() ([]byte, error) { v, err := d.secrets.Read("github_webhook_secret"); return []byte(v), err },
		CookieSecure:  cfg.CookieSecure,
		Version:       version, Revision: revision, Workers: cfg.Workers,
		Health:   httpapi.HealthDeps{Hermes: d.hermes, GitHub: d.github, Telegram: d.telegram},
		GitGraph: d.git, GitHubWebURL: cfg.GitHubWebURL,
		HermesProfiles: d.secrets.HermesProfiles,
		TelegramSend:   d.telegram.Send,
	}
	srv := &http.Server{
		Addr: cfg.HTTPAddr, Handler: api.Router(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		MaxHeaderBytes: 64 << 10,
	}
	bg, cancel := context.WithCancel(ctx)
	defer cancel()
	go eng.Start(bg)
	go httpapi.ListenPG(bg, s.Pool, hub, log)
	errc := make(chan error, 1)
	go func() {
		log.Info("écoute HTTP", "addr", cfg.HTTPAddr, "workers", cfg.Workers)
		errc <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serveur HTTP", "error", err.Error())
			return 1
		}
	}
	log.Info("arrêt demandé")
	sctx, scancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer scancel()
	_ = srv.Shutdown(sctx)
	cancel()
	return 0
}

func nopLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }
