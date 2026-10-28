package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/domain"
)

func one[T any](ctx context.Context, q Q, sql string, args ...any) (*T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToAddrOfStructByName[T])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func many[T any](ctx context.Context, q Q, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if out == nil {
		out = []T{}
	}
	return out, err
}

// JSON sérialise v pour une colonne jsonb.
func JSON(v any) []byte {
	if v == nil {
		return nil
	}
	if raw, ok := v.(json.RawMessage); ok {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}

// ================================================================== projects

// ProjectInput regroupe les champs modifiables d'un projet.
type ProjectInput struct {
	Slug, Name, Repository, BaseBranch, Mode, TestCommand string
	RequiredChecks                                        []string
	Enabled                                               bool
	AgentProfiles                                         map[string]string
	AgentModels                                           map[string]domain.ModelChoice
	TelegramChatID                                        string
	TelegramThreadID                                      int64
}

// CreateProject insère un projet.
func CreateProject(ctx context.Context, q Q, in ProjectInput) (*Project, error) {
	if in.RequiredChecks == nil {
		in.RequiredChecks = []string{}
	}
	if in.AgentProfiles == nil {
		in.AgentProfiles = map[string]string{}
	}
	if in.AgentModels == nil {
		in.AgentModels = map[string]domain.ModelChoice{}
	}
	p, err := one[Project](ctx, q, `INSERT INTO projects (slug, name, repository, base_branch, required_checks, mode, enabled, test_command, agent_profiles,
		telegram_chat_id, telegram_thread_id, agent_models) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING `+projectCols,
		in.Slug, in.Name, in.Repository, in.BaseBranch, in.RequiredChecks, in.Mode, in.Enabled, in.TestCommand, in.AgentProfiles,
		in.TelegramChatID, in.TelegramThreadID, in.AgentModels)
	if IsUniqueViolation(err) {
		return nil, ErrDuplicate
	}
	return p, err
}

// GetProject lit un projet.
func GetProject(ctx context.Context, q Q, id int64) (*Project, error) {
	return one[Project](ctx, q, `SELECT `+projectCols+` FROM projects WHERE id=$1`, id)
}

// LockProject lit un projet avec un verrou d'écriture. La création de feature
// et les changements structurels utilisent ce même verrou pour éviter une race.
func LockProject(ctx context.Context, q Q, id int64) (*Project, error) {
	return one[Project](ctx, q, `SELECT `+projectCols+` FROM projects WHERE id=$1 FOR UPDATE`, id)
}

// ListProjects liste les projets (ordre id décroissant).
func ListProjects(ctx context.Context, q Q, p Page) ([]Project, error) {
	return many[Project](ctx, q, `SELECT `+projectCols+` FROM projects WHERE id < $1 ORDER BY id DESC LIMIT $2`, cursorOrMax(p.Cursor), p.Limit+1)
}

// UpdateProject réécrit les champs modifiables.
func UpdateProject(ctx context.Context, q Q, id int64, in ProjectInput) (*Project, error) {
	if in.RequiredChecks == nil {
		in.RequiredChecks = []string{}
	}
	if in.AgentProfiles == nil {
		in.AgentProfiles = map[string]string{}
	}
	if in.AgentModels == nil {
		in.AgentModels = map[string]domain.ModelChoice{}
	}
	return one[Project](ctx, q, `UPDATE projects SET name=$2, repository=$3, base_branch=$4, required_checks=$5, mode=$6,
		enabled=$7, test_command=$8, agent_profiles=$9, telegram_chat_id=$10, telegram_thread_id=$11, agent_models=$12, updated_at=now()
		WHERE id=$1 RETURNING `+projectCols,
		id, in.Name, in.Repository, in.BaseBranch, in.RequiredChecks, in.Mode, in.Enabled, in.TestCommand, in.AgentProfiles,
		in.TelegramChatID, in.TelegramThreadID, in.AgentModels)
}

// ProjectAgentProfiles liste les profils Hermes propres aux projets actifs (sans doublon, triés).
func ProjectAgentProfiles(ctx context.Context, q Q) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT DISTINCT e.value FROM projects p, jsonb_each_text(p.agent_profiles) e
		WHERE p.enabled AND e.value <> '' ORDER BY 1`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ================================================================== agents

// ListAgents liste les agents.
func ListAgents(ctx context.Context, q Q) ([]Agent, error) {
	return many[Agent](ctx, q, `SELECT `+agentCols+` FROM agents ORDER BY id`)
}

// GetAgent lit un agent par rôle.
func GetAgent(ctx context.Context, q Q, role string) (*Agent, error) {
	return one[Agent](ctx, q, `SELECT `+agentCols+` FROM agents WHERE role=$1`, role)
}

// UpdateAgent met à jour la configuration désirée d'un agent.
func UpdateAgent(ctx context.Context, q Q, a *Agent) (*Agent, error) {
	return one[Agent](ctx, q, `UPDATE agents SET hermes_profile=$2, desired_model=$3, max_tokens_per_run=$4, enabled=$5,
		sync_state = CASE
			WHEN sync_state='UNREACHABLE' THEN 'UNREACHABLE'
			WHEN observed_model IS NULL THEN 'UNKNOWN'
			WHEN $3='' OR observed_model=$3 THEN 'SYNCED'
			ELSE 'DRIFT_DETECTED' END,
		updated_at=now() WHERE role=$1 RETURNING `+agentCols,
		a.Role, a.HermesProfile, a.DesiredModel, a.MaxTokensPerRun, a.Enabled)
}

// SetAgentObservation enregistre l'état observé.
func SetAgentObservation(ctx context.Context, q Q, role, state string, model, provider *string) error {
	_, err := q.Exec(ctx, `UPDATE agents SET sync_state=$2, observed_model=COALESCE($3, observed_model),
		observed_provider=COALESCE($4, observed_provider), observed_at=now(), updated_at=now() WHERE role=$1`, role, state, model, provider)
	return err
}

// LastObservedRuntime renvoie le modèle/fournisseur du dernier run terminé d'un rôle.
func LastObservedRuntime(ctx context.Context, q Q, role string) (model, provider *string, err error) {
	err = q.QueryRow(ctx, `SELECT model, provider FROM runs WHERE agent_role=$1 AND model IS NOT NULL ORDER BY id DESC LIMIT 1`, role).Scan(&model, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	return model, provider, err
}

// ================================================================== features

// CreateFeature insère une feature en IDEA.
func CreateFeature(ctx context.Context, q Q, projectID int64, title, description string) (*Feature, error) {
	return one[Feature](ctx, q, `INSERT INTO features (project_id, title, description) VALUES ($1,$2,$3) RETURNING `+featureCols,
		projectID, title, description)
}

// GetFeature lit une feature.
func GetFeature(ctx context.Context, q Q, id int64) (*Feature, error) {
	return one[Feature](ctx, q, `SELECT `+featureCols+` FROM features WHERE id=$1`, id)
}

// LockFeature lit une feature avec verrou de ligne (dans une transaction).
func LockFeature(ctx context.Context, q Q, id int64) (*Feature, error) {
	return one[Feature](ctx, q, `SELECT `+featureCols+` FROM features WHERE id=$1 FOR UPDATE`, id)
}

// FeatureFilter filtre la liste des features.
type FeatureFilter struct {
	State     string
	ProjectID int64
}

// ListFeatures liste les features.
func ListFeatures(ctx context.Context, q Q, f FeatureFilter, p Page) ([]Feature, error) {
	return many[Feature](ctx, q, `SELECT `+featureCols+` FROM features
		WHERE id < $1 AND ($2 = '' OR state = $2) AND ($3 = 0 OR project_id = $3)
		ORDER BY id DESC LIMIT $4`, cursorOrMax(p.Cursor), f.State, f.ProjectID, p.Limit+1)
}

// ProjectHasActiveFeatures indique si une configuration de projet est encore
// utilisée par un workflow non terminal.
func ProjectHasActiveFeatures(ctx context.Context, q Q, projectID int64) (bool, error) {
	var active bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM features WHERE project_id=$1 AND state NOT IN ('DONE','CANCELLED'))`, projectID).Scan(&active)
	return active, err
}

// SetFeatureState applique un nouvel état avec verrou optimiste (version attendue).
func SetFeatureState(ctx context.Context, q Q, id int64, version int, state, previous, reason string) (*Feature, error) {
	var prev *string
	if previous != "" {
		prev = &previous
	}
	f, err := one[Feature](ctx, q, `UPDATE features SET state=$3, previous_state=COALESCE($4, previous_state), state_reason=$5,
		version=version+1, updated_at=now() WHERE id=$1 AND version=$2 RETURNING `+featureCols, id, version, state, prev, reason)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrVersionConflict
	}
	return f, err
}

// SetFeatureSpec enregistre la spec.
func SetFeatureSpec(ctx context.Context, q Q, id int64, spec any) error {
	_, err := q.Exec(ctx, `UPDATE features SET spec=$2, updated_at=now() WHERE id=$1`, id, JSON(spec))
	return err
}

// SetFeatureDesign enregistre le design.
func SetFeatureDesign(ctx context.Context, q Q, id int64, design any) error {
	_, err := q.Exec(ctx, `UPDATE features SET design=$2, updated_at=now() WHERE id=$1`, id, JSON(design))
	return err
}

// ================================================================== tickets

// TicketInput décrit un ticket à créer.
type TicketInput struct {
	FeatureID, ProjectID int64
	Key                  string
	Seq                  int
	Title, Body          string
	Acceptance           []string
}

// InsertTicket crée le ticket si la clé n'existe pas, et renvoie la ligne.
func InsertTicket(ctx context.Context, q Q, in TicketInput) (*Ticket, error) {
	_, err := q.Exec(ctx, `INSERT INTO tickets (feature_id, project_id, key, seq, title, body, acceptance)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (key) DO NOTHING`,
		in.FeatureID, in.ProjectID, in.Key, in.Seq, in.Title, in.Body, JSON(in.Acceptance))
	if err != nil {
		return nil, err
	}
	return one[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE key=$1`, in.Key)
}

// GetTicket lit un ticket.
func GetTicket(ctx context.Context, q Q, id int64) (*Ticket, error) {
	return one[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE id=$1`, id)
}

// LockTicket lit un ticket avec verrou de ligne.
func LockTicket(ctx context.Context, q Q, id int64) (*Ticket, error) {
	return one[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE id=$1 FOR UPDATE`, id)
}

// ListTicketsByFeature liste les tickets d'une feature (ordre seq).
func ListTicketsByFeature(ctx context.Context, q Q, featureID int64) ([]Ticket, error) {
	return many[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE feature_id=$1 ORDER BY seq`, featureID)
}

// ListTicketsByProject liste les tickets d'un projet qui ont une branche (graphe Git).
func ListTicketsByProject(ctx context.Context, q Q, projectID int64) ([]Ticket, error) {
	return many[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE project_id=$1 AND branch IS NOT NULL ORDER BY id`, projectID)
}

// ProjectDevBusy indique si un ticket du projet est en cours de développement : un ticket
// dans un état actif (ni PLANNED, READY_FOR_DEV, DONE ni CANCELLED — BLOCKED compris), ou un
// job develop en file / pris. Le développement est séquentiel par projet.
func ProjectDevBusy(ctx context.Context, q Q, projectID int64) (bool, error) {
	var busy bool
	err := q.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM tickets WHERE project_id=$1
			AND state NOT IN ('PLANNED','READY_FOR_DEV','DONE','CANCELLED'))
		OR EXISTS(SELECT 1 FROM jobs WHERE project_id=$1 AND kind='develop' AND status IN ('queued','leased'))`,
		projectID).Scan(&busy)
	return busy, err
}

// NextReadyTicket verrouille le prochain ticket READY_FOR_DEV du projet (feature la plus
// ancienne, puis ordre du plan). nil si aucun.
func NextReadyTicket(ctx context.Context, q Q, projectID int64) (*Ticket, error) {
	t, err := one[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE project_id=$1 AND state='READY_FOR_DEV'
		ORDER BY feature_id, seq LIMIT 1 FOR UPDATE`, projectID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return t, err
}

// ListTicketsByStates liste les tickets dans des états donnés.
func ListTicketsByStates(ctx context.Context, q Q, states []string) ([]Ticket, error) {
	return many[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE state = ANY($1) ORDER BY id`, states)
}

// FindTicketsForGitHub retrouve les tickets suivis par PR, branche ou SHA.
func FindTicketsForGitHub(ctx context.Context, q Q, repository string, prNumber int, sha, branch string) ([]Ticket, error) {
	return many[Ticket](ctx, q, `SELECT `+prefixed("t", ticketCols)+` FROM tickets t JOIN projects p ON p.id=t.project_id
		WHERE p.repository=$1 AND t.state IN ('PR_OPENED','HUMAN_MERGE')
		AND (($2 > 0 AND t.pr_number=$2) OR ($3 <> '' AND t.head_sha=$3) OR ($4 <> '' AND t.branch=$4))`,
		repository, prNumber, sha, branch)
}

func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// SetTicketState applique un nouvel état avec verrou optimiste.
func SetTicketState(ctx context.Context, q Q, id int64, version int, state, previous, reason string) (*Ticket, error) {
	var prev *string
	if previous != "" {
		prev = &previous
	}
	t, err := one[Ticket](ctx, q, `UPDATE tickets SET state=$3, previous_state=COALESCE($4, previous_state), state_reason=$5,
		version=version+1, updated_at=now() WHERE id=$1 AND version=$2 RETURNING `+ticketCols, id, version, state, prev, reason)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrVersionConflict
	}
	return t, err
}

// SetTicketAttempt fixe le compteur de rework.
func SetTicketAttempt(ctx context.Context, q Q, id int64, attempt int) error {
	_, err := q.Exec(ctx, `UPDATE tickets SET attempt=$2, updated_at=now() WHERE id=$1`, id, attempt)
	return err
}

// SetTicketIssue enregistre l'issue GitHub relue.
func SetTicketIssue(ctx context.Context, q Q, id int64, number int, url string) error {
	_, err := q.Exec(ctx, `UPDATE tickets SET issue_number=$2, issue_url=$3, updated_at=now() WHERE id=$1`, id, number, url)
	return err
}

// SetTicketHead enregistre branche et SHA poussé.
func SetTicketHead(ctx context.Context, q Q, id int64, branch, sha string) error {
	_, err := q.Exec(ctx, `UPDATE tickets SET branch=$2, head_sha=$3, ci_state='unverified', updated_at=now() WHERE id=$1`, id, branch, sha)
	return err
}

// SetTicketPR enregistre la PR relue.
func SetTicketPR(ctx context.Context, q Q, id int64, number int, url string) error {
	_, err := q.Exec(ctx, `UPDATE tickets SET pr_number=$2, pr_url=$3, updated_at=now() WHERE id=$1`, id, number, url)
	return err
}

// SetTicketCI enregistre l'état CI.
func SetTicketCI(ctx context.Context, q Q, id int64, state string) error {
	_, err := q.Exec(ctx, `UPDATE tickets SET ci_state=$2, updated_at=now() WHERE id=$1`, id, state)
	return err
}

// ================================================================== jobs

// NewJob décrit un job à créer.
type NewJob struct {
	Kind, SubjectType string
	SubjectID         int64
	ProjectID         *int64
	IdempotencyKey    string
	Payload           any
	MaxAttempts       int
	RunAfter          time.Time
}

// EnqueueJob crée un job ; si la clé d'idempotence existe déjà, renvoie le job existant (created=false).
func EnqueueJob(ctx context.Context, q Q, j NewJob) (*Job, bool, error) {
	if j.MaxAttempts <= 0 {
		j.MaxAttempts = 3
	}
	payload := JSON(j.Payload)
	if payload == nil {
		payload = []byte("{}")
	}
	runAfter := j.RunAfter
	if runAfter.IsZero() {
		runAfter = time.Now()
	}
	job, err := one[Job](ctx, q, `INSERT INTO jobs (kind, subject_type, subject_id, project_id, idempotency_key, payload, max_attempts, run_after)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (idempotency_key) DO NOTHING RETURNING `+jobCols,
		j.Kind, j.SubjectType, j.SubjectID, j.ProjectID, j.IdempotencyKey, payload, j.MaxAttempts, runAfter)
	if errors.Is(err, ErrNotFound) {
		existing, err := one[Job](ctx, q, `SELECT `+jobCols+` FROM jobs WHERE idempotency_key=$1`, j.IdempotencyKey)
		return existing, false, err
	}
	if err != nil {
		return nil, false, err
	}
	return job, true, nil
}

// GetJob lit un job.
func GetJob(ctx context.Context, q Q, id int64) (*Job, error) {
	return one[Job](ctx, q, `SELECT `+jobCols+` FROM jobs WHERE id=$1`, id)
}

const dequeueSQL = `UPDATE jobs SET status='leased', lease_owner=$1, lease_expires_at=now() + make_interval(secs => $2),
	attempts=attempts+1, updated_at=now()
	WHERE id = (
		SELECT id FROM jobs
		WHERE status='queued' AND run_after <= now()
		  AND NOT EXISTS (SELECT 1 FROM settings WHERE key='dispatch_paused' AND value='true'::jsonb)
		  AND NOT (kind IN ('develop','review','qa') AND project_id IS NOT NULL AND EXISTS (
			SELECT 1 FROM jobs a WHERE a.project_id = jobs.project_id AND a.status='leased' AND a.kind IN ('develop','review','qa')))
		ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
	RETURNING ` + jobCols

// Dequeue prend le prochain job disponible (SKIP LOCKED). nil si aucun.
func Dequeue(ctx context.Context, q Q, owner string, lease time.Duration) (*Job, error) {
	j, err := one[Job](ctx, q, dequeueSQL, owner, lease.Seconds())
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if IsUniqueViolation(err) {
		// un autre worker a pris un job develop|review|qa du même projet entre-temps
		return nil, nil
	}
	return j, err
}

// Heartbeat prolonge le lease ; false si le lease est perdu (job annulé/repris).
func Heartbeat(ctx context.Context, q Q, id int64, owner string, lease time.Duration) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE jobs SET lease_expires_at=now() + make_interval(secs => $3), updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='leased'`, id, owner, lease.Seconds())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// CompleteJob marque le job réussi si le lease est toujours détenu.
func CompleteJob(ctx context.Context, q Q, id int64, owner string) error {
	tag, err := q.Exec(ctx, `UPDATE jobs SET status='succeeded', lease_owner=NULL, lease_expires_at=NULL, last_error=NULL,
		error_class=NULL, updated_at=now() WHERE id=$1 AND lease_owner=$2 AND status='leased'`, id, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// ErrLeaseLost indique que le worker ne détient plus le lease.
var ErrLeaseLost = errors.New("lease perdu")

// FailJob marque le job en échec définitif.
func FailJob(ctx context.Context, q Q, id int64, owner, class, msg string) error {
	tag, err := q.Exec(ctx, `UPDATE jobs SET status='failed', lease_owner=NULL, lease_expires_at=NULL, last_error=$3,
		error_class=$4, updated_at=now() WHERE id=$1 AND ($2 = '' OR lease_owner=$2) AND status IN ('leased','queued')`, id, owner, truncate(msg, 2000), class)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// RequeueJob remet le job en file après un délai.
func RequeueJob(ctx context.Context, q Q, id int64, owner, class, msg string, after time.Duration) error {
	tag, err := q.Exec(ctx, `UPDATE jobs SET status='queued', lease_owner=NULL, lease_expires_at=NULL, last_error=$3,
		error_class=$4, run_after=now() + make_interval(secs => $5), updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='leased'`, id, owner, truncate(msg, 2000), class, after.Seconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// RecoverExpired remet en file les jobs au lease expiré qui ont encore des tentatives.
// Les jobs épuisés sont traités un par un par FailOneExhausted (avec blocage du sujet
// dans la même transaction).
func RecoverExpired(ctx context.Context, q Q) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE jobs SET status='queued', lease_owner=NULL, lease_expires_at=NULL,
		last_error='lease expiré', error_class='timeout', updated_at=now()
		WHERE status='leased' AND lease_expires_at < now() AND attempts < max_attempts`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// FailOneExhausted marque failed UN job au lease expiré sans tentative restante et le
// renvoie (nil si aucun). À appeler dans la transaction qui bloque aussi son sujet :
// en cas d'échec, le rollback laisse le job leased et la recovery suivante recommence.
func FailOneExhausted(ctx context.Context, q Q) (*Job, error) {
	j, err := one[Job](ctx, q, `UPDATE jobs SET status='failed', lease_owner=NULL, lease_expires_at=NULL,
		last_error='lease expiré, tentatives épuisées', error_class='timeout', updated_at=now()
		WHERE id = (SELECT id FROM jobs WHERE status='leased' AND lease_expires_at < now() AND attempts >= max_attempts
			ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING `+jobCols)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return j, err
}

// CancelJobs annule les jobs actifs des sujets donnés et renvoie leurs ids.
func CancelJobs(ctx context.Context, q Q, subjectType string, ids []int64) ([]int64, error) {
	rows, err := q.Query(ctx, `UPDATE jobs SET status='cancelled', lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE subject_type=$1 AND subject_id = ANY($2) AND status IN ('queued','leased') RETURNING id`, subjectType, ids)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// CancelJob annule un job précis.
func CancelJob(ctx context.Context, q Q, id int64) error {
	_, err := q.Exec(ctx, `UPDATE jobs SET status='cancelled', lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND status IN ('queued','leased')`, id)
	return err
}

// ListActiveJobs liste les jobs en file ou en cours.
func ListActiveJobs(ctx context.Context, q Q) ([]Job, error) {
	return many[Job](ctx, q, `SELECT `+jobCols+` FROM jobs WHERE status IN ('queued','leased') ORDER BY id`)
}

// ListJobsForSubject liste les jobs d'un sujet.
func ListJobsForSubject(ctx context.Context, q Q, subjectType string, id int64) ([]Job, error) {
	return many[Job](ctx, q, `SELECT `+jobCols+` FROM jobs WHERE subject_type=$1 AND subject_id=$2 ORDER BY id`, subjectType, id)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// ================================================================== runs

// CreateRun crée un run soumis.
func CreateRun(ctx context.Context, q Q, jobID int64, role, profile string) (*Run, error) {
	return one[Run](ctx, q, `INSERT INTO runs (job_id, agent_role, hermes_profile) VALUES ($1,$2,$3) RETURNING `+runCols, jobID, role, profile)
}

// SetRunRequestedModel enregistre la surcharge de modèle envoyée à Hermes (vide = aucune).
func SetRunRequestedModel(ctx context.Context, q Q, id int64, m domain.ModelChoice) error {
	_, err := q.Exec(ctx, `UPDATE runs SET requested_model=NULLIF($2,''), requested_provider=NULLIF($3,''), updated_at=now() WHERE id=$1`,
		id, m.Model, m.Provider)
	return err
}

// MarkRunModelFallback rattache le run à sa re-soumission sans surcharge (repli sur le défaut du profil).
// La surcharge demandée est conservée pour l'affichage « demandé ≠ servi ».
func MarkRunModelFallback(ctx context.Context, q Q, id int64, hermesID string) error {
	_, err := q.Exec(ctx, `UPDATE runs SET model_fallback=true, hermes_run_id=$2, status='running', error=NULL, updated_at=now() WHERE id=$1`, id, hermesID)
	return err
}

// GetRun lit un run.
func GetRun(ctx context.Context, q Q, id int64) (*Run, error) {
	return one[Run](ctx, q, `SELECT `+runCols+` FROM runs WHERE id=$1`, id)
}

// LatestRunForJob renvoie le dernier run d'un job (nil si aucun).
func LatestRunForJob(ctx context.Context, q Q, jobID int64) (*Run, error) {
	r, err := one[Run](ctx, q, `SELECT `+runCols+` FROM runs WHERE job_id=$1 ORDER BY id DESC LIMIT 1`, jobID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// SetRunHermesID rattache l'identifiant Hermes.
func SetRunHermesID(ctx context.Context, q Q, id int64, hermesID string) error {
	// Un arrêt demandé entre-temps est conservé : le run sera arrêté par la réconciliation.
	_, err := q.Exec(ctx, `UPDATE runs SET hermes_run_id=$2,
		status = CASE status WHEN 'submitted' THEN 'running' WHEN 'stopped' THEN 'stopping' ELSE status END,
		updated_at=now() WHERE id=$1`, id, hermesID)
	return err
}

// RunResult regroupe les champs finaux d'un run.
type RunResult struct {
	Status                                 string
	InputTokens, OutputTokens, TotalTokens int64
	CacheReadTokens, CacheWriteTokens      int64
	Model, Provider                        string
	Output                                 string
	Artifact                               any
	Error                                  string
}

// MaxOutput borne la sortie stockée (256 KiB).
const MaxOutput = 256 << 10

// FinishRun enregistre l'état final d'un run.
func FinishRun(ctx context.Context, q Q, id int64, r RunResult) error {
	out := r.Output
	if len(out) > MaxOutput {
		out = out[len(out)-MaxOutput:]
		for len(out) > 0 && (out[0]&0xC0) == 0x80 { // ne pas couper un caractère UTF-8
			out = out[1:]
		}
	}
	var art []byte
	if r.Artifact != nil {
		art = JSON(r.Artifact)
	}
	// Le cache relu est compris dans l'entrée : borné à [0, input] pour garder une entrée nouvelle ≥ 0.
	cacheRead := min(max(r.CacheReadTokens, 0), max(r.InputTokens, 0))
	_, err := q.Exec(ctx, `UPDATE runs SET status=$2, input_tokens=$3, output_tokens=$4, total_tokens=$5, model=NULLIF($6,''),
		provider=NULLIF($7,''), output=$8, artifact=$9, error=NULLIF($10,''), cache_read_tokens=$11, cache_write_tokens=$12,
		finished_at=now(), updated_at=now() WHERE id=$1`,
		id, r.Status, r.InputTokens, r.OutputTokens, r.TotalTokens, r.Model, r.Provider, out, art, truncate(r.Error, 2000),
		cacheRead, max(r.CacheWriteTokens, 0))
	return err
}

// RequestRunStop passe un run actif en « stopping » (idempotent). false si le run est déjà terminé.
func RequestRunStop(ctx context.Context, q Q, id int64, reason string) (bool, error) {
	tag, err := q.Exec(ctx, `UPDATE runs SET status='stopping', stop_requested_at=COALESCE(stop_requested_at, now()),
		error=COALESCE(NULLIF($2,''), error), updated_at=now()
		WHERE id=$1 AND status IN ('submitted','running','stopping')`, id, truncate(reason, 500))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// RecordStopAttempt compte une tentative d'arrêt et son erreur éventuelle.
func RecordStopAttempt(ctx context.Context, q Q, id int64, errMsg string) error {
	_, err := q.Exec(ctx, `UPDATE runs SET stop_attempts=stop_attempts+1, stop_error=NULLIF($2,''), updated_at=now() WHERE id=$1`,
		id, truncate(errMsg, 500))
	return err
}

// ListStoppingRuns liste les runs dont l'arrêt n'est pas encore confirmé.
func ListStoppingRuns(ctx context.Context, q Q) ([]Run, error) {
	return many[Run](ctx, q, `SELECT `+runCols+` FROM runs WHERE status='stopping' ORDER BY id LIMIT 200`)
}

// SetRunStatus change uniquement le statut (stop/lost).
func SetRunStatus(ctx context.Context, q Q, id int64, status, errMsg string) error {
	_, err := q.Exec(ctx, `UPDATE runs SET status=$2, error=COALESCE(NULLIF($3,''), error),
		finished_at=CASE WHEN $2 IN ('completed','failed','stopped','lost') THEN COALESCE(finished_at, now()) ELSE finished_at END,
		updated_at=now() WHERE id=$1`, id, status, errMsg)
	return err
}

// RunFilter filtre la liste des runs.
type RunFilter struct {
	Status, Role string
	ProjectID    int64
}

// ListRuns liste les runs.
func ListRuns(ctx context.Context, q Q, f RunFilter, p Page) ([]Run, error) {
	return many[Run](ctx, q, `SELECT `+prefixed("r", runCols)+` FROM runs r JOIN jobs j ON j.id=r.job_id
		WHERE r.id < $1 AND ($2='' OR r.status=$2) AND ($3='' OR r.agent_role=$3) AND ($4=0 OR j.project_id=$4)
		ORDER BY r.id DESC LIMIT $5`, cursorOrMax(p.Cursor), f.Status, f.Role, f.ProjectID, p.Limit+1)
}

// ListRunsForSubject liste les runs des jobs d'un sujet.
func ListRunsForSubject(ctx context.Context, q Q, subjectType string, id int64) ([]Run, error) {
	return many[Run](ctx, q, `SELECT `+prefixed("r", runCols)+` FROM runs r JOIN jobs j ON j.id=r.job_id
		WHERE j.subject_type=$1 AND j.subject_id=$2 ORDER BY r.id`, subjectType, id)
}

// ListActiveRunsForSubjects liste les runs non terminés pour des sujets.
func ListActiveRunsForSubjects(ctx context.Context, q Q, subjectType string, ids []int64) ([]Run, error) {
	return many[Run](ctx, q, `SELECT `+prefixed("r", runCols)+` FROM runs r JOIN jobs j ON j.id=r.job_id
		WHERE j.subject_type=$1 AND j.subject_id = ANY($2) AND r.status IN ('submitted','running','stopping')`, subjectType, ids)
}

// InsertRunEvent persiste un événement (ignoré si déjà présent).
func InsertRunEvent(ctx context.Context, q Q, runID int64, seq int, event string, data []byte) (bool, error) {
	if len(data) > 60000 {
		data = JSON(map[string]any{"truncated": true})
	}
	tag, err := q.Exec(ctx, `INSERT INTO run_events (run_id, seq, event, data) VALUES ($1,$2,$3,$4) ON CONFLICT (run_id, seq) DO NOTHING`,
		runID, seq, truncate(event, 100), data)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// GetRunEvent lit un événement persisté par (run, seq).
func GetRunEvent(ctx context.Context, q Q, runID int64, seq int) (*RunEvent, error) {
	return one[RunEvent](ctx, q, `SELECT id, run_id, seq, event, data, created_at FROM run_events WHERE run_id=$1 AND seq=$2`, runID, seq)
}

// MaxRunEvents borne les événements renvoyés par le détail d'un run.
const MaxRunEvents = 2000

// ListRunEvents renvoie les MaxRunEvents derniers événements d'un run (ordre seq croissant)
// et indique si des événements plus anciens ont été omis.
func ListRunEvents(ctx context.Context, q Q, runID int64) ([]RunEvent, bool, error) {
	evs, err := many[RunEvent](ctx, q, `SELECT id, run_id, seq, event, data, created_at FROM run_events WHERE run_id=$1
		ORDER BY seq DESC LIMIT $2`, runID, MaxRunEvents+1)
	if err != nil {
		return nil, false, err
	}
	truncated := len(evs) > MaxRunEvents
	if truncated {
		evs = evs[:MaxRunEvents]
	}
	slices.Reverse(evs)
	return evs, truncated, nil
}

// ================================================================== approvals

// NewApproval décrit une approbation à créer.
type NewApproval struct {
	Kind, SubjectType string
	SubjectID         int64
	Title, Effect     string
	Details           any
}

// CreateApproval crée une approbation pending (une seule par kind/sujet : renvoie l'existante sinon).
func CreateApproval(ctx context.Context, q Q, a NewApproval) (*Approval, bool, error) {
	details := JSON(a.Details)
	if details == nil {
		details = []byte("{}")
	}
	ap, err := one[Approval](ctx, q, `INSERT INTO approvals (kind, subject_type, subject_id, title, effect, details)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (kind, subject_type, subject_id) WHERE status='pending' DO NOTHING RETURNING `+approvalCols,
		a.Kind, a.SubjectType, a.SubjectID, a.Title, a.Effect, details)
	if errors.Is(err, ErrNotFound) {
		ex, err := one[Approval](ctx, q, `SELECT `+approvalCols+` FROM approvals WHERE kind=$1 AND subject_type=$2 AND subject_id=$3 AND status='pending'`,
			a.Kind, a.SubjectType, a.SubjectID)
		return ex, false, err
	}
	return ap, err == nil, err
}

// GetApproval lit une approbation.
func GetApproval(ctx context.Context, q Q, id int64) (*Approval, error) {
	return one[Approval](ctx, q, `SELECT `+approvalCols+` FROM approvals WHERE id=$1`, id)
}

// LockApproval lit une approbation avec un verrou d'écriture.
func LockApproval(ctx context.Context, q Q, id int64) (*Approval, error) {
	return one[Approval](ctx, q, `SELECT `+approvalCols+` FROM approvals WHERE id=$1 FOR UPDATE`, id)
}

// ApprovalFilter filtre les approbations.
type ApprovalFilter struct {
	Status      string
	SubjectType string
	SubjectID   int64
}

// ListApprovals liste les approbations.
func ListApprovals(ctx context.Context, q Q, f ApprovalFilter, p Page) ([]Approval, error) {
	return many[Approval](ctx, q, `SELECT `+approvalCols+` FROM approvals
		WHERE id < $1 AND ($2='' OR status=$2) AND ($3='' OR subject_type=$3) AND ($4=0 OR subject_id=$4)
		ORDER BY id DESC LIMIT $5`, cursorOrMax(p.Cursor), f.Status, f.SubjectType, f.SubjectID, p.Limit+1)
}

// DecideApproval enregistre la décision ; ErrAlreadyDecided si elle n'est plus pending.
func DecideApproval(ctx context.Context, q Q, id int64, decision, by, comment string) (*Approval, error) {
	ap, err := one[Approval](ctx, q, `UPDATE approvals SET status=$2, decision=$2, decided_by=$3, decided_at=now(),
		decision_comment=NULLIF($4,''), updated_at=now() WHERE id=$1 AND status='pending' RETURNING `+approvalCols, id, decision, by, comment)
	if errors.Is(err, ErrNotFound) {
		if _, gerr := GetApproval(ctx, q, id); gerr != nil {
			return nil, gerr
		}
		return nil, ErrAlreadyDecided
	}
	return ap, err
}

// MarkApprovalExecuted enregistre le résultat d'exécution.
func MarkApprovalExecuted(ctx context.Context, q Q, id int64, ok bool, result any) error {
	status := "executed"
	if !ok {
		status = "execution_failed"
	}
	tag, err := q.Exec(ctx, `UPDATE approvals SET status=$2, executed_at=now(), execution_result=$3, updated_at=now()
		WHERE id=$1 AND status IN ('approved','rejected')`, id, status, JSON(result))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrVersionConflict
	}
	return nil
}

// ExpirePendingApprovals expire les approbations encore actionnables et annule
// leurs jobs d'exécution durables.
func ExpirePendingApprovals(ctx context.Context, q Q, subjectType string, ids []int64) ([]int64, error) {
	rows, err := q.Query(ctx, `UPDATE approvals SET status='expired', updated_at=now()
		WHERE subject_type=$1 AND subject_id = ANY($2) AND status IN ('pending','approved','rejected') RETURNING id`, subjectType, ids)
	if err != nil {
		return nil, err
	}
	approvalIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	if len(approvalIDs) > 0 {
		if _, err := CancelJobs(ctx, q, "approval", approvalIDs); err != nil {
			return nil, err
		}
	}
	return approvalIDs, nil
}

// ================================================================== audit (lecture)

// AuditFilter filtre l'audit.
type AuditFilter struct {
	SubjectType string
	SubjectID   int64
	Actor       string
}

// ListAudit liste l'audit (ordre décroissant).
func ListAudit(ctx context.Context, q Q, f AuditFilter, p Page) ([]AuditEntry, error) {
	return many[AuditEntry](ctx, q, `SELECT `+auditCols+` FROM audit_log
		WHERE id < $1 AND ($2='' OR subject_type=$2) AND ($3=0 OR subject_id=$3) AND ($4='' OR actor=$4)
		ORDER BY id DESC LIMIT $5`, cursorOrMax(p.Cursor), f.SubjectType, f.SubjectID, f.Actor, p.Limit+1)
}

// MaxTimeline borne la timeline renvoyée par les détails feature/ticket.
const MaxTimeline = 1000

// ListAuditForSubjects renvoie les MaxTimeline dernières entrées d'audit de plusieurs sujets
// (ordre chronologique) et indique si des entrées plus anciennes ont été omises.
func ListAuditForSubjects(ctx context.Context, q Q, featureID int64, ticketIDs []int64) ([]AuditEntry, bool, error) {
	if ticketIDs == nil {
		ticketIDs = []int64{}
	}
	es, err := many[AuditEntry](ctx, q, `SELECT `+auditCols+` FROM audit_log
		WHERE (subject_type='feature' AND subject_id=$1) OR (subject_type='ticket' AND subject_id = ANY($2))
		ORDER BY id DESC LIMIT $3`, featureID, ticketIDs, MaxTimeline+1)
	if err != nil {
		return nil, false, err
	}
	truncated := len(es) > MaxTimeline
	if truncated {
		es = es[:MaxTimeline]
	}
	slices.Reverse(es)
	return es, truncated, nil
}

// ListApprovalsForSubject renvoie toutes les approbations d'un sujet (nombre borné par le
// workflow : une par étape et par rework).
func ListApprovalsForSubject(ctx context.Context, q Q, subjectType string, id int64) ([]Approval, error) {
	return many[Approval](ctx, q, `SELECT `+approvalCols+` FROM approvals WHERE subject_type=$1 AND subject_id=$2 ORDER BY id DESC`, subjectType, id)
}

// ================================================================== deliveries

// InsertDelivery enregistre une livraison (corps vérifié par HMAC) ; false si elle existait déjà.
func InsertDelivery(ctx context.Context, q Q, deliveryID, event string, payload []byte) (bool, error) {
	tag, err := q.Exec(ctx, `INSERT INTO github_deliveries (delivery_id, event, payload) VALUES ($1,$2,$3)
		ON CONFLICT (delivery_id) DO NOTHING`, deliveryID, event, payload)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// deliveryStale : une livraison « processing » plus ancienne est considérée abandonnée (crash).
const deliveryStale = `(status='processing' AND claimed_at < now() - interval '5 minutes')`

// ClaimDelivery réserve une livraison précise (première réception ou redélivrance GitHub
// d'une livraison non traitée). nil si elle est déjà traitée ou en cours.
func ClaimDelivery(ctx context.Context, q Q, deliveryID string) (*Delivery, error) {
	d, err := one[Delivery](ctx, q, `UPDATE github_deliveries SET status='processing', attempts=attempts+1, claimed_at=now(), updated_at=now()
		WHERE delivery_id=$1 AND payload IS NOT NULL AND (status IN ('received','failed') OR `+deliveryStale+`)
		RETURNING `+deliveryCols, deliveryID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return d, err
}

// ClaimDueDelivery réserve la prochaine livraison à retraiter (échec dont l'échéance est
// passée, ou traitement abandonné), hors des ids déjà traités dans la passe. nil si aucune.
func ClaimDueDelivery(ctx context.Context, q Q, exclude []int64) (*Delivery, error) {
	if exclude == nil {
		exclude = []int64{}
	}
	d, err := one[Delivery](ctx, q, `UPDATE github_deliveries SET status='processing', attempts=attempts+1, claimed_at=now(), updated_at=now()
		WHERE id = (SELECT id FROM github_deliveries WHERE payload IS NOT NULL
			AND ((status='failed' AND next_attempt_at <= now()) OR `+deliveryStale+`)
			AND id <> ALL($1)
			ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		RETURNING `+deliveryCols, exclude)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return d, err
}

// FinishDelivery termine le traitement : processed, ou failed avec prochaine échéance
// (nil = plus de reprise automatique ; une redélivrance GitHub reste possible).
func FinishDelivery(ctx context.Context, q Q, id int64, ok bool, errMsg string, next *time.Time) error {
	status := "processed"
	if !ok {
		status = "failed"
	}
	_, err := q.Exec(ctx, `UPDATE github_deliveries SET status=$2, processed=$3, last_error=NULLIF($4,''), next_attempt_at=$5,
		claimed_at=NULL, updated_at=now() WHERE id=$1`, id, status, ok, truncate(errMsg, 1000), next)
	return err
}

// GetDelivery lit une livraison.
func GetDelivery(ctx context.Context, q Q, deliveryID string) (*Delivery, error) {
	return one[Delivery](ctx, q, `SELECT `+deliveryCols+` FROM github_deliveries WHERE delivery_id=$1`, deliveryID)
}

// ================================================================== settings

// DispatchPaused lit le réglage dispatch_paused.
func DispatchPaused(ctx context.Context, q Q) (bool, error) {
	var v bool
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT value='true'::jsonb FROM settings WHERE key='dispatch_paused'), false)`).Scan(&v)
	return v, err
}

// SetDispatchPaused écrit le réglage.
func SetDispatchPaused(ctx context.Context, q Q, paused bool) error {
	_, err := q.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('dispatch_paused', $1)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now()`, JSON(paused))
	return err
}

// ================================================================== sessions

// CreateSession crée une session.
func CreateSession(ctx context.Context, q Q, tokenHash, login, csrf string, expires time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO sessions (token_hash, login, csrf_token, expires_at) VALUES ($1,$2,$3,$4)`, tokenHash, login, csrf, expires)
	return err
}

// GetSession lit une session valide.
func GetSession(ctx context.Context, q Q, tokenHash string) (*Session, error) {
	return one[Session](ctx, q, `SELECT id, token_hash, login, csrf_token, expires_at FROM sessions WHERE token_hash=$1 AND expires_at > now()`, tokenHash)
}

// DeleteSession supprime une session.
func DeleteSession(ctx context.Context, q Q, tokenHash string) error {
	_, err := q.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

// PurgeSessions supprime les sessions expirées.
func PurgeSessions(ctx context.Context, q Q) error {
	_, err := q.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	return err
}

// ================================================================== overview

// Overview regroupe les compteurs du tableau de bord.
type Overview struct {
	PendingApprovals int
	BlockedFeatures  int
	BlockedTickets   int
	ActiveRuns       int
	TokensToday      int64
	TokensTodaySplit TokenSplit
	Tokens7d         []DayTokens
}

// TokenSplit répartit une consommation : entrée nouvelle, relue du cache, sortie.
type TokenSplit struct {
	FreshInput int64 `json:"fresh_input"`
	CacheRead  int64 `json:"cache_read"`
	Output     int64 `json:"output"`
}

// DayTokens est une consommation journalière.
type DayTokens struct {
	Day   string `json:"day"`
	Total int64  `json:"total"`
	TokenSplit
}

// GetOverview calcule les compteurs.
func GetOverview(ctx context.Context, q Q) (*Overview, error) {
	o := &Overview{}
	err := q.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM approvals WHERE status='pending'),
		(SELECT count(*) FROM features WHERE state='BLOCKED'),
		(SELECT count(*) FROM tickets WHERE state='BLOCKED'),
		(SELECT count(*) FROM runs WHERE status IN ('submitted','running','stopping')),
		(SELECT COALESCE(sum(total_tokens),0) FROM runs WHERE started_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		(SELECT COALESCE(sum(GREATEST(input_tokens-cache_read_tokens,0)),0) FROM runs WHERE started_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		(SELECT COALESCE(sum(cache_read_tokens),0) FROM runs WHERE started_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'),
		(SELECT COALESCE(sum(output_tokens),0) FROM runs WHERE started_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')`).
		Scan(&o.PendingApprovals, &o.BlockedFeatures, &o.BlockedTickets, &o.ActiveRuns, &o.TokensToday,
			&o.TokensTodaySplit.FreshInput, &o.TokensTodaySplit.CacheRead, &o.TokensTodaySplit.Output)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT to_char(d, 'YYYY-MM-DD'),
		COALESCE(sum(r.total_tokens),0)::bigint, COALESCE(sum(GREATEST(r.input_tokens-r.cache_read_tokens,0)),0)::bigint,
		COALESCE(sum(r.cache_read_tokens),0)::bigint, COALESCE(sum(r.output_tokens),0)::bigint
		FROM generate_series((now() AT TIME ZONE 'UTC')::date - 6, (now() AT TIME ZONE 'UTC')::date, interval '1 day') d
		LEFT JOIN runs r ON (r.started_at AT TIME ZONE 'UTC')::date = d::date
		GROUP BY d ORDER BY d`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DayTokens
		if err := rows.Scan(&d.Day, &d.Total, &d.FreshInput, &d.CacheRead, &d.Output); err != nil {
			return nil, err
		}
		o.Tokens7d = append(o.Tokens7d, d)
	}
	return o, rows.Err()
}

// ProjectNames renvoie id → slug.
func ProjectNames(ctx context.Context, q Q) (map[int64]string, error) {
	rows, err := q.Query(ctx, `SELECT id, slug FROM projects`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var s string
		if err := rows.Scan(&id, &s); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// ListPipelineFeatures liste les features non terminales récentes.
func ListPipelineFeatures(ctx context.Context, q Q) ([]Feature, error) {
	return many[Feature](ctx, q, `SELECT `+featureCols+` FROM features WHERE state NOT IN ('DONE','CANCELLED') OR updated_at > now() - interval '7 days' ORDER BY updated_at DESC LIMIT 500`)
}

// TicketBrief résume un ticket pour l'avancement d'une feature.
type TicketBrief struct {
	Key   string `json:"key"`
	State string `json:"state"`
}

// FeatureProgress résume l'avancement des tickets d'une feature (tickets annulés exclus).
type FeatureProgress struct {
	Total   int          `json:"total"`
	Done    int          `json:"done"`
	Current *TicketBrief `json:"current"` // premier ticket ni terminé ni planifié, dans l'ordre
}

// FeatureTicketProgress calcule l'avancement de chaque feature demandée.
func FeatureTicketProgress(ctx context.Context, q Q, featureIDs []int64) (map[int64]*FeatureProgress, error) {
	out := map[int64]*FeatureProgress{}
	if len(featureIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT feature_id, key, state FROM tickets
		WHERE feature_id = ANY($1) AND state <> 'CANCELLED' ORDER BY feature_id, seq, id`, featureIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var fid int64
		var b TicketBrief
		if err := rows.Scan(&fid, &b.Key, &b.State); err != nil {
			return nil, err
		}
		p := out[fid]
		if p == nil {
			p = &FeatureProgress{}
			out[fid] = p
		}
		p.Total++
		switch {
		case b.State == "DONE":
			p.Done++
		case p.Current == nil && b.State != "PLANNED":
			bb := b
			p.Current = &bb
		}
	}
	return out, rows.Err()
}

// FeatureTitles renvoie le titre des features demandées.
func FeatureTitles(ctx context.Context, q Q, ids []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT id, title FROM features WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var t string
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// ListPipelineTickets liste les tickets non terminaux récents.
func ListPipelineTickets(ctx context.Context, q Q) ([]Ticket, error) {
	return many[Ticket](ctx, q, `SELECT `+ticketCols+` FROM tickets WHERE state NOT IN ('DONE','CANCELLED') OR updated_at > now() - interval '7 days' ORDER BY updated_at DESC LIMIT 500`)
}

// Ping vérifie la base.
func (s *Store) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("base injoignable")
	}
	return nil
}
