// Package engine implémente la file de jobs (leases, heartbeat, recovery), les handlers par
// type de job, l'exécution des approbations et les actions utilisateur.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	gh "github.com/zeggriim2/agentic-os/internal/github"
	"github.com/zeggriim2/agentic-os/internal/gitops"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/notify"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Hermes est le sous-ensemble du client Hermes utilisé par le moteur.
type Hermes interface {
	CreateRun(ctx context.Context, profile, key string, req hermes.RunRequest) (hermes.CreateResult, error)
	GetRun(ctx context.Context, profile, runID string) (hermes.RunStatus, error)
	StreamEvents(ctx context.Context, profile, runID string, fn func(hermes.Event) error) error
	StopRun(ctx context.Context, profile, runID string) error
	Capabilities(ctx context.Context, profile string) error
	Models(ctx context.Context, profile string) ([]string, error)
	ModelOptions(ctx context.Context, profile string) (hermes.ModelOptions, error)
}

// GitHub est le sous-ensemble du client GitHub utilisé par le moteur.
type GitHub interface {
	EnsureIssue(ctx context.Context, repo, key, title, body string) (gh.Issue, bool, error)
	BranchSHA(ctx context.Context, repo, branch string) (string, error)
	EnsureDraftPR(ctx context.Context, repo, head, base, title, body string) (gh.PR, bool, error)
	GetPR(ctx context.Context, repo string, number int) (gh.PR, error)
	RetargetPR(ctx context.Context, repo string, number int, base string) (gh.PR, error)
	CIState(ctx context.Context, repo, sha string, required []string) (string, []gh.CheckEvidence, error)
}

// Git est le sous-ensemble de gitops utilisé par le moteur.
type Git interface {
	EnsureBranchWorktree(ctx context.Context, slug, repo, key, branch, base string) (string, error)
	EnsureDetachedWorktree(ctx context.Context, slug, repo, name, branch, sha string) (string, error)
	EnsureClean(ctx context.Context, path string) error
	HeadSHA(ctx context.Context, path string) (string, error)
	CurrentBranch(ctx context.Context, path string) (string, error)
	RevParse(ctx context.Context, dir, ref string) (string, error)
	DeletedTestFiles(ctx context.Context, path, baseRef string) ([]string, error)
	Push(ctx context.Context, path, branch string) error
	EnsureRemoteBranch(ctx context.Context, slug, repo, branch, from string) (string, bool, error)
	Relative(p string) string
}

// Notifier envoie des notifications (non bloquantes, échec loggé).
type Notifier interface {
	Notify(ctx context.Context, to notify.Destination, text string)
}

type nopNotifier struct{}

func (nopNotifier) Notify(context.Context, notify.Destination, string) {}

// notifyProject envoie text dans le canal du projet (son groupe/sujet Telegram), ou dans le chat
// par défaut préfixé du nom du projet. Un projet introuvable n'empêche pas l'envoi.
func (e *Engine) notifyProject(ctx context.Context, projectID int64, text string) {
	if text == "" {
		return
	}
	to := notify.Destination{}
	if p, err := store.GetProject(ctx, e.Store.Pool, projectID); err == nil {
		to = notify.Destination{ChatID: p.TelegramChatID, ThreadID: p.TelegramThreadID, Label: p.Name}
	}
	e.Notifier.Notify(ctx, to, text)
}

// notifySubject retrouve le projet d'un sujet (feature, ticket, approbation) puis notifie.
func (e *Engine) notifySubject(ctx context.Context, subjectType string, subjectID int64, text string) {
	e.notifyProject(ctx, e.projectOf(ctx, subjectType, subjectID, 0), text)
}

func (e *Engine) projectOf(ctx context.Context, subjectType string, id int64, depth int) int64 {
	switch subjectType {
	case "feature":
		if f, err := store.GetFeature(ctx, e.Store.Pool, id); err == nil {
			return f.ProjectID
		}
	case "ticket":
		if t, err := store.GetTicket(ctx, e.Store.Pool, id); err == nil {
			return t.ProjectID
		}
	case "approval":
		if ap, err := store.GetApproval(ctx, e.Store.Pool, id); err == nil && depth < 2 {
			return e.projectOf(ctx, ap.SubjectType, ap.SubjectID, depth+1)
		}
	}
	return 0
}

// Engine orchestre les jobs.
type Engine struct {
	Store        *store.Store
	Hermes       Hermes
	GitHub       GitHub
	Git          Git
	Notifier     Notifier
	Log          *slog.Logger
	Workers      int
	Lease        time.Duration
	MaxRework    int
	PollInterval time.Duration
	RunTimeout   time.Duration
	IdleInterval time.Duration
	Backoff      time.Duration
	// HeartbeatFn prolonge le lease (store.Heartbeat par défaut ; injectable pour les tests).
	HeartbeatFn func(ctx context.Context, id int64, owner string, lease time.Duration) (bool, error)
	ownerPrefix string

	mu     sync.Mutex
	active int
}

// Init complète les valeurs par défaut.
func (e *Engine) Init() {
	if e.Log == nil {
		e.Log = slog.Default()
	}
	if e.Notifier == nil {
		e.Notifier = nopNotifier{}
	}
	if e.Workers <= 0 {
		e.Workers = 2
	}
	if e.Lease <= 0 {
		e.Lease = 60 * time.Second
	}
	if e.PollInterval <= 0 {
		e.PollInterval = 3 * time.Second
	}
	if e.RunTimeout <= 0 {
		e.RunTimeout = 2 * time.Hour
	}
	if e.IdleInterval <= 0 {
		e.IdleInterval = time.Second
	}
	if e.Backoff <= 0 {
		e.Backoff = 30 * time.Second
	}
	if e.HeartbeatFn == nil {
		e.HeartbeatFn = func(ctx context.Context, id int64, owner string, lease time.Duration) (bool, error) {
			return store.Heartbeat(ctx, e.Store.Pool, id, owner, lease)
		}
	}
	if e.ownerPrefix == "" {
		host, _ := os.Hostname()
		e.ownerPrefix = fmt.Sprintf("%s-%d-%d", host, os.Getpid(), rand.Intn(1_000_000))
	}
}

// ActiveWorkers renvoie le nombre de jobs en cours de traitement dans ce processus.
func (e *Engine) ActiveWorkers() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.active
}

// ------------------------------------------------------------------ erreurs

// JobError est une erreur de job classée.
type JobError struct {
	Class     string
	Msg       string
	Retryable bool
}

func (e *JobError) Error() string { return e.Class + ": " + e.Msg }

func permanent(class, format string, a ...any) error {
	return &JobError{Class: class, Msg: fmt.Sprintf(format, a...)}
}

func retryable(class, format string, a ...any) error {
	return &JobError{Class: class, Msg: fmt.Sprintf(format, a...), Retryable: true}
}

func classify(err error) *JobError {
	var je *JobError
	if errors.As(err, &je) {
		return je
	}
	var he *hermes.Error
	if errors.As(err, &he) {
		return &JobError{Class: he.Class, Msg: he.Error(), Retryable: he.Class == domain.ErrClassProvider && he.Status != 404}
	}
	var ge *gh.Error
	if errors.As(err, &ge) {
		return &JobError{Class: ge.Class, Msg: ge.Error(), Retryable: ge.Class == domain.ErrClassExternal}
	}
	if errors.Is(err, gitops.ErrNonFastForward) {
		return &JobError{Class: domain.ErrClassPolicy, Msg: "push refusé (non fast-forward) : aucun force-push"}
	}
	var de *gitops.DirtyError
	if errors.As(err, &de) {
		return &JobError{Class: domain.ErrClassPolicy, Msg: de.Error()}
	}
	if errors.Is(err, gitops.ErrGit) {
		return &JobError{Class: domain.ErrClassExternal, Msg: err.Error(), Retryable: true}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &JobError{Class: domain.ErrClassTimeout, Msg: "délai dépassé", Retryable: true}
	}
	if errors.Is(err, domain.ErrInvalidDeliverable) {
		return &JobError{Class: domain.ErrClassInvalidOutput, Msg: err.Error()}
	}
	return &JobError{Class: domain.ErrClassInternal, Msg: err.Error()}
}

// ------------------------------------------------------------------ boucle

// Start lance workers, recovery et poller CI ; retourne quand ctx est annulé.
func (e *Engine) Start(ctx context.Context) {
	e.Init()
	var wg sync.WaitGroup
	for i := 0; i < e.Workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e.workerLoop(ctx, fmt.Sprintf("%s-w%d", e.ownerPrefix, i))
		}(i)
	}
	wg.Add(4)
	go func() { defer wg.Done(); e.every(ctx, 30*time.Second, e.Recover) }()
	go func() { defer wg.Done(); e.every(ctx, 30*time.Second, e.ReconcileStops) }()
	go func() { defer wg.Done(); e.every(ctx, 30*time.Second, e.RetryDeliveries) }()
	go func() { defer wg.Done(); e.every(ctx, 60*time.Second, e.PollPRs) }()
	wg.Wait()
}

func (e *Engine) every(ctx context.Context, d time.Duration, fn func(context.Context) error) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			e.Log.Warn("tâche périodique en échec", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) workerLoop(ctx context.Context, owner string) {
	for ctx.Err() == nil {
		did, err := e.RunOnce(ctx, owner)
		if err != nil && ctx.Err() == nil {
			e.Log.Error("worker", "error", err.Error())
		}
		if !did || err != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(e.IdleInterval):
			}
		}
	}
}

// RunOnce prend et traite un job ; false si aucun job disponible.
func (e *Engine) RunOnce(ctx context.Context, owner string) (bool, error) {
	e.Init()
	j, err := store.Dequeue(ctx, e.Store.Pool, owner, e.Lease)
	if err != nil || j == nil {
		return false, err
	}
	_ = store.Notify(ctx, e.Store.Pool, "job.updated", j.ID)
	e.process(ctx, j, owner)
	return true, nil
}

func (e *Engine) process(ctx context.Context, j *store.Job, owner string) {
	e.mu.Lock()
	e.active++
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.active--
		e.mu.Unlock()
	}()
	log := e.Log.With("job_id", j.ID, "kind", j.Kind, "subject_type", j.SubjectType, "subject_id", j.SubjectID)
	jctx, cancel := context.WithCancel(withLease(ctx, j.ID, owner))
	defer cancel()
	lost := make(chan struct{})
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		t := time.NewTicker(e.Lease / 3)
		defer t.Stop()
		lastOK := time.Now()
		for {
			select {
			case <-jctx.Done():
				return
			case <-t.C:
				ok, err := e.HeartbeatFn(jctx, j.ID, owner, e.Lease)
				switch {
				case err == nil && ok:
					lastOK = time.Now()
				case err == nil && !ok:
					close(lost)
					cancel()
					return
				default:
					if jctx.Err() != nil {
						return
					}
					// Sans heartbeat réussi depuis 2/3 du lease, un autre worker peut
					// bientôt reprendre le job : on s'arrête avant l'expiration.
					log.Warn("heartbeat en échec", "error", err.Error())
					if time.Since(lastOK) >= e.Lease*2/3 {
						log.Error("heartbeat impossible depuis trop longtemps : abandon du job avant expiration du lease")
						close(lost)
						cancel()
						return
					}
				}
			}
		}
	}()
	log.Info("job démarré", "attempt", j.Attempts)
	err := e.handle(jctx, j, owner)
	cancel()
	<-hbDone
	select {
	case <-lost:
		log.Warn("lease perdu : job abandonné par ce worker")
		return
	default:
	}
	if err == nil {
		log.Info("job terminé")
		return
	}
	if errors.Is(err, store.ErrLeaseLost) {
		log.Warn("lease perdu à la finalisation")
		return
	}
	if ctx.Err() != nil {
		log.Warn("arrêt du processus pendant le job : il sera repris après expiration du lease")
		return
	}
	je := classify(err)
	log.Warn("job en échec", "class", je.Class, "retryable", je.Retryable, "error", je.Msg)
	bg := context.Background()
	if je.Retryable && j.Attempts < j.MaxAttempts {
		backoff := e.Backoff * time.Duration(1<<(j.Attempts-1))
		if err := store.RequeueJob(bg, e.Store.Pool, j.ID, owner, je.Class, je.Msg, backoff); err != nil {
			log.Error("remise en file impossible", "error", err.Error())
		}
		_ = store.Notify(bg, e.Store.Pool, "job.updated", j.ID)
		return
	}
	if err := e.failJob(bg, j, owner, je); err != nil {
		log.Error("échec de la finalisation en erreur", "error", err.Error())
	}
}

// failJob marque le job failed et bloque le sujet, dans une transaction.
func (e *Engine) failJob(ctx context.Context, j *store.Job, owner string, je *JobError) error {
	var notify string
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		if err := store.FailJob(ctx, tx, j.ID, owner, je.Class, je.Msg); err != nil {
			return err
		}
		if err := store.Notify(ctx, tx, "job.updated", j.ID); err != nil {
			return err
		}
		msg, err := e.blockSubject(ctx, tx, j, fmt.Sprintf("job %s en échec (%s) : %s", j.Kind, je.Class, je.Msg))
		notify = msg
		return err
	})
	if err == nil && notify != "" {
		e.notifySubject(ctx, j.SubjectType, j.SubjectID, notify)
	}
	return err
}

// blockSubject applique l'événement fail au sujet du job s'il est autorisé.
func (e *Engine) blockSubject(ctx context.Context, tx pgx.Tx, j *store.Job, reason string) (string, error) {
	reason = truncate(reason, 1000)
	switch j.SubjectType {
	case "feature":
		f, err := store.LockFeature(ctx, tx, j.SubjectID)
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if _, err := domain.ApplyFeature(domain.FeatureState(f.State), domain.FeatureEvFail); err != nil {
			return "", nil
		}
		if _, err := e.featureEvent(ctx, tx, f, domain.FeatureEvFail, actorFor(j), reason); err != nil {
			return "", err
		}
		return fmt.Sprintf("⛔ Feature #%d « %s » bloquée : %s", f.ID, f.Title, reason), nil
	case "ticket":
		t, err := store.LockTicket(ctx, tx, j.SubjectID)
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if _, err := domain.ApplyTicket(domain.TicketState(t.State), domain.TicketEvFail); err != nil {
			return "", nil
		}
		if _, err := e.ticketEvent(ctx, tx, t, domain.TicketEvFail, actorFor(j), reason); err != nil {
			return "", err
		}
		return fmt.Sprintf("⛔ Ticket %s bloqué : %s", t.Key, reason), nil
	case "approval":
		ap, err := store.LockApproval(ctx, tx, j.SubjectID)
		if err != nil {
			return "", err
		}
		if ap.Status == domain.ApprovalExecuted || ap.Status == domain.ApprovalExecutionFailed {
			return "", nil
		}
		result := map[string]any{"error": truncate(reason, 500)}
		if err := store.MarkApprovalExecuted(ctx, tx, ap.ID, false, result); err != nil {
			return "", err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "approval.execution_failed", SubjectType: ap.SubjectType,
			SubjectID: ap.SubjectID, After: map[string]any{"approval_id": ap.ID, "error": result["error"]}}); err != nil {
			return "", err
		}
		return "", store.Notify(ctx, tx, "approval.updated", ap.ID)
	}
	return "", nil
}

func actorFor(j *store.Job) string {
	if r := domain.RoleForJob(j.Kind); r != "" {
		return "agent:" + r
	}
	return "system"
}

// finish exécute fn et marque le job réussi dans la même transaction.
func (e *Engine) finish(ctx context.Context, j *store.Job, owner string, fn func(tx pgx.Tx) error) error {
	return e.Store.InTx(ctx, func(tx pgx.Tx) error {
		if fn != nil {
			if err := fn(tx); err != nil {
				return err
			}
		}
		if err := store.CompleteJob(ctx, tx, j.ID, owner); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "job.updated", j.ID)
	})
}

// Recover remet en file les jobs au lease expiré. Chaque job épuisé est marqué failed
// et son sujet bloqué dans UNE transaction : un crash ou une erreur laisse le job leased
// (expiré) et la recovery suivante recommence, sans job failed à sujet non bloqué.
func (e *Engine) Recover(ctx context.Context) error {
	e.Init()
	n, err := store.RecoverExpired(ctx, e.Store.Pool)
	if err != nil {
		return err
	}
	if n > 0 {
		e.Log.Info("jobs remis en file après expiration du lease", "count", n)
		_ = store.Notify(ctx, e.Store.Pool, "overview.invalidate", 0)
	}
	for i := 0; i < 100; i++ {
		var msg string
		var j *store.Job
		err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			var err error
			if j, err = store.FailOneExhausted(ctx, tx); err != nil || j == nil {
				return err
			}
			if msg, err = e.blockSubject(ctx, tx, j, "lease expiré, tentatives épuisées"); err != nil {
				return err
			}
			return store.Notify(ctx, tx, "job.updated", j.ID)
		})
		if err != nil {
			return fmt.Errorf("blocage après épuisement (réessayé à la prochaine recovery) : %w", err)
		}
		if j == nil {
			return nil
		}
		if msg != "" {
			e.notifySubject(ctx, j.SubjectType, j.SubjectID, msg)
		}
	}
	return nil
}

type leaseKey struct{}

type leaseRef struct {
	jobID int64
	owner string
}

func withLease(ctx context.Context, jobID int64, owner string) context.Context {
	return context.WithValue(ctx, leaseKey{}, leaseRef{jobID, owner})
}

// fence vérifie (et prolonge) le lease juste avant un effet externe : push Git, issue,
// PR, soumission Hermes. Un worker qui a perdu son lease n'écrit plus rien dehors.
func (e *Engine) fence(ctx context.Context, effect string) error {
	ref, ok := ctx.Value(leaseKey{}).(leaseRef)
	if !ok {
		return permanent(domain.ErrClassInternal, "effet externe %s hors d'un job leasé", effect)
	}
	held, err := e.HeartbeatFn(ctx, ref.jobID, ref.owner, e.Lease)
	if err != nil {
		return retryable(domain.ErrClassInternal, "vérification du lease avant %s impossible : %v", effect, err)
	}
	if !held {
		e.Log.Warn("lease perdu : effet externe annulé", "job_id", ref.jobID, "effect", effect)
		return store.ErrLeaseLost
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
