package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/engine"
	"github.com/zeggriim2/agentic-os/internal/store"
)

func TestAcceptanceCorrectionUpdatesOpenFeaturePR(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f := ev.toAcceptance()
	ev.decide(domain.ApprovalPublishFeaturePR, "approved", "")
	ev.drain()
	fe := ev.feature(f.ID)
	branch := *fe.FeatureBranch
	prN := *fe.FeaturePRNumber
	before := ev.branchSHA(branch)

	// Validation et état.
	var ue *engine.UserError
	if _, err := ev.e.RequestCorrection(ctx, f.ID, engine.CorrectionRequest{Title: "Récapitulatif", Description: "  "}, "admin"); !errors.As(err, &ue) || ue.Status != 422 {
		t.Fatalf("description vide : %v", err)
	}
	tk, err := ev.e.RequestCorrection(ctx, f.ID, engine.CorrectionRequest{Title: "Récapitulatif : forfait 10 % absent",
		Description: "Sur /summary, le bloc forfait 10 % ne s'affiche pas quand la configuration fiscale est complète.",
		Criteria:    []string{"hello.txt existe", " "}}, "lilian")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "ticket de correction", tk.Origin, "correction")
	expect(t, "numéro suivant", tk.Seq, 3)
	if !strings.HasSuffix(tk.Key, "-T3") || !strings.Contains(string(tk.Acceptance), "hello.txt existe") || strings.Contains(string(tk.Acceptance), `" "`) {
		t.Fatalf("ticket : %s %s", tk.Key, tk.Acceptance)
	}
	expect(t, "feature repart en dev", ev.feature(f.ID).State, "IN_DEV")
	if _, err := ev.e.RequestCorrection(ctx, f.ID, engine.CorrectionRequest{Title: "x", Description: "y"}, "admin"); !errors.As(err, &ue) || ue.Status != 409 {
		t.Fatalf("correction hors recette : %v", err)
	}

	// Issue, développement depuis la branche de feature, PR vers elle, merge humain.
	ev.drain()
	ct := ev.ticketByID(tk.ID)
	if ct.IssueNumber == nil {
		t.Fatal("issue du ticket de correction absente")
	}
	expect(t, "correction validée", ct.State, "AWAITING_PR_APPROVAL")
	if !ev.isAncestor(before, *ct.HeadSHA) {
		t.Fatal("la correction doit partir de la branche de feature")
	}
	ev.publishAndMerge(tk.ID)
	if pr := ev.g.prs["aos/"+tk.Key]; pr.Base.Ref != branch {
		t.Fatalf("PR de correction vers %s", pr.Base.Ref)
	}

	// Retour en recette : la PR finale existante est mise à jour, aucune nouvelle approbation.
	expect(t, "retour en recette", ev.feature(f.ID).State, "ACCEPTANCE")
	ev.drain()
	if ap := ev.maybePending(domain.ApprovalPublishFeaturePR); ap != nil {
		t.Fatalf("pas de nouvelle approbation de PR finale : %+v", ap)
	}
	body := ev.g.updates[prN]
	for _, want := range []string{"Closes #1", "Closes #2", fmt.Sprintf("Closes #%d", *ct.IssueNumber), tk.Key} {
		if !strings.Contains(body, want) {
			t.Fatalf("PR finale mise à jour sans %q : %s", want, body)
		}
	}
	expect(t, "une seule PR finale", *ev.feature(f.ID).FeaturePRNumber, prN)
	if !hasMsg(ev.n.msgs, "🔁 Correction demandée") || !hasMsg(ev.n.msgs, "de nouveau en recette") {
		t.Fatalf("notifications : %v", ev.n.msgs)
	}

	// Merge humain de la PR finale : terminée, avec la correction.
	ev.g.mergePR(t, prN)
	if n, err := ev.e.HandleGitHubEvent(ctx, "o/r", prN, "", branch); err != nil || n != 1 {
		t.Fatalf("webhook PR finale : %d %v", n, err)
	}
	expect(t, "feature terminée", ev.feature(f.ID).State, "DONE")
	if !ev.isAncestor(*ev.ticketByID(tk.ID).HeadSHA, ev.branchSHA("main")) {
		t.Fatal("main doit contenir la correction")
	}
}

func TestAcceptanceCorrectionBeforeFeaturePRAndFromBlocked(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f := ev.toAcceptance()
	// PR finale pas encore publiée : son approbation est expirée, puis redemandée au retour.
	old := ev.pending(domain.ApprovalPublishFeaturePR)
	tk, err := ev.e.RequestCorrection(ctx, f.ID, engine.CorrectionRequest{Title: "Libellé", Description: "Le libellé est tronqué.", Criteria: []string{"hello.txt existe"}}, "lilian")
	if err != nil {
		t.Fatal(err)
	}
	if ap := ev.maybePending(domain.ApprovalPublishFeaturePR); ap != nil {
		t.Fatalf("approbation non expirée : %+v", ap)
	}
	ev.drain()
	ev.publishAndMerge(tk.ID)
	ap := ev.pending(domain.ApprovalPublishFeaturePR)
	if ap == nil || ap.ID == old.ID || !strings.Contains(ap.Effect, "#1, #2, #3") {
		t.Fatalf("nouvelle approbation avec la correction : %+v", ap)
	}

	// Refus de la PR finale → bloquée depuis la recette : la correction reste possible.
	ev.decide(domain.ApprovalPublishFeaturePR, "rejected", "un défaut")
	ev.drain()
	expect(t, "bloquée", ev.feature(f.ID).State, "BLOCKED")
	tk2, err := ev.e.RequestCorrection(ctx, f.ID, engine.CorrectionRequest{Title: "Encore", Description: "Autre défaut."}, "lilian")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tk2.Acceptance), "corrigé et vérifié") {
		t.Fatalf("critère par défaut attendu : %s", tk2.Acceptance)
	}
	expect(t, "repart en dev", ev.feature(f.ID).State, "IN_DEV")
}

func TestCorrectionRequiresFeatureBranch(t *testing.T) {
	ev := newEnv(t)
	f, _ := ev.toDevGateTwo()
	var ue *engine.UserError
	if _, err := ev.e.RequestCorrection(context.Background(), f.ID, engine.CorrectionRequest{Title: "x", Description: "y"}, "admin"); !errors.As(err, &ue) || ue.Status != 409 {
		t.Fatalf("hors recette : %v", err)
	}
}

func (ev *env) maybePending(kind string) *store.Approval {
	ev.t.Helper()
	aps, err := store.ListApprovals(context.Background(), ev.s.Pool, store.ApprovalFilter{Status: "pending"}, store.Page{Limit: 50})
	if err != nil {
		ev.t.Fatal(err)
	}
	for i := range aps {
		if aps[i].Kind == kind {
			return &aps[i]
		}
	}
	return nil
}
