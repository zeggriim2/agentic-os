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
	n, exhausted, err := store.RecoverExpired(ctx, s.Pool)
	if err != nil || n != 1 || len(exhausted) != 0 {
		t.Fatalf("recovery 1: %d %v %v", n, exhausted, err)
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
	_, exhausted, err = store.RecoverExpired(ctx, s.Pool)
	if err != nil || len(exhausted) != 1 || exhausted[0].Status != "failed" {
		t.Fatalf("épuisement: %+v %v", exhausted, err)
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
	if ok, _ := store.RecordDelivery(ctx, s.Pool, "d1", "pull_request"); !ok {
		t.Fatal("1re livraison refusée")
	}
	if ok, _ := store.RecordDelivery(ctx, s.Pool, "d1", "pull_request"); ok {
		t.Fatal("doublon accepté")
	}
}
