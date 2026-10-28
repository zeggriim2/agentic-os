package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
	"github.com/zeggriim2/agentic-os/internal/testutil/pgtest"
	"github.com/zeggriim2/agentic-os/migrations"
)

func mkProject(t *testing.T, s *store.Store, slug string) *store.Project {
	t.Helper()
	p, err := store.CreateProject(context.Background(), s.Pool, store.ProjectInput{
		Slug: slug, Name: "Demo", Repository: "o/r", BaseBranch: "main", Mode: "sandbox", RequiredChecks: []string{"tests"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMigrationsIdempotent(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	applied, err := s.Migrate(ctx)
	if err != nil || len(applied) != 0 {
		t.Fatalf("2e migration: %v %v", applied, err)
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil || v != store.ExpectedSchemaVersion() || v < 1 {
		t.Fatalf("version %d (attendue %d) %v", v, store.ExpectedSchemaVersion(), err)
	}
	// migrations concurrentes : le verrou consultatif sérialise
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Migrate(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	agents, err := store.ListAgents(ctx, s.Pool)
	if err != nil || len(agents) != 5 {
		t.Fatalf("agents: %d %v", len(agents), err)
	}
}

func TestApprovalMigrationRequiresFreshHumanDecision(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	p := mkProject(t, s, "upgrade")
	f, err := store.CreateFeature(ctx, s.Pool, p.ID, "Upgrade", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetFeatureDesign(ctx, s.Pool, f.ID, map[string]any{"tickets": []map[string]any{{"title": "T"}}}); err != nil {
		t.Fatal(err)
	}
	ap, _, err := store.CreateApproval(ctx, s.Pool, store.NewApproval{Kind: "design_review", SubjectType: "feature", SubjectID: f.ID,
		Title: "Ancienne conception", Effect: "Ancien effet", Details: map[string]any{"legacy": true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DecideApproval(ctx, s.Pool, ap.ID, "approved", "user:admin", "ancienne décision"); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.FS.ReadFile("0002_durable_approval_execution.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	ap, err = store.GetApproval(ctx, s.Pool, ap.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ap.Status != "pending" || ap.Decision != nil || ap.DecidedBy != nil || ap.DecidedAt != nil {
		t.Fatalf("une nouvelle décision humaine doit être requise: %+v", ap)
	}
	var details map[string]any
	if err := json.Unmarshal(ap.Details, &details); err != nil {
		t.Fatal(err)
	}
	if details["repository"] != p.Repository || details["base_branch"] != p.BaseBranch || !strings.Contains(ap.Effect, p.Repository) {
		t.Fatalf("snapshot migré incohérent: details=%s effect=%s", ap.Details, ap.Effect)
	}
	jobs, err := store.ListActiveJobs(ctx, s.Pool)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("ancienne décision exécutée silencieusement: %+v err=%v", jobs, err)
	}
}

func TestDequeueSkipLockedConcurrent(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	const n = 40
	for i := 0; i < n; i++ {
		if _, _, err := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: "sync_agents", SubjectType: "system", SubjectID: int64(i), IdempotencyKey: fmt.Sprintf("k%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	seen := map[int64]string{}
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		owner := []string{"w1", "w2"}[w]
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, err := store.Dequeue(ctx, s.Pool, owner, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if j == nil {
					return
				}
				mu.Lock()
				if prev, dup := seen[j.ID]; dup {
					t.Errorf("job %d pris par %s et %s", j.ID, prev, owner)
				}
				seen[j.ID] = owner
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("%d jobs pris sur %d", len(seen), n)
	}
}

func TestDequeueRespectsPauseAndProjectSerialization(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	p := mkProject(t, s, "demo")
	for i, kind := range []string{"develop", "review", "po_spec"} {
		if _, _, err := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: kind, SubjectType: "ticket", SubjectID: int64(i + 1), ProjectID: &p.ID, IdempotencyKey: kind}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetDispatchPaused(ctx, s.Pool, true); err != nil {
		t.Fatal(err)
	}
	if j, _ := store.Dequeue(ctx, s.Pool, "w", time.Minute); j != nil {
		t.Fatal("job pris alors que le dispatch est en pause")
	}
	_ = store.SetDispatchPaused(ctx, s.Pool, false)
	j1, _ := store.Dequeue(ctx, s.Pool, "w", time.Minute)
	j2, _ := store.Dequeue(ctx, s.Pool, "w", time.Minute)
	if j1 == nil || j1.Kind != "develop" || j2 == nil || j2.Kind != "po_spec" {
		t.Fatalf("sérialisation par projet: %+v %+v", j1, j2)
	}
	if j3, _ := store.Dequeue(ctx, s.Pool, "w", time.Minute); j3 != nil {
		t.Fatal("review pris alors qu'un develop est actif sur le projet")
	}
	if err := store.CompleteJob(ctx, s.Pool, j1.ID, "w"); err != nil {
		t.Fatal(err)
	}
	if j3, _ := store.Dequeue(ctx, s.Pool, "w", time.Minute); j3 == nil || j3.Kind != "review" {
		t.Fatal("review non pris après la fin du develop")
	}
}

func TestLeaseExpiryAndRecovery(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	job, created, err := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "a", MaxAttempts: 2})
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, created, _ := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "a"}); created {
		t.Fatal("clé d'idempotence dupliquée acceptée")
	}
	j, _ := store.Dequeue(ctx, s.Pool, "w1", time.Second)
	if j == nil || j.ID != job.ID || j.Attempts != 1 {
		t.Fatalf("dequeue: %+v", j)
	}
	ok, _ := store.Heartbeat(ctx, s.Pool, j.ID, "w1", time.Second)
	if !ok {
		t.Fatal("heartbeat refusé")
	}
	if ok, _ := store.Heartbeat(ctx, s.Pool, j.ID, "autre", time.Second); ok {
		t.Fatal("heartbeat d'un autre worker accepté")
	}
	time.Sleep(1200 * time.Millisecond)
	n, err := store.RecoverExpired(ctx, s.Pool)
	if err != nil || n != 1 {
		t.Fatalf("recovery 1: %d %v", n, err)
	}
	if none, err := store.FailOneExhausted(ctx, s.Pool); err != nil || none != nil {
		t.Fatalf("aucun job épuisé attendu: %+v %v", none, err)
	}
	if ok, _ := store.Heartbeat(ctx, s.Pool, j.ID, "w1", time.Second); ok {
		t.Fatal("lease perdu non détecté")
	}
	if err := store.CompleteJob(ctx, s.Pool, j.ID, "w1"); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("completion après perte du lease: %v", err)
	}
	j, _ = store.Dequeue(ctx, s.Pool, "w2", time.Second)
	if j == nil || j.Attempts != 2 {
		t.Fatalf("reprise: %+v", j)
	}
	time.Sleep(1200 * time.Millisecond)
	if n, err := store.RecoverExpired(ctx, s.Pool); err != nil || n != 0 {
		t.Fatalf("un job épuisé ne doit pas être remis en file: %d %v", n, err)
	}
	failed, err := store.FailOneExhausted(ctx, s.Pool)
	if err != nil || failed == nil || failed.Status != "failed" || failed.ID != j.ID {
		t.Fatalf("épuisement: %+v %v", failed, err)
	}
}

func TestRunAttachDoesNotResubmit(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	job, _, _ := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "x"})
	r, err := store.CreateRun(ctx, s.Pool, job.ID, "po", "aos-po")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunHermesID(ctx, s.Pool, r.ID, "run_abc"); err != nil {
		t.Fatal(err)
	}
	got, err := store.LatestRunForJob(ctx, s.Pool, job.ID)
	if err != nil || got == nil || got.HermesRunID == nil || *got.HermesRunID != "run_abc" || got.Status != "running" {
		t.Fatalf("rattachement: %+v %v", got, err)
	}
	// le même hermes_run_id ne peut être rattaché à un 2e run
	r2, _ := store.CreateRun(ctx, s.Pool, job.ID, "po", "aos-po")
	if err := store.SetRunHermesID(ctx, s.Pool, r2.ID, "run_abc"); !store.IsUniqueViolation(err) {
		t.Fatalf("hermes_run_id dupliqué accepté: %v", err)
	}
	big := make([]byte, store.MaxOutput+1000)
	for i := range big {
		big[i] = 'a'
	}
	if err := store.FinishRun(ctx, s.Pool, r.ID, store.RunResult{Status: "completed", Output: string(big), TotalTokens: 10, Artifact: map[string]any{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.GetRun(ctx, s.Pool, r.ID)
	if len(*got.Output) != store.MaxOutput || got.FinishedAt == nil {
		t.Fatal("sortie non bornée")
	}
	if ins, _ := store.InsertRunEvent(ctx, s.Pool, r.ID, 1, "tool.started", []byte(`{"a":1}`)); !ins {
		t.Fatal("événement non inséré")
	}
	if ins, _ := store.InsertRunEvent(ctx, s.Pool, r.ID, 1, "tool.started", []byte(`{"a":1}`)); ins {
		t.Fatal("événement dupliqué inséré")
	}
}

func TestAuditTriggerRefusesUpdateDelete(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	if err := audit.Write(ctx, s.Pool, audit.Entry{Actor: audit.System, Action: "test", SubjectType: "feature", SubjectID: 1, After: map[string]any{"x": 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE audit_log SET action='x'`); err == nil {
		t.Fatal("UPDATE accepté")
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM audit_log`); err == nil {
		t.Fatal("DELETE accepté")
	}
	if _, err := s.Pool.Exec(ctx, `TRUNCATE audit_log`); err == nil {
		t.Fatal("TRUNCATE accepté")
	}
	entries, err := store.ListAudit(ctx, s.Pool, store.AuditFilter{SubjectType: "feature", SubjectID: 1}, store.Page{Limit: 10})
	if err != nil || len(entries) != 1 {
		t.Fatalf("audit: %v %v", entries, err)
	}
}

func TestOptimisticLock(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	p := mkProject(t, s, "demo")
	f, err := store.CreateFeature(ctx, s.Pool, p.ID, "F", "d")
	if err != nil || f.Version != 1 || f.State != "IDEA" {
		t.Fatal(err)
	}
	f2, err := store.SetFeatureState(ctx, s.Pool, f.ID, 1, "SPEC", "", "")
	if err != nil || f2.Version != 2 {
		t.Fatal(err)
	}
	if _, err := store.SetFeatureState(ctx, s.Pool, f.ID, 1, "BLOCKED", "SPEC", "x"); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("version périmée acceptée: %v", err)
	}
	tk, err := store.InsertTicket(ctx, s.Pool, store.TicketInput{FeatureID: f.ID, ProjectID: p.ID, Key: "demo-F1-T1", Seq: 1, Title: "T", Acceptance: []string{"a"}})
	if err != nil || tk.State != "PLANNED" {
		t.Fatal(err)
	}
	tk2, _ := store.InsertTicket(ctx, s.Pool, store.TicketInput{FeatureID: f.ID, ProjectID: p.ID, Key: "demo-F1-T1", Seq: 1, Title: "autre"})
	if tk2.ID != tk.ID || tk2.Title != "T" {
		t.Fatal("ticket dupliqué")
	}
	if _, err := store.SetTicketState(ctx, s.Pool, tk.ID, 99, "READY_FOR_DEV", "", ""); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatal("version ticket périmée acceptée")
	}
}

func TestApprovalsUniquePendingAndDecision(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	a1, created, err := store.CreateApproval(ctx, s.Pool, store.NewApproval{Kind: "spec_review", SubjectType: "feature", SubjectID: 7, Title: "t", Effect: "e"})
	if err != nil || !created {
		t.Fatal(err)
	}
	a2, created, err := store.CreateApproval(ctx, s.Pool, store.NewApproval{Kind: "spec_review", SubjectType: "feature", SubjectID: 7, Title: "t2", Effect: "e"})
	if err != nil || created || a2.ID != a1.ID {
		t.Fatalf("2e pending créée: %v %v", created, err)
	}
	// insertion directe concurrente refusée par l'index unique partiel
	_, err = s.Pool.Exec(ctx, `INSERT INTO approvals (kind, subject_type, subject_id, title, effect) VALUES ('spec_review','feature',7,'x','y')`)
	if !store.IsUniqueViolation(err) {
		t.Fatalf("index unique partiel absent: %v", err)
	}
	d, err := store.DecideApproval(ctx, s.Pool, a1.ID, "approved", "user:admin", "")
	if err != nil || d.Status != "approved" || d.DecidedBy == nil {
		t.Fatal(err)
	}
	if _, err := store.DecideApproval(ctx, s.Pool, a1.ID, "rejected", "user:admin", "non"); !errors.Is(err, store.ErrAlreadyDecided) {
		t.Fatalf("décision répétée acceptée: %v", err)
	}
	if _, err := store.DecideApproval(ctx, s.Pool, 999999, "approved", "u", ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("approbation inexistante: %v", err)
	}
	// une nouvelle pending est possible une fois la précédente décidée
	if _, created, err := store.CreateApproval(ctx, s.Pool, store.NewApproval{Kind: "spec_review", SubjectType: "feature", SubjectID: 7, Title: "t3", Effect: "e"}); err != nil || !created {
		t.Fatal("nouvelle pending refusée")
	}
}

func TestNotifyDeliveredOnCommit(t *testing.T) {
	s := pgtest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+store.Channel); err != nil {
		t.Fatal(err)
	}
	err = s.InTx(ctx, func(tx pgx.Tx) error { return store.Notify(ctx, tx, "feature.updated", 3) })
	if err != nil {
		t.Fatal(err)
	}
	n, err := conn.Conn().WaitForNotification(ctx)
	if err != nil || n.Payload != `{"id":3,"type":"feature.updated"}` {
		t.Fatalf("notification: %+v %v", n, err)
	}
}

func TestDeliveriesDedup(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	if ok, _ := store.InsertDelivery(ctx, s.Pool, "d1", "pull_request", []byte(`{}`)); !ok {
		t.Fatal("1re livraison refusée")
	}
	if ok, _ := store.InsertDelivery(ctx, s.Pool, "d1", "pull_request", []byte(`{}`)); ok {
		t.Fatal("doublon accepté")
	}
	d, err := store.ClaimDelivery(ctx, s.Pool, "d1")
	if err != nil || d == nil || d.Attempts != 1 || d.Status != "processing" {
		t.Fatalf("réservation: %+v %v", d, err)
	}
	if again, _ := store.ClaimDelivery(ctx, s.Pool, "d1"); again != nil {
		t.Fatal("livraison en cours réservée deux fois")
	}
	past := time.Now().Add(-time.Second)
	if err := store.FinishDelivery(ctx, s.Pool, d.ID, false, "panne", &past); err != nil {
		t.Fatal(err)
	}
	due, err := store.ClaimDueDelivery(ctx, s.Pool, nil)
	if err != nil || due == nil || due.Attempts != 2 {
		t.Fatalf("reprise: %+v %v", due, err)
	}
	if err := store.FinishDelivery(ctx, s.Pool, due.ID, true, "", nil); err != nil {
		t.Fatal(err)
	}
	if done, _ := store.ClaimDelivery(ctx, s.Pool, "d1"); done != nil {
		t.Fatal("livraison traitée réservée à nouveau")
	}
}

// ------------------------------------------------------------------ répartition des tokens

func TestRunTokenSplitAndOverview(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	job, _, err := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := store.CreateRun(ctx, s.Pool, job.ID, "developer", "aos-developer")
	if err := store.FinishRun(ctx, s.Pool, r.ID, store.RunResult{Status: "completed", InputTokens: 305443, OutputTokens: 2637,
		TotalTokens: 308080, CacheReadTokens: 248960, CacheWriteTokens: 12}); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetRun(ctx, s.Pool, r.ID)
	if got.CacheReadTokens != 248960 || got.CacheWriteTokens != 12 {
		t.Fatalf("cache non enregistré : %+v", got)
	}
	// Un cache supérieur à l'entrée (donnée incohérente) est borné : l'entrée nouvelle reste ≥ 0.
	r2, _ := store.CreateRun(ctx, s.Pool, job.ID, "qa", "aos-qa")
	if err := store.FinishRun(ctx, s.Pool, r2.ID, store.RunResult{Status: "completed", InputTokens: 100, OutputTokens: 5,
		TotalTokens: 105, CacheReadTokens: 900, CacheWriteTokens: -3}); err != nil {
		t.Fatal(err)
	}
	got2, _ := store.GetRun(ctx, s.Pool, r2.ID)
	if got2.CacheReadTokens != 100 || got2.CacheWriteTokens != 0 {
		t.Fatalf("bornes : %+v", got2)
	}
	o, err := store.GetOverview(ctx, s.Pool)
	if err != nil {
		t.Fatal(err)
	}
	want := store.TokenSplit{FreshInput: 305443 - 248960, CacheRead: 248960 + 100, Output: 2637 + 5}
	if o.TokensToday != 308080+105 || o.TokensTodaySplit != want {
		t.Fatalf("aujourd'hui : %d %+v", o.TokensToday, o.TokensTodaySplit)
	}
	if len(o.Tokens7d) != 7 || o.Tokens7d[6].TokenSplit != want || o.Tokens7d[6].Total != 308185 || o.Tokens7d[0].Total != 0 {
		t.Fatalf("7 jours : %+v", o.Tokens7d)
	}
}

func TestTokenSplitMigrationBackfillsFromRunEvents(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	job, _, err := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "bf"})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(input int64, usage string) int64 {
		r, _ := store.CreateRun(ctx, s.Pool, job.ID, "po", "aos-po")
		if err := store.FinishRun(ctx, s.Pool, r.ID, store.RunResult{Status: "completed", InputTokens: input, TotalTokens: input}); err != nil {
			t.Fatal(err)
		}
		if usage != "" {
			if _, err := store.InsertRunEvent(ctx, s.Pool, r.ID, 1, "run.completed", []byte(`{"usage":`+usage+`}`)); err != nil {
				t.Fatal(err)
			}
		}
		return r.ID
	}
	ok := mk(305443, `{"input_tokens":305443,"cache_read_tokens":248960,"cache_write_tokens":7}`)
	none := mk(50, "")
	bad := mk(50, `{"cache_read_tokens":"x","cache_write_tokens":"y"}`)
	over := mk(10, `{"cache_read_tokens":99}`)
	// Simule une base 0003 : colonnes supprimées puis migration 0004 rejouée.
	if _, err := s.Pool.Exec(ctx, `ALTER TABLE runs DROP COLUMN cache_read_tokens, DROP COLUMN cache_write_tokens`); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.FS.ReadFile("0004_token_usage_split.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64][2]int64{ok: {248960, 7}, none: {0, 0}, bad: {0, 0}, over: {10, 0}} {
		r, _ := store.GetRun(ctx, s.Pool, id)
		if r.CacheReadTokens != want[0] || r.CacheWriteTokens != want[1] {
			t.Fatalf("run %d : cache %d/%d, attendu %v", id, r.CacheReadTokens, r.CacheWriteTokens, want)
		}
	}
}

func TestAgentModelsMigrationUpgradesLegacyRows(t *testing.T) {
	s := pgtest.New(t)
	ctx := context.Background()
	p := mkProject(t, s, "legacy")
	job, _, err := store.EnqueueJob(ctx, s.Pool, store.NewJob{Kind: "po_spec", SubjectType: "feature", SubjectID: 1, IdempotencyKey: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := store.CreateRun(ctx, s.Pool, job.ID, "po", "aos-po")
	if err := store.FinishRun(ctx, s.Pool, r.ID, store.RunResult{Status: "completed", Model: "m1", Provider: "p"}); err != nil {
		t.Fatal(err)
	}
	// Simule une base 0006 (sans les colonnes) puis rejoue la migration 0007 sur les lignes existantes.
	if _, err := s.Pool.Exec(ctx, `ALTER TABLE projects DROP COLUMN agent_models;
		ALTER TABLE runs DROP COLUMN requested_model, DROP COLUMN requested_provider, DROP COLUMN model_fallback`); err != nil {
		t.Fatal(err)
	}
	sql, err := migrations.FS.ReadFile("0007_project_agent_models.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	if v, err := s.SchemaVersion(ctx); err != nil || v != 7 || store.ExpectedSchemaVersion() != 7 {
		t.Fatalf("version %d (attendue 7) %v", v, err)
	}
	got, err := store.GetProject(ctx, s.Pool, p.ID)
	if err != nil || got.AgentModels == nil || len(got.AgentModels) != 0 {
		t.Fatalf("projet legacy : agent_models=%v err=%v", got.AgentModels, err)
	}
	run, err := store.GetRun(ctx, s.Pool, r.ID)
	if err != nil || run.RequestedModel != nil || run.RequestedProvider != nil || run.ModelFallback || *run.Model != "m1" {
		t.Fatalf("run legacy : %+v err=%v", run, err)
	}
	// Les nouvelles colonnes fonctionnent : projet avec modèles, run avec surcharge puis repli.
	up, err := store.UpdateProject(ctx, s.Pool, p.ID, store.ProjectInput{Slug: p.Slug, Name: p.Name, Repository: p.Repository,
		BaseBranch: p.BaseBranch, Mode: p.Mode, RequiredChecks: p.RequiredChecks,
		AgentModels: map[string]domain.ModelChoice{"developer": {Provider: "anthropic", Model: "claude-sonnet-5"}}})
	if err != nil || up.AgentModels["developer"].Model != "claude-sonnet-5" || up.AgentModels["developer"].Provider != "anthropic" {
		t.Fatalf("agent_models : %v %v", up.AgentModels, err)
	}
	if m := up.ModelFor("developer", "gpt-9"); m.Provider != "anthropic" || m.Model != "claude-sonnet-5" {
		t.Fatalf("ModelFor projet : %+v", m)
	}
	if m := up.ModelFor("qa", "gpt-9"); m.Provider != "" || m.Model != "gpt-9" {
		t.Fatalf("ModelFor agent : %+v", m)
	}
	if m := up.ModelFor("qa", ""); !m.Empty() {
		t.Fatalf("ModelFor défaut : %+v", m)
	}
	// CHECK jsonb_typeof = object.
	if _, err := s.Pool.Exec(ctx, `UPDATE projects SET agent_models='[]'::jsonb WHERE id=$1`, p.ID); err == nil {
		t.Fatal("agent_models non objet doit être refusé")
	}
	r2, _ := store.CreateRun(ctx, s.Pool, job.ID, "developer", "aos-developer")
	if err := store.SetRunRequestedModel(ctx, s.Pool, r2.ID, domain.ModelChoice{Provider: "anthropic", Model: "claude-sonnet-5"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunHermesID(ctx, s.Pool, r2.ID, "run_a"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRunModelFallback(ctx, s.Pool, r2.ID, "run_b"); err != nil {
		t.Fatal(err)
	}
	run, _ = store.GetRun(ctx, s.Pool, r2.ID)
	if !run.ModelFallback || *run.HermesRunID != "run_b" || *run.RequestedProvider != "anthropic" || *run.RequestedModel != "claude-sonnet-5" || run.Status != "running" {
		t.Fatalf("run après repli : %+v", run)
	}
}
