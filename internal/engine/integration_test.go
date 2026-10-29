package engine_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/engine"
	"github.com/zeggriim2/agentic-os/internal/store"
)

func (ev *env) branchSHA(branch string) string {
	ev.t.Helper()
	sha, err := ev.g.BranchSHA(context.Background(), "o/r", branch)
	if err != nil {
		ev.t.Fatalf("branche %s : %v", branch, err)
	}
	return sha
}

func (ev *env) isAncestor(a, b string) bool {
	return exec.Command("/usr/bin/git", "--git-dir", ev.g.bare, "merge-base", "--is-ancestor", a, b).Run() == nil
}

func (ev *env) ticketByID(id int64) *store.Ticket {
	ev.t.Helper()
	tk, err := store.GetTicket(context.Background(), ev.s.Pool, id)
	if err != nil {
		ev.t.Fatal(err)
	}
	return tk
}

// publishAndMerge approuve la PR du ticket, l'ouvre, la merge (fast-forward) et réconcilie.
func (ev *env) publishAndMerge(id int64) int {
	ev.t.Helper()
	ev.decide(domain.ApprovalPublishPR, "approved", "")
	ev.drain()
	tk := ev.ticketByID(id)
	expect(ev.t, "PR ouverte", tk.State, "PR_OPENED")
	ev.g.mergePR(ev.t, *tk.PRNumber)
	if err := ev.e.ReconcilePR(context.Background(), id); err != nil {
		ev.t.Fatal(err)
	}
	expect(ev.t, "ticket mergé", ev.ticketState(id), "DONE")
	return *tk.PRNumber
}

func TestFeatureBranchIntegrationFullCycle(t *testing.T) {
	ev := newEnv(t)
	ev.integration = domain.IntegrationFeatureBranch
	ctx := context.Background()
	mainSHA := ev.branchSHA("main")
	f, ts := ev.toDevGateTwo()
	t1, t2 := ts[0], ts[1]
	fe := ev.feature(f.ID)
	expect(t, "mode hérité du projet", fe.IntegrationMode, domain.IntegrationFeatureBranch)
	branch := domain.FeatureBranchName(f.ID, f.Title)
	if fe.FeatureBranch == nil || *fe.FeatureBranch != branch {
		t.Fatalf("branche fixée au feu vert : %v", fe.FeatureBranch)
	}

	// T1 : la branche de feature est créée depuis main, le ticket part d'elle et sa PR la vise.
	ev.drain()
	expect(t, "branche de feature créée depuis main", ev.branchSHA(branch), mainSHA)
	expect(t, "T1 validé", ev.ticketState(t1.ID), "AWAITING_PR_APPROVAL")
	if ap := ev.pending(domain.ApprovalPublishPR); !strings.Contains(ap.Effect, "vers "+branch) {
		t.Fatalf("effet de publication : %s", ap.Effect)
	}
	ev.publishAndMerge(t1.ID)
	if pr := ev.g.prs["aos/"+t1.Key]; pr.Base.Ref != branch {
		t.Fatalf("PR T1 vers %s", pr.Base.Ref)
	}
	if !strings.Contains(ev.g.prBody, "Refs #") || strings.Contains(ev.g.prBody, "Closes #") {
		t.Fatalf("corps de PR de ticket en mode branche de feature : %s", ev.g.prBody)
	}
	t1SHA := *ev.ticketByID(t1.ID).HeadSHA
	expect(t, "main inchangé", ev.branchSHA("main"), mainSHA)
	expect(t, "feature toujours en cours", ev.feature(f.ID).State, "IN_DEV")

	// T2 part de la branche de feature à jour (contient T1).
	ev.drain()
	expect(t, "T2 validé", ev.ticketState(t2.ID), "AWAITING_PR_APPROVAL")
	if !ev.isAncestor(t1SHA, *ev.ticketByID(t2.ID).HeadSHA) {
		t.Fatal("T2 doit partir de la branche de feature contenant T1")
	}
	ev.publishAndMerge(t2.ID)

	// Tous les tickets intégrés : recette, approbation de la PR finale.
	fe = ev.feature(f.ID)
	expect(t, "recette", fe.State, "ACCEPTANCE")
	ap := ev.pending(domain.ApprovalPublishFeaturePR)
	if !strings.Contains(ap.Effect, branch+" vers main") || !strings.Contains(ap.Effect, "#1, #2") {
		t.Fatalf("effet de la PR finale : %s", ap.Effect)
	}
	expect(t, "main toujours inchangé", ev.branchSHA("main"), mainSHA)
	if !hasMsg(ev.n.msgs, "🧪 Tous les tickets") {
		t.Fatalf("notification de recette absente : %v", ev.n.msgs)
	}
	ev.decide(domain.ApprovalPublishFeaturePR, "approved", "")
	ev.drain()
	fe = ev.feature(f.ID)
	if fe.FeaturePRNumber == nil {
		t.Fatal("PR finale non enregistrée")
	}
	fpr := ev.g.prs[branch]
	if fpr == nil || fpr.Base.Ref != "main" || !fpr.Draft || *fe.FeaturePRNumber != fpr.Number {
		t.Fatalf("PR finale : %+v", fpr)
	}
	if !strings.Contains(ev.g.prBody, "Closes #1") || !strings.Contains(ev.g.prBody, "Closes #2") {
		t.Fatalf("corps de PR finale : %s", ev.g.prBody)
	}
	expect(t, "toujours en recette tant que non mergée", ev.feature(f.ID).State, "ACCEPTANCE")
	if err := ev.e.PollPRs(ctx); err != nil {
		t.Fatal(err)
	}
	expect(t, "PR ouverte : recette", ev.feature(f.ID).State, "ACCEPTANCE")

	// Merge humain de la PR finale : feature terminée, main contient tout.
	ev.g.mergePR(t, fpr.Number)
	if n, err := ev.e.HandleGitHubEvent(ctx, "o/r", fpr.Number, "", branch); err != nil || n != 1 {
		t.Fatalf("webhook PR finale : %d %v", n, err)
	}
	expect(t, "feature terminée", ev.feature(f.ID).State, "DONE")
	if !ev.isAncestor(*ev.ticketByID(t2.ID).HeadSHA, ev.branchSHA("main")) {
		t.Fatal("main doit contenir les tickets après le merge de la PR finale")
	}
}

func hasMsg(msgs []string, part string) bool {
	for _, m := range msgs {
		if strings.Contains(m, part) {
			return true
		}
	}
	return false
}

// toAcceptance : feature en mode branche de feature, deux tickets mergés dans la branche.
func (ev *env) toAcceptance() *store.Feature {
	ev.t.Helper()
	ev.integration = domain.IntegrationFeatureBranch
	f, ts := ev.toDevGateTwo()
	ev.drain()
	ev.publishAndMerge(ts[0].ID)
	ev.drain()
	ev.publishAndMerge(ts[1].ID)
	expect(ev.t, "recette", ev.feature(f.ID).State, "ACCEPTANCE")
	return f
}

func TestFeaturePRRejectedClosedAndRetried(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f := ev.toAcceptance()
	ev.decide(domain.ApprovalPublishFeaturePR, "rejected", "pas encore")
	ev.drain()
	fe := ev.feature(f.ID)
	expect(t, "refus : bloquée", fe.State, "BLOCKED")
	if !strings.Contains(fe.StateReason, "PR finale refusée") {
		t.Fatalf("raison : %s", fe.StateReason)
	}
	// Le mode ne peut plus changer (tickets mergés).
	var ue *engine.UserError
	if _, err := ev.e.SetFeatureIntegration(ctx, f.ID, domain.IntegrationDirect, "admin"); !errors.As(err, &ue) || ue.Status != 409 {
		t.Fatalf("changement de mode après merge : %v", err)
	}
	// Reprise : nouvelle approbation, puis PR fermée sans merge → bloquée, PR oubliée.
	if _, err := ev.e.FeatureAction(ctx, f.ID, "retry", "admin"); err != nil {
		t.Fatal(err)
	}
	expect(t, "reprise", ev.feature(f.ID).State, "ACCEPTANCE")
	ev.decide(domain.ApprovalPublishFeaturePR, "approved", "")
	ev.drain()
	n := *ev.feature(f.ID).FeaturePRNumber
	ev.g.prs[*ev.feature(f.ID).FeatureBranch].State = "closed"
	if err := ev.e.ReconcileFeaturePR(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	fe = ev.feature(f.ID)
	expect(t, "PR fermée : bloquée", fe.State, "BLOCKED")
	if fe.FeaturePRNumber != nil || !strings.Contains(fe.StateReason, "fermée sans merge") {
		t.Fatalf("PR finale oubliée : %v %s (#%d)", fe.FeaturePRNumber, fe.StateReason, n)
	}
	if _, err := ev.e.FeatureAction(ctx, f.ID, "retry", "admin"); err != nil {
		t.Fatal(err)
	}
	if ap := ev.pending(domain.ApprovalPublishFeaturePR); ap == nil {
		t.Fatal("nouvelle approbation attendue")
	}
}

func TestSwitchToFeatureBranchRetargetsOpenPR(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	mainSHA := ev.branchSHA("main")
	f, ts := ev.toDevGateTwo()
	t1 := ts[0]
	expect(t, "direct par défaut", ev.feature(f.ID).IntegrationMode, domain.IntegrationDirect)
	ev.drain()
	ev.decide(domain.ApprovalPublishPR, "approved", "")
	ev.drain()
	tk := ev.ticketByID(t1.ID)
	expect(t, "PR T1 ouverte", tk.State, "PR_OPENED")
	if pr := ev.g.prs["aos/"+t1.Key]; pr.Base.Ref != "main" {
		t.Fatalf("PR T1 vers %s", pr.Base.Ref)
	}

	var ue *engine.UserError
	if _, err := ev.e.SetFeatureIntegration(ctx, f.ID, "branch", "admin"); !errors.As(err, &ue) || ue.Status != 422 {
		t.Fatalf("mode inconnu : %v", err)
	}
	fe, err := ev.e.SetFeatureIntegration(ctx, f.ID, domain.IntegrationFeatureBranch, "admin")
	if err != nil {
		t.Fatal(err)
	}
	branch := domain.FeatureBranchName(f.ID, f.Title)
	if fe.FeatureBranch == nil || *fe.FeatureBranch != branch {
		t.Fatalf("branche : %v", fe.FeatureBranch)
	}
	ev.drain()
	expect(t, "branche créée depuis main", ev.branchSHA(branch), mainSHA)
	if len(ev.g.retargets) != 1 || ev.g.retargets[0] != "#42→"+branch {
		t.Fatalf("redirections : %v", ev.g.retargets)
	}
	if !hasMsg(ev.n.msgs, "PR redirigée(s) vers "+branch+" : #42") {
		t.Fatalf("notification : %v", ev.n.msgs)
	}
	// Idempotent : même mode, aucun nouveau job.
	if _, err := ev.e.SetFeatureIntegration(ctx, f.ID, domain.IntegrationFeatureBranch, "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	expect(t, "une seule redirection", len(ev.g.retargets), 1)
	// Merge de T1 dans la branche de feature : le mode est verrouillé.
	ev.g.mergePR(t, 42)
	if err := ev.e.ReconcilePR(ctx, t1.ID); err != nil {
		t.Fatal(err)
	}
	expect(t, "main inchangé", ev.branchSHA("main"), mainSHA)
	if _, err := ev.e.SetFeatureIntegration(ctx, f.ID, domain.IntegrationDirect, "admin"); !errors.As(err, &ue) || ue.Status != 409 {
		t.Fatalf("retour au mode direct après merge : %v", err)
	}
}

func TestSwitchBeforeGateUpdatesDevGateApproval(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	ev.design = designTwo
	p := ev.project()
	f, _ := store.CreateFeatureWithMode(ctx, ev.s.Pool, p.ID, "Export CSV", "", domain.IntegrationDirect)
	if _, err := ev.e.FeatureAction(ctx, f.ID, "start_spec", "admin"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	ev.decide(domain.ApprovalSpecReview, "approved", "")
	ev.drain()
	ev.decide(domain.ApprovalDesignReview, "approved", "")
	ev.drain()
	if ap := ev.pending(domain.ApprovalDevGate); !strings.Contains(ap.Effect, "Intégration : directe") {
		t.Fatalf("effet initial : %s", ap.Effect)
	}
	if _, err := ev.e.SetFeatureIntegration(ctx, f.ID, domain.IntegrationFeatureBranch, "admin"); err != nil {
		t.Fatal(err)
	}
	branch := domain.FeatureBranchName(f.ID, f.Title)
	if ap := ev.pending(domain.ApprovalDevGate); !strings.Contains(ap.Effect, "branche de feature "+branch) {
		t.Fatalf("effet mis à jour : %s", ap.Effect)
	}
	fe := ev.feature(f.ID)
	if fe.FeatureBranch != nil {
		t.Fatal("avant le feu vert, la branche n'est pas encore fixée")
	}
	expect(t, "aucun job de bascule avant le feu vert", ev.jobCount(domain.JobSwitchIntegration), 0)
	ev.decide(domain.ApprovalDevGate, "approved", "")
	ev.drain()
	fe = ev.feature(f.ID)
	if fe.FeatureBranch == nil || *fe.FeatureBranch != branch {
		t.Fatalf("branche fixée au feu vert : %v", fe.FeatureBranch)
	}
	if _, err := exec.Command("/usr/bin/git", "--git-dir", ev.g.bare, "rev-parse", "refs/heads/"+branch).Output(); err != nil {
		t.Fatal("branche de feature absente après le premier développement")
	}
}

func (ev *env) jobCount(kind string) int {
	var n int
	if err := ev.s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE kind=$1`, kind).Scan(&n); err != nil {
		ev.t.Fatal(err)
	}
	return n
}
