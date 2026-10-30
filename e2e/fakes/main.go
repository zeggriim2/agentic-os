// Commande aos-e2e-fakes : faux services externes pour les tests E2E navigateur d'Agentic OS.
//
// Un seul processus sert, sur une adresse loopback :
//   - une fausse API Hermes (/p/<profil>/v1/...) compatible avec internal/hermes ;
//   - une fausse API REST GitHub (/repos/..., /search/issues, /rate_limit) compatible avec
//     internal/github, dont les branches sont lues dans de vrais dépôts Git bare locaux ;
//   - un faux Bot API Telegram (/bot<token>/sendMessage, /bot<token>/getMe) ;
//   - des points de contrôle pour les tests (/__e2e/...), dont l'envoi de webhooks GitHub
//     signés HMAC vers Agentic OS.
//
// Aucun appel réseau externe : tout reste en mémoire ou dans le dossier des dépôts bare.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type config struct {
	addr          string
	secretsDir    string
	reposRoot     string
	workspaceRoot string
	gitBinary     string
	webURL        string
	webhookURL    string
	checks        []string
	seed          []string
}

func main() {
	var c config
	var checks, seed string
	flag.StringVar(&c.addr, "addr", "127.0.0.1:0", "adresse d'écoute (loopback)")
	flag.StringVar(&c.secretsDir, "secrets-dir", "", "dossier des secrets d'Agentic OS (clés Hermes, token GitHub, secret webhook)")
	flag.StringVar(&c.reposRoot, "repos-root", "", "racine des dépôts bare <owner>/<repo>.git")
	flag.StringVar(&c.workspaceRoot, "workspace-root", "", "racine des worktrees d'Agentic OS (seul endroit où le faux développeur écrit)")
	flag.StringVar(&c.gitBinary, "git", "/usr/bin/git", "binaire git absolu")
	flag.StringVar(&c.webURL, "web-url", "https://github.example.test", "base des URL html_url renvoyées")
	flag.StringVar(&c.webhookURL, "webhook-url", "", "URL du webhook GitHub d'Agentic OS")
	flag.StringVar(&checks, "checks", "tests", "checks CI renvoyés en succès (séparés par des virgules)")
	flag.StringVar(&seed, "seed", "", "dépôts bare à créer avec une branche main (owner/repo, séparés par des virgules)")
	flag.Parse()
	c.checks = splitList(checks)
	c.seed = splitList(seed)
	if c.secretsDir == "" || c.reposRoot == "" || c.workspaceRoot == "" || !filepath.IsAbs(c.gitBinary) {
		log.Fatal("-secrets-dir, -repos-root et -workspace-root sont obligatoires ; -git doit être absolu")
	}
	host, _, err := net.SplitHostPort(c.addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("-addr doit être une adresse loopback")
	}
	f := newFakes(c)
	for _, repo := range c.seed {
		if err := f.seedRepo(repo); err != nil {
			log.Fatalf("création du dépôt %s : %v", repo, err)
		}
	}
	ln, err := net.Listen("tcp", c.addr)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{Handler: f.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("faux services E2E à l'écoute sur http://%s", ln.Addr())
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ------------------------------------------------------------------ état

type fakes struct {
	cfg config

	mu sync.Mutex
	// Hermes
	runs    map[string]*run
	idem    map[string]string // profil + clé d'idempotence → run id
	held    map[string]bool   // rôles retenus (le run attend /__e2e/release)
	nextRun int
	// GitHub
	issues   map[string][]*issue // dépôt → issues
	pulls    map[string][]*pull  // dépôt → PR
	numbers  map[string]int      // dépôt → dernier numéro (partagé issues/PR comme GitHub)
	checkOvr map[string]checkRun // sha + nom → check imposé
	// Telegram
	messages []string
	// Webhooks envoyés
	deliveries []map[string]any
}

type run struct {
	ID          string
	Profile     string
	Role        string
	Key         string
	Digest      string
	Input       string
	Instr       string // instructions (dossier d'enregistrement du QA navigateur)
	Status      string
	Output      string
	Usage       map[string]int64
	Interrupted bool
	Error       string
	Model       string            // modèle servi (vide = fake-model)
	Requested   map[string]string // surcharge demandée, renvoyée dans le statut
	Events      []map[string]any
	release     chan struct{}
	changed     chan struct{} // fermé puis remplacé à chaque nouvel événement
	CreatedAt   time.Time
}

type issue struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	HTMLURL string `json:"html_url"`
}

type pull struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	Merged  bool   `json:"merged"`
	Head    string `json:"head"`
	Base    string `json:"base"`
	HeadSHA string `json:"head_sha"`
	HTMLURL string `json:"html_url"`
}

type checkRun struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

func newFakes(c config) *fakes {
	return &fakes{cfg: c, runs: map[string]*run{}, idem: map[string]string{}, held: map[string]bool{},
		issues: map[string][]*issue{}, pulls: map[string][]*pull{}, numbers: map[string]int{}, checkOvr: map[string]checkRun{}}
}

func (f *fakes) routes() http.Handler {
	mux := http.NewServeMux()
	// Hermes
	mux.HandleFunc("POST /p/{profile}/v1/runs", f.hermesAuth(f.createRun))
	mux.HandleFunc("GET /p/{profile}/v1/runs/{id}", f.hermesAuth(f.getRun))
	mux.HandleFunc("POST /p/{profile}/v1/runs/{id}/stop", f.hermesAuth(f.stopRun))
	mux.HandleFunc("GET /p/{profile}/v1/runs/{id}/events", f.hermesAuth(f.runEvents))
	mux.HandleFunc("GET /p/{profile}/v1/capabilities", f.hermesAuth(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"runs": true, "events": true, "stop": true, "idempotency": true})
	}))
	mux.HandleFunc("GET /p/{profile}/v1/models", f.hermesAuth(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"object": "list", "data": []map[string]any{{"id": "fake-model", "object": "model"}}})
	}))
	// Catalogue des modèles (api/model/options) : un fournisseur authentifié (fake-e2e), un non authentifié.
	mux.HandleFunc("GET /p/{profile}/api/model/options", f.hermesAuth(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"model": "fake-model", "provider": "fake-e2e", "providers": []map[string]any{
			{"slug": "fake-e2e", "name": "Fake E2E", "is_current": true, "authenticated": true, "total_models": 2,
				"models": []string{"fake-model", "fake-model-xl"}, "featured_models": []string{"fake-model"}, "warning": ""},
			{"slug": "absent", "name": "Absent", "is_current": false, "authenticated": false, "total_models": 0,
				"models": []string{}, "featured_models": []string{}, "warning": "non authentifié"},
		}})
	}))
	// GitHub
	mux.HandleFunc("GET /rate_limit", f.ghAuth(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"resources": map[string]any{"core": map[string]any{"limit": 5000, "remaining": 4999}}})
	}))
	mux.HandleFunc("GET /search/issues", f.ghAuth(f.searchIssues))
	mux.HandleFunc("GET /repos/{owner}/{repo}", f.ghAuth(f.getRepo))
	mux.HandleFunc("POST /repos/{owner}/{repo}/issues", f.ghAuth(f.createIssue))
	mux.HandleFunc("GET /repos/{owner}/{repo}/issues/{number}", f.ghAuth(f.getIssue))
	mux.HandleFunc("GET /repos/{owner}/{repo}/branches/{branch...}", f.ghAuth(f.getBranch))
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls", f.ghAuth(f.listPulls))
	mux.HandleFunc("POST /repos/{owner}/{repo}/pulls", f.ghAuth(f.createPull))
	mux.HandleFunc("GET /repos/{owner}/{repo}/pulls/{number}", f.ghAuth(f.getPull))
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/pulls/{number}", f.ghAuth(f.patchPull))
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{sha}/check-runs", f.ghAuth(f.checkRuns))
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{sha}/status", f.ghAuth(f.commitStatus))
	// Contrôle des tests
	mux.HandleFunc("GET /__e2e/health", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]any{"status": "ok"}) })
	mux.HandleFunc("GET /__e2e/state", f.state)
	mux.HandleFunc("POST /__e2e/hold", f.hold)
	mux.HandleFunc("POST /__e2e/release", f.release)
	mux.HandleFunc("POST /__e2e/checks", f.setCheck)
	mux.HandleFunc("POST /__e2e/webhook/check-run", f.sendCheckRunWebhook)
	mux.HandleFunc("POST /__e2e/merge", f.merge)
	// Telegram (/bot<token>/<méthode>) et 404 JSON
	mux.HandleFunc("/", f.telegramOrNotFound)
	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		path := r.URL.Path
		if strings.HasPrefix(path, "/bot") {
			path = "/bot***/" + filepath.Base(path) // ne jamais journaliser le token
		}
		log.Printf("%s %s (%s)", r.Method, path, time.Since(start).Round(time.Millisecond))
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func ghError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"message": msg, "documentation_url": "https://docs.github.com/rest"})
}

func readSecret(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func bearerOK(r *http.Request, want string) bool {
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ------------------------------------------------------------------ Hermes

var profileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// hermesAuth exige la clé API propre au profil (<secrets>/hermes/<profil>.key), comme Hermes.
func (f *fakes) hermesAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profile := r.PathValue("profile")
		if !profileRe.MatchString(profile) {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "profile_not_found"}})
			return
		}
		key, err := readSecret(f.cfg.secretsDir, "hermes/"+profile+".key")
		if err != nil {
			writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "profile_not_found"}})
			return
		}
		if !bearerOK(r, key) {
			writeJSON(w, 401, map[string]any{"error": map[string]any{"code": "unauthorized"}})
			return
		}
		h(w, r)
	}
}

func roleOf(instructions string) string {
	switch {
	case strings.Contains(instructions, "Tu es le Product Owner"):
		return "po"
	case strings.Contains(instructions, "Tu es le Tech Lead"):
		return "tech_lead"
	case strings.Contains(instructions, "Tu es le Développeur"):
		return "developer"
	case strings.Contains(instructions, "Tu es le Reviewer"):
		return "reviewer"
	case strings.Contains(instructions, "Tu es le QA"):
		return "qa"
	}
	return ""
}

func (f *fakes) createRun(w http.ResponseWriter, r *http.Request) {
	profile := r.PathValue("profile")
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": "idempotency_key_required"}})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": "invalid_body"}})
		return
	}
	var req struct {
		Input        string `json:"input"`
		Instructions string `json:"instructions"`
		Model        string `json:"model"`
		Provider     string `json:"provider"`
	}
	if err := json.Unmarshal(body, &req); err != nil || req.Input == "" {
		writeJSON(w, 400, map[string]any{"error": map[string]any{"code": "invalid_body"}})
		return
	}
	// Surcharge de modèle : un fournisseur hors catalogue authentifié échoue comme Hermes (résolution du
	// fournisseur), ce qui exerce le repli du moteur ; sinon le modèle demandé est servi.
	if req.Provider != "" && req.Provider != "fake-e2e" {
		f.mu.Lock()
		f.nextRun++
		id := fmt.Sprintf("run_e2e_%04d_%s", f.nextRun, randomHex(4))
		rn := &run{ID: id, Profile: profile, Role: roleOf(req.Instructions), Key: key, Input: req.Input, Status: "failed",
			Error: "⚠️ Échec d'authentification du fournisseur: " + req.Provider, Usage: map[string]int64{},
			Requested: map[string]string{"provider": req.Provider, "model": req.Model},
			release:   make(chan struct{}), changed: make(chan struct{}), CreatedAt: time.Now()}
		close(rn.release)
		f.runs[id] = rn
		f.idem[profile+"\x00"+key] = id
		f.mu.Unlock()
		writeJSON(w, 202, map[string]any{"run_id": id, "status": "started", "replayed": false})
		return
	}
	sum := sha256.Sum256([]byte(req.Instructions + "\x00" + req.Input + "\x00" + req.Provider + "/" + req.Model))
	digest := hex.EncodeToString(sum[:])
	f.mu.Lock()
	if id, ok := f.idem[profile+"\x00"+key]; ok {
		existing := f.runs[id]
		f.mu.Unlock()
		if existing.Digest != digest {
			writeJSON(w, 409, map[string]any{"error": map[string]any{"code": "idempotency_key_conflict"}})
			return
		}
		writeJSON(w, 202, map[string]any{"run_id": id, "status": "started", "replayed": true})
		return
	}
	f.nextRun++
	id := fmt.Sprintf("run_e2e_%04d_%s", f.nextRun, randomHex(4))
	rn := &run{ID: id, Profile: profile, Role: roleOf(req.Instructions), Key: key, Digest: digest, Input: req.Input, Instr: req.Instructions,
		Status: "running", release: make(chan struct{}), changed: make(chan struct{}), CreatedAt: time.Now()}
	if req.Model != "" {
		rn.Model = req.Model
		rn.Requested = map[string]string{"provider": req.Provider, "model": req.Model}
	}
	if !f.held[rn.Role] {
		close(rn.release)
	}
	f.runs[id] = rn
	f.idem[profile+"\x00"+key] = id
	f.mu.Unlock()
	go f.execute(rn, req.Instructions)
	writeJSON(w, 202, map[string]any{"run_id": id, "status": "started", "replayed": false})
}

func (f *fakes) lookupRun(w http.ResponseWriter, r *http.Request) *run {
	f.mu.Lock()
	rn := f.runs[r.PathValue("id")]
	f.mu.Unlock()
	if rn == nil || rn.Profile != r.PathValue("profile") {
		writeJSON(w, 404, map[string]any{"error": map[string]any{"code": "run_not_found"}})
		return nil
	}
	return rn
}

func (f *fakes) getRun(w http.ResponseWriter, r *http.Request) {
	rn := f.lookupRun(w, r)
	if rn == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	model, source := "fake-model", "global"
	if rn.Model != "" {
		model, source = rn.Model, "raw_request"
	}
	out := map[string]any{
		"run_id": rn.ID, "status": rn.Status, "output": rn.Output,
		"usage":        rn.Usage,
		"runtime":      map[string]any{"provider": "fake-e2e", "model": model, "route_source": source},
		"route_source": source,
		"completed":    rn.Status == "completed", "partial": false, "interrupted": rn.Interrupted,
	}
	if rn.Requested != nil {
		out["requested"] = rn.Requested
	}
	if rn.Error != "" {
		out["error"] = rn.Error
	}
	writeJSON(w, 200, out)
}

func (f *fakes) stopRun(w http.ResponseWriter, r *http.Request) {
	rn := f.lookupRun(w, r)
	if rn == nil {
		return
	}
	f.mu.Lock()
	if rn.Status == "running" {
		rn.Status, rn.Interrupted = "stopped", true
		f.emitLocked(rn, "run.failed", map[string]any{"reason": "stopped"})
	}
	select {
	case <-rn.release:
	default:
		close(rn.release)
	}
	f.mu.Unlock()
	writeJSON(w, 200, map[string]any{"run_id": rn.ID, "status": "stopping"})
}

// emitLocked ajoute un événement (f.mu tenu) et réveille les flux SSE.
func (f *fakes) emitLocked(rn *run, event string, data map[string]any) {
	ev := map[string]any{"event": event, "seq": len(rn.Events) + 1, "run_id": rn.ID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano)}
	for k, v := range data {
		ev[k] = v
	}
	rn.Events = append(rn.Events, ev)
	close(rn.changed)
	rn.changed = make(chan struct{})
}

func (f *fakes) emit(rn *run, event string, data map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitLocked(rn, event, data)
}

// runEvents diffuse le flux SSE (`data: {"event","seq",...}`) et le ferme après l'événement terminal.
func (f *fakes) runEvents(w http.ResponseWriter, r *http.Request) {
	rn := f.lookupRun(w, r)
	if rn == nil {
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]any{"error": "streaming non supporté"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	fl.Flush()
	sent := 0
	for {
		f.mu.Lock()
		pending := append([]map[string]any(nil), rn.Events[sent:]...)
		changed := rn.changed
		f.mu.Unlock()
		for _, ev := range pending {
			b, _ := json.Marshal(ev)
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["event"], b); err != nil {
				return
			}
			sent++
			if e := ev["event"]; e == "run.completed" || e == "run.failed" {
				fl.Flush()
				return
			}
		}
		fl.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-time.After(15 * time.Second):
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// execute simule le travail de l'agent : quelques événements, attente éventuelle d'une
// libération (/__e2e/release), puis le livrable propre au rôle.
func (f *fakes) execute(rn *run, instructions string) {
	time.Sleep(150 * time.Millisecond)
	f.emit(rn, "reasoning.available", map[string]any{"text": "Analyse de la demande (" + rn.Role + ")"})
	f.emit(rn, "tool.started", map[string]any{"tool": "terminal", "preview": "lecture du contexte"})
	f.emit(rn, "message.delta", map[string]any{"delta": "Travail en cours…"})
	<-rn.release
	f.mu.Lock()
	stopped := rn.Status != "running"
	f.mu.Unlock()
	if stopped {
		return
	}
	output, err := f.produce(rn)
	f.emit(rn, "tool.completed", map[string]any{"tool": "terminal", "exit_code": 0})
	f.mu.Lock()
	defer f.mu.Unlock()
	if rn.Status != "running" {
		return
	}
	if err != nil {
		rn.Status, rn.Error = "failed", err.Error()
		log.Printf("run %s (%s) en échec : %v", rn.ID, rn.Role, err)
		f.emitLocked(rn, "run.failed", map[string]any{"error": err.Error()})
		return
	}
	rn.Output = output
	in, out := int64(len(rn.Input)/4+len(instructions)/4), int64(len(output)/4)
	rn.Usage = map[string]int64{"input_tokens": in, "output_tokens": out, "total_tokens": in + out}
	rn.Status = "completed"
	f.emitLocked(rn, "run.completed", map[string]any{"usage": rn.Usage})
}

var (
	worktreeRe = regexp.MustCompile(`(?m)^Worktree(?: \(détaché\))? : (.+)$`)
	branchRe   = regexp.MustCompile(`(?m)^Branche : (\S+)$`)
	keyRe      = regexp.MustCompile(`(?m)^Clé : (\S+)$`)
	reviewSHA  = regexp.MustCompile(`(?m)^SHA à relire : ([0-9a-f]{40})$`)
	qaSHA      = regexp.MustCompile(`(?m)^SHA à tester : ([0-9a-f]{40})$`)
	titleRe    = regexp.MustCompile(`(?m)^Titre : (.+)$`)
)

func deliverable(prefix string, v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return prefix + "\n\n```json\n" + string(b) + "\n```\n", nil
}

func (f *fakes) produce(rn *run) (string, error) {
	in := rn.Input
	switch rn.Role {
	case "po":
		summary := "Spécification produite par le faux Hermes E2E."
		if strings.Contains(in, `source="human_feedback"`) {
			summary = "Spécification révisée selon le retour humain (faux Hermes E2E)."
		}
		return deliverable("Voici la spécification.", map[string]any{
			"summary":             summary,
			"user_stories":        []string{"En tant qu'opérateur, je veux un fichier de démonstration pour vérifier la chaîne complète."},
			"acceptance_criteria": []string{"Un fichier de démonstration existe sur la branche du ticket."},
			"open_questions":      []string{},
		})
	case "tech_lead":
		title := "feature"
		if m := titleRe.FindStringSubmatch(in); m != nil {
			title = strings.TrimSpace(m[1])
		}
		return deliverable("Conception technique.", map[string]any{
			"summary": "Un seul ticket : ajouter un fichier de démonstration.",
			"decisions": []map[string]any{{
				"decision": "Fichier Markdown à la racine", "rationale": "Changement minimal et vérifiable",
				"rejected_alternatives": []string{"Modifier le README"},
			}},
			"risks": []string{"Aucun risque notable (dépôt de démonstration)."},
			"tickets": []map[string]any{{
				"title": "Ajouter le fichier de démonstration",
				"body":  "Créer un fichier Markdown de démonstration pour « " + title + " ».",
				"acceptance_criteria": []string{
					"Le fichier de démonstration est commité sur la branche du ticket",
					"Le fichier mentionne la clé du ticket",
				},
			}},
		})
	case "developer":
		return f.develop(in)
	case "reviewer":
		m := reviewSHA.FindStringSubmatch(in)
		if m == nil {
			return "", errors.New("SHA à relire introuvable dans l'entrée")
		}
		return deliverable("Relecture terminée.", map[string]any{
			"verdict": "approved", "sha": m[1], "findings": []any{},
			"summary": "Diff minimal et conforme au ticket.",
		})
	case "qa":
		if err := recordQA(rn.Instr); err != nil {
			return "", err
		}
		return f.qa(in)
	}
	return "", errors.New("rôle inconnu (instructions non reconnues)")
}

// worktreeFrom extrait et valide le chemin du worktree préparé par Agentic OS.
func (f *fakes) worktreeFrom(in string) (string, error) {
	m := worktreeRe.FindStringSubmatch(in)
	if m == nil {
		return "", errors.New("worktree introuvable dans l'entrée")
	}
	wt := filepath.Clean(strings.TrimSpace(m[1]))
	root := filepath.Clean(f.cfg.workspaceRoot)
	rel, err := filepath.Rel(root, wt)
	if !filepath.IsAbs(wt) || err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("worktree %q hors de la racine autorisée", wt)
	}
	if st, err := os.Stat(wt); err != nil || !st.IsDir() {
		return "", fmt.Errorf("worktree %q absent", wt)
	}
	return wt, nil
}

func (f *fakes) git(dir string, args ...string) (string, error) {
	full := append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false",
		"-c", "user.name=Faux Hermes E2E", "-c", "user.email=fake-hermes@e2e.invalid"}, args...)
	cmd := exec.Command(f.cfg.gitBinary, full...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C", "LC_ALL=C",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s : %v : %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

func demoFile(key string) string { return "e2e-" + key + ".md" }

// develop écrit réellement un fichier et le commite dans le worktree, sur la branche indiquée.
func (f *fakes) develop(in string) (string, error) {
	wt, err := f.worktreeFrom(in)
	if err != nil {
		return "", err
	}
	bm, km := branchRe.FindStringSubmatch(in), keyRe.FindStringSubmatch(in)
	if bm == nil || km == nil {
		return "", errors.New("branche ou clé du ticket introuvable dans l'entrée")
	}
	cur, err := f.git(wt, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || cur != bm[1] {
		return "", fmt.Errorf("worktree sur %q au lieu de %q", cur, bm[1])
	}
	name := demoFile(km[1])
	content := fmt.Sprintf("# Démonstration %s\n\nFichier créé par le faux développeur E2E (%s).\n", km[1], time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(wt, name), []byte(content), 0o644); err != nil {
		return "", err
	}
	if _, err := f.git(wt, "add", "--", name); err != nil {
		return "", err
	}
	if _, err := f.git(wt, "commit", "-m", "feat: fichier de démonstration "+km[1]); err != nil {
		return "", err
	}
	return deliverable("Implémentation terminée.", map[string]any{
		"summary":       "Ajout du fichier " + name + ".",
		"files_changed": []string{name},
		"tests":         []map[string]any{{"command": "test -f " + name, "exit_code": 0}},
		"notes":         "Commit local uniquement, sans push.",
	})
}

// qa vérifie réellement la présence du fichier au SHA exact et reprend chaque critère du ticket.
func (f *fakes) qa(in string) (string, error) {
	m := qaSHA.FindStringSubmatch(in)
	if m == nil {
		return "", errors.New("SHA à tester introuvable dans l'entrée")
	}
	wt, err := f.worktreeFrom(in)
	if err != nil {
		return "", err
	}
	km := keyRe.FindStringSubmatch(in)
	if km == nil {
		return "", errors.New("clé du ticket introuvable")
	}
	criteria, err := acceptanceFrom(in)
	if err != nil {
		return "", err
	}
	head, err := f.git(wt, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	exit := 0
	if _, err := os.Stat(filepath.Join(wt, demoFile(km[1]))); err != nil || head != m[1] {
		exit = 1
	}
	status, verdict := "met", "passed"
	if exit != 0 {
		status, verdict = "not_met", "failed"
	}
	acc := make([]map[string]any, 0, len(criteria))
	for _, c := range criteria {
		acc = append(acc, map[string]any{"criterion": c, "status": status, "evidence": "fichier " + demoFile(km[1]) + " vérifié au SHA " + m[1][:12]})
	}
	return deliverable("QA terminée.", map[string]any{
		"verdict": verdict, "sha": m[1],
		"checks":     []map[string]any{{"command": "test -f " + demoFile(km[1]), "exit_code": exit}},
		"acceptance": acc,
		"summary":    "Vérification du fichier de démonstration au SHA exact.",
	})
}

//go:embed qafixtures/video.webm qafixtures/trace.zip qafixtures/shot.png
var qaFixtures embed.FS

var recDirRe = regexp.MustCompile(`Dossier d'enregistrement \(seul endroit où écrire\) : (/\S+)`)

// recordQA simule une session QA navigateur (aos-qa-browser) : quand Agentic OS fournit un dossier
// d'enregistrement, y dépose une vraie vidéo, une vraie trace Playwright, une capture et report.json.
func recordQA(instructions string) error {
	m := recDirRe.FindStringSubmatch(instructions)
	if m == nil {
		return nil
	}
	dir := m[1]
	if err := os.MkdirAll(filepath.Join(dir, "shots"), 0o755); err != nil {
		return err
	}
	for src, dst := range map[string]string{"qafixtures/video.webm": "video.webm", "qafixtures/trace.zip": "trace.zip", "qafixtures/shot.png": "shots/01-recapitulatif.png"} {
		b, err := qaFixtures.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, dst), b, 0o644); err != nil {
			return err
		}
	}
	report := `{"ok":true,"error":null,"duration_ms":4200,"steps":[{"name":"connexion","ok":true,"ms":900},{"name":"récapitulatif","ok":true,"ms":1300}],` +
		`"shots":[{"file":"shots/01-recapitulatif.png","label":"Récapitulatif (faux QA navigateur)","criterion":1}],"console_errors":[],"failed_requests":[],"blocked_external":1}`
	if err := os.WriteFile(filepath.Join(dir, "report.json"), []byte(report), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "scenario.mjs"), []byte("export default async ({ login, shot }) => { await login(); await shot(\"Récapitulatif\", { criterion: 1 }); };\n"), 0o644)
}

// acceptanceFrom relit la liste JSON des critères (« Critères d'acceptation : [...] »).
func acceptanceFrom(in string) ([]string, error) {
	const marker = "Critères d'acceptation : "
	i := strings.LastIndex(in, marker)
	if i < 0 {
		return nil, errors.New("critères d'acceptation introuvables")
	}
	var out []string
	if err := json.NewDecoder(strings.NewReader(in[i+len(marker):])).Decode(&out); err != nil || len(out) == 0 {
		return nil, errors.New("critères d'acceptation illisibles")
	}
	return out, nil
}

// ------------------------------------------------------------------ GitHub

func (f *fakes) ghAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok, err := readSecret(f.cfg.secretsDir, "github_token")
		if err != nil || !bearerOK(r, tok) {
			ghError(w, 401, "Bad credentials")
			return
		}
		h(w, r)
	}
}

var repoPartRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

func (f *fakes) bareRepo(owner, repo string) (string, bool) {
	if !repoPartRe.MatchString(owner) || !repoPartRe.MatchString(repo) || strings.Contains(owner+repo, "..") {
		return "", false
	}
	p := filepath.Join(f.cfg.reposRoot, owner, repo+".git")
	if st, err := os.Stat(filepath.Join(p, "HEAD")); err != nil || st.IsDir() {
		return "", false
	}
	return p, true
}

func (f *fakes) repoOf(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	owner, repo := r.PathValue("owner"), r.PathValue("repo")
	p, ok := f.bareRepo(owner, repo)
	if !ok {
		ghError(w, 404, "Not Found")
		return "", "", false
	}
	return owner + "/" + repo, p, true
}

func (f *fakes) branchSHA(bare, branch string) (string, bool) {
	if branch == "" || strings.Contains(branch, "..") || strings.HasPrefix(branch, "-") {
		return "", false
	}
	sha, err := f.git(bare, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch+"^{commit}")
	if err != nil || len(sha) != 40 {
		return "", false
	}
	return sha, true
}

func (f *fakes) seedRepo(full string) error {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 || !repoPartRe.MatchString(parts[0]) || !repoPartRe.MatchString(parts[1]) {
		return errors.New("dépôt invalide")
	}
	bare := filepath.Join(f.cfg.reposRoot, parts[0], parts[1]+".git")
	if _, err := os.Stat(filepath.Join(bare, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(bare, 0o755); err != nil {
		return err
	}
	if _, err := f.git(bare, "init", "--bare", "--initial-branch=main"); err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "aos-e2e-seed-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if _, err := f.git(work, "init", "--initial-branch=main"); err != nil {
		return err
	}
	readme := "# " + full + "\n\nDépôt de démonstration des tests E2E d'Agentic OS.\n"
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte(readme), 0o644); err != nil {
		return err
	}
	if _, err := f.git(work, "add", "README.md"); err != nil {
		return err
	}
	if _, err := f.git(work, "commit", "-m", "init"); err != nil {
		return err
	}
	_, err = f.git(work, "push", bare, "main:refs/heads/main")
	return err
}

func (f *fakes) getRepo(w http.ResponseWriter, r *http.Request) {
	full, _, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"full_name": full, "private": true, "default_branch": "main", "permissions": map[string]bool{"push": true, "pull": true}})
}

var searchMarkerRe = regexp.MustCompile(`repo:(\S+) "agentic-os:ticket=([^"]+)"`)

func (f *fakes) searchIssues(w http.ResponseWriter, r *http.Request) {
	m := searchMarkerRe.FindStringSubmatch(r.URL.Query().Get("q"))
	items := []map[string]any{}
	if m != nil {
		marker := "<!-- agentic-os:ticket=" + m[2] + " -->"
		f.mu.Lock()
		for _, is := range f.issues[m[1]] {
			if strings.Contains(is.Body, marker) {
				items = append(items, issueJSON(is))
			}
		}
		f.mu.Unlock()
	}
	writeJSON(w, 200, map[string]any{"total_count": len(items), "incomplete_results": false, "items": items})
}

func issueJSON(is *issue) map[string]any {
	return map[string]any{"number": is.Number, "title": is.Title, "body": is.Body, "state": is.State, "html_url": is.HTMLURL}
}

func (f *fakes) createIssue(w http.ResponseWriter, r *http.Request) {
	full, _, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || strings.TrimSpace(req.Title) == "" {
		ghError(w, 422, "Validation Failed")
		return
	}
	f.mu.Lock()
	f.numbers[full]++
	is := &issue{Number: f.numbers[full], Title: req.Title, Body: req.Body, State: "open"}
	is.HTMLURL = fmt.Sprintf("%s/%s/issues/%d", f.cfg.webURL, full, is.Number)
	f.issues[full] = append(f.issues[full], is)
	f.mu.Unlock()
	writeJSON(w, 201, issueJSON(is))
}

func (f *fakes) getIssue(w http.ResponseWriter, r *http.Request) {
	full, _, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("number"))
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, is := range f.issues[full] {
		if is.Number == n {
			writeJSON(w, 200, issueJSON(is))
			return
		}
	}
	ghError(w, 404, "Not Found")
}

func (f *fakes) getBranch(w http.ResponseWriter, r *http.Request) {
	_, bare, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	branch := r.PathValue("branch")
	if u, err := url.PathUnescape(branch); err == nil {
		branch = u
	}
	sha, found := f.branchSHA(bare, branch)
	if !found {
		ghError(w, 404, "Branch not found")
		return
	}
	writeJSON(w, 200, map[string]any{"name": branch, "commit": map[string]any{"sha": sha}, "protected": false})
}

// pullJSON relit le SHA de la head dans le dépôt bare tant que la PR est ouverte (comme GitHub).
func (f *fakes) pullJSON(bare string, p *pull) map[string]any {
	sha := p.HeadSHA
	if p.State == "open" {
		if s, ok := f.branchSHA(bare, p.Head); ok {
			sha = s
		}
	}
	return map[string]any{
		"number": p.Number, "title": p.Title, "body": p.Body, "state": p.State, "draft": p.Draft, "merged": p.Merged,
		"html_url": p.HTMLURL,
		"head":     map[string]any{"ref": p.Head, "sha": sha},
		"base":     map[string]any{"ref": p.Base},
	}
}

func (f *fakes) listPulls(w http.ResponseWriter, r *http.Request) {
	full, bare, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	state, head, base := q.Get("state"), q.Get("head"), q.Get("base")
	if state == "" {
		state = "open"
	}
	owner := strings.SplitN(full, "/", 2)[0]
	out := []map[string]any{}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.pulls[full] {
		if state != "all" && p.State != state {
			continue
		}
		if head != "" && head != owner+":"+p.Head {
			continue
		}
		if base != "" && base != p.Base {
			continue
		}
		out = append(out, f.pullJSON(bare, p))
	}
	writeJSON(w, 200, out)
}

func (f *fakes) createPull(w http.ResponseWriter, r *http.Request) {
	full, bare, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
		Draft bool   `json:"draft"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Title == "" {
		ghError(w, 422, "Validation Failed")
		return
	}
	headSHA, okHead := f.branchSHA(bare, req.Head)
	if _, okBase := f.branchSHA(bare, req.Base); !okHead || !okBase {
		ghError(w, 422, "Validation Failed: head or base branch not found")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.pulls[full] {
		if p.State == "open" && p.Head == req.Head && p.Base == req.Base {
			ghError(w, 422, "A pull request already exists for "+req.Head)
			return
		}
	}
	f.numbers[full]++
	p := &pull{Number: f.numbers[full], Title: req.Title, Body: req.Body, State: "open", Draft: req.Draft,
		Head: req.Head, Base: req.Base, HeadSHA: headSHA}
	p.HTMLURL = fmt.Sprintf("%s/%s/pull/%d", f.cfg.webURL, full, p.Number)
	f.pulls[full] = append(f.pulls[full], p)
	writeJSON(w, 201, f.pullJSON(bare, p))
}

func (f *fakes) findPull(full string, n int) *pull {
	for _, p := range f.pulls[full] {
		if p.Number == n {
			return p
		}
	}
	return nil
}

func (f *fakes) getPull(w http.ResponseWriter, r *http.Request) {
	full, bare, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("number"))
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.findPull(full, n)
	if p == nil {
		ghError(w, 404, "Not Found")
		return
	}
	writeJSON(w, 200, f.pullJSON(bare, p))
}

// patchPull change la base d'une PR ouverte (seul champ pris en charge, comme utilisé par Agentic OS).
func (f *fakes) patchPull(w http.ResponseWriter, r *http.Request) {
	full, bare, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(r.PathValue("number"))
	var req struct {
		Base string `json:"base"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.findPull(full, n)
	if p == nil {
		ghError(w, 404, "Not Found")
		return
	}
	if p.State != "open" {
		ghError(w, 422, "Cannot change the base branch of a closed pull request")
		return
	}
	if _, ok := f.branchSHA(bare, req.Base); !ok || req.Base == "" {
		ghError(w, 422, "Proposed base branch does not exist")
		return
	}
	p.Base = req.Base
	writeJSON(w, 200, f.pullJSON(bare, p))
}

var shaRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (f *fakes) checkRuns(w http.ResponseWriter, r *http.Request) {
	full, _, ok := f.repoOf(w, r)
	if !ok {
		return
	}
	sha := r.PathValue("sha")
	if !shaRe.MatchString(sha) {
		ghError(w, 422, "invalid sha")
		return
	}
	runs := []map[string]any{}
	f.mu.Lock()
	for i, name := range f.cfg.checks {
		c, ok := f.checkOvr[sha+"\x00"+name]
		if !ok {
			c = checkRun{Status: "completed", Conclusion: "success"}
		}
		runs = append(runs, map[string]any{
			"id": 1000 + i, "name": name, "status": c.Status, "conclusion": nullable(c.Conclusion), "head_sha": sha,
			"html_url": fmt.Sprintf("%s/%s/actions/runs/%d", f.cfg.webURL, full, 1000+i),
		})
	}
	f.mu.Unlock()
	writeJSON(w, 200, map[string]any{"total_count": len(runs), "check_runs": runs})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (f *fakes) commitStatus(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := f.repoOf(w, r); !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"state": "success", "sha": r.PathValue("sha"), "statuses": []any{}})
}

// ------------------------------------------------------------------ Telegram

func (f *fakes) telegramOrNotFound(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if len(parts) != 2 || !strings.HasPrefix(parts[0], "bot") {
		writeJSON(w, 404, map[string]any{"message": "Not Found"})
		return
	}
	want, err := readSecret(f.cfg.secretsDir, "telegram_bot_token")
	if err != nil || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(parts[0], "bot")), []byte(want)) != 1 {
		writeJSON(w, 401, map[string]any{"ok": false, "error_code": 401, "description": "Unauthorized"})
		return
	}
	switch parts[1] {
	case "getMe":
		writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]any{"id": 1, "is_bot": true, "username": "aos_e2e_bot"}})
	case "sendMessage":
		var req struct {
			ChatID any    `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil || req.Text == "" {
			writeJSON(w, 400, map[string]any{"ok": false, "description": "Bad Request"})
			return
		}
		f.mu.Lock()
		f.messages = append(f.messages, req.Text)
		f.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true, "result": map[string]any{"message_id": time.Now().UnixNano()}})
	default:
		writeJSON(w, 404, map[string]any{"ok": false, "description": "Not Found"})
	}
}

// ------------------------------------------------------------------ contrôle des tests

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v); err != nil {
		writeJSON(w, 400, map[string]any{"error": "corps JSON invalide"})
		return false
	}
	return true
}

func (f *fakes) state(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pulls := map[string][]map[string]any{}
	for full, ps := range f.pulls {
		parts := strings.SplitN(full, "/", 2)
		bare, _ := f.bareRepo(parts[0], parts[1])
		for _, p := range ps {
			pulls[full] = append(pulls[full], f.pullJSON(bare, p))
		}
	}
	runs := []map[string]any{}
	ids := make([]string, 0, len(f.runs))
	for id := range f.runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rn := f.runs[id]
		runs = append(runs, map[string]any{"id": rn.ID, "profile": rn.Profile, "role": rn.Role, "status": rn.Status, "events": len(rn.Events)})
	}
	writeJSON(w, 200, map[string]any{"issues": f.issues, "pulls": pulls, "runs": runs, "telegram_messages": f.messages, "webhooks": f.deliveries})
}

func (f *fakes) hold(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role string `json:"role"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	f.mu.Lock()
	f.held[req.Role] = true
	f.mu.Unlock()
	writeJSON(w, 200, map[string]any{"held": req.Role})
}

// release libère le rôle et les runs retenus (un événement supplémentaire est émis pour le flux en direct).
func (f *fakes) release(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Role string `json:"role"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	f.mu.Lock()
	delete(f.held, req.Role)
	n := 0
	for _, rn := range f.runs {
		if rn.Role != req.Role {
			continue
		}
		select {
		case <-rn.release:
		default:
			f.emitLocked(rn, "tool.started", map[string]any{"tool": "write_file", "preview": "rédaction du livrable"})
			close(rn.release)
			n++
		}
	}
	f.mu.Unlock()
	writeJSON(w, 200, map[string]any{"released": n})
}

func (f *fakes) setCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SHA        string `json:"sha"`
		Name       string `json:"name"`
		Status     string `json:"status"`
		Conclusion string `json:"conclusion"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !shaRe.MatchString(req.SHA) || req.Name == "" {
		writeJSON(w, 400, map[string]any{"error": "sha ou name invalide"})
		return
	}
	f.mu.Lock()
	f.checkOvr[req.SHA+"\x00"+req.Name] = checkRun{Status: req.Status, Conclusion: req.Conclusion}
	f.mu.Unlock()
	writeJSON(w, 200, map[string]any{"ok": true})
}

type prTarget struct {
	Repo   string `json:"repo"`
	Number int    `json:"number"`
}

func (f *fakes) targetPull(w http.ResponseWriter, r *http.Request) (*pull, string, string, bool) {
	var req prTarget
	if !decodeBody(w, r, &req) {
		return nil, "", "", false
	}
	parts := strings.SplitN(req.Repo, "/", 2)
	if len(parts) != 2 {
		writeJSON(w, 400, map[string]any{"error": "repo invalide"})
		return nil, "", "", false
	}
	bare, ok := f.bareRepo(parts[0], parts[1])
	if !ok {
		writeJSON(w, 404, map[string]any{"error": "dépôt inconnu"})
		return nil, "", "", false
	}
	f.mu.Lock()
	p := f.findPull(req.Repo, req.Number)
	f.mu.Unlock()
	if p == nil {
		writeJSON(w, 404, map[string]any{"error": "PR inconnue"})
		return nil, "", "", false
	}
	return p, req.Repo, bare, true
}

// sendCheckRunWebhook envoie un webhook check_run « completed » pour la head de la PR.
func (f *fakes) sendCheckRunWebhook(w http.ResponseWriter, r *http.Request) {
	p, full, bare, ok := f.targetPull(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	pj := f.pullJSON(bare, p)
	f.mu.Unlock()
	head := pj["head"].(map[string]any)
	name := "tests"
	if len(f.cfg.checks) > 0 {
		name = f.cfg.checks[0]
	}
	payload := map[string]any{
		"action":     "completed",
		"repository": map[string]any{"full_name": full},
		"check_run": map[string]any{
			"name": name, "status": "completed", "conclusion": "success", "head_sha": head["sha"],
			"check_suite": map[string]any{"head_branch": p.Head},
		},
	}
	f.deliver(w, "check_run", payload)
}

// merge fusionne réellement la head dans la base du dépôt bare (fast-forward), marque la PR
// mergée puis envoie le webhook pull_request closed+merged signé.
func (f *fakes) merge(w http.ResponseWriter, r *http.Request) {
	p, full, bare, ok := f.targetPull(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	if p.State != "open" {
		f.mu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "PR non ouverte"})
		return
	}
	headSHA, okHead := f.branchSHA(bare, p.Head)
	baseSHA, okBase := f.branchSHA(bare, p.Base)
	if !okHead || !okBase {
		f.mu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "branches introuvables"})
		return
	}
	if _, err := f.git(bare, "merge-base", "--is-ancestor", baseSHA, headSHA); err != nil {
		f.mu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "merge non fast-forward"})
		return
	}
	if _, err := f.git(bare, "update-ref", "refs/heads/"+p.Base, headSHA, baseSHA); err != nil {
		f.mu.Unlock()
		writeJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	p.State, p.Merged, p.HeadSHA = "closed", true, headSHA
	pj := f.pullJSON(bare, p)
	f.mu.Unlock()
	payload := map[string]any{"action": "closed", "number": p.Number, "repository": map[string]any{"full_name": full}, "pull_request": pj}
	f.deliver(w, "pull_request", payload)
}

// deliver signe (X-Hub-Signature-256) et envoie le webhook, puis renvoie la réponse d'Agentic OS.
func (f *fakes) deliver(w http.ResponseWriter, event string, payload map[string]any) {
	if f.cfg.webhookURL == "" {
		writeJSON(w, 500, map[string]any{"error": "-webhook-url non configuré"})
		return
	}
	secret, err := readSecret(f.cfg.secretsDir, "github_webhook_secret")
	if err != nil {
		writeJSON(w, 500, map[string]any{"error": "secret de webhook illisible"})
		return
	}
	body, _ := json.Marshal(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	delivery := "e2e-" + randomHex(8)
	req, _ := http.NewRequest(http.MethodPost, f.cfg.webhookURL, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHub-Hookshot/e2e")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", delivery)
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": "webhook injoignable : " + err.Error()})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var parsed any
	if json.Unmarshal(respBody, &parsed) != nil {
		parsed = string(respBody)
	}
	rec := map[string]any{"event": event, "delivery": delivery, "status": resp.StatusCode, "response": parsed}
	f.mu.Lock()
	f.deliveries = append(f.deliveries, rec)
	f.mu.Unlock()
	writeJSON(w, 200, rec)
}
