package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/engine"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
)

func (ev *env) messages(featureID int64) []store.FeatureMessage {
	ev.t.Helper()
	ms, err := store.ListMessages(context.Background(), ev.s.Pool, featureID)
	if err != nil {
		ev.t.Fatal(err)
	}
	return ms
}

// reqWith renvoie la requête Hermes dont l'entrée contient s.
func (ev *env) reqWith(s string) (hermes.RunRequest, bool) {
	ev.h.mu.Lock()
	defer ev.h.mu.Unlock()
	for _, r := range ev.h.reqs {
		if strings.Contains(r.Input, s) {
			return r, true
		}
	}
	return hermes.RunRequest{}, false
}

func TestChatInstructionSteersRunningAgentOrWaitsForNextRun(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, run := ev.runningPOJob()

	// Agent en cours (PO) : consigne transmise immédiatement.
	m, err := ev.e.PostInstruction(ctx, f.ID, "", "  Garde la spec courte.  ", "lilian")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "transmise", m.Status, domain.MessageDelivered)
	if m.RunID == nil || *m.RunID != run.ID {
		t.Fatalf("run de la consigne : %v", m.RunID)
	}
	if len(ev.h.steers) != 1 || !strings.Contains(ev.h.steers[0], "Consigne de lilian") || !strings.Contains(ev.h.steers[0], "Garde la spec courte.") {
		t.Fatalf("steer : %v", ev.h.steers)
	}
	// Adressée à un autre rôle que l'agent en cours : gardée.
	m2, err := ev.e.PostInstruction(ctx, f.ID, domain.RoleDeveloper, "Ajoute un test.", "lilian")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "gardée pour le développeur", m2.Status, domain.MessagePending)
	// Run terminé chez Hermes : steer refusé, consigne gardée avec une note.
	ev.h.mu.Lock()
	ev.h.runs["run_live"] = hermes.RunStatus{Status: "completed"}
	ev.h.mu.Unlock()
	m3, err := ev.e.PostInstruction(ctx, f.ID, "", "Trop tard ?", "lilian")
	if err != nil {
		t.Fatal(err)
	}
	if m3.Status != domain.MessagePending || m3.Error == nil || !strings.Contains(*m3.Error, "prochain passage") {
		t.Fatalf("consigne après fin : %+v", m3)
	}
	// Validation.
	var ue *engine.UserError
	if _, err := ev.e.PostInstruction(ctx, f.ID, "", "   ", "lilian"); !errors.As(err, &ue) || ue.Status != 422 {
		t.Fatalf("vide : %v", err)
	}
	if _, err := ev.e.PostInstruction(ctx, f.ID, "chef", "x", "lilian"); !errors.As(err, &ue) || ue.Status != 422 {
		t.Fatalf("rôle inconnu : %v", err)
	}
	if _, err := ev.e.PostInstruction(ctx, f.ID, "", strings.Repeat("é", domain.MaxMessageLen+1), "lilian"); !errors.As(err, &ue) || ue.Status != 422 {
		t.Fatalf("trop long : %v", err)
	}
	if _, err := ev.e.AskQuestion(ctx, f.ID, domain.RolePO, "Où en es-tu ?", "lilian"); !errors.As(err, &ue) || ue.Code != "agent_busy" {
		t.Fatalf("question à un agent au travail : %v", err)
	}
}

func TestChatPendingInstructionJoinsNextRunOfItsRole(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev() // feu vert donné, développement pas encore lancé
	m, err := ev.e.PostInstruction(ctx, f.ID, domain.RoleDeveloper, "Nomme le fichier hello.txt en minuscules.", "lilian")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "aucun agent en cours", m.Status, domain.MessagePending)
	ev.drain()
	dev, ok := ev.reqWith("Worktree : ")
	if !ok || !strings.Contains(dev.Instructions, "Consignes de l'humain") || !strings.Contains(dev.Instructions, "hello.txt en minuscules") {
		t.Fatalf("consigne absente des instructions du développeur : %q", dev.Instructions)
	}
	got := ev.messages(f.ID)[0]
	if got.Status != domain.MessageConsumed || got.RunID == nil {
		t.Fatalf("consigne rattachée au run : %+v", got)
	}
	// Une seule fois : le reviewer et le QA ne la reçoivent pas.
	ev.h.mu.Lock()
	n := 0
	for _, r := range ev.h.reqs {
		if strings.Contains(r.Instructions, "hello.txt en minuscules") {
			n++
		}
	}
	ev.h.mu.Unlock()
	expect(t, "consigne jointe à un seul run", n, 1)
}

func TestChatQuestionResumesRoleSessionAndAnswers(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	ev.telegramChat = "-100"
	f, _ := ev.startToDev()
	ev.drain()
	expect(t, "ticket prêt", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")

	q, err := ev.e.AskQuestion(ctx, f.ID, domain.RoleDeveloper, "Pourquoi un fichier texte ?", "lilian")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "en cours de réponse", q.Status, domain.MessageAnswering)
	ev.drain()

	req, ok := ev.reqWith("Pourquoi un fichier texte ?")
	if !ok {
		t.Fatal("question non envoyée à Hermes")
	}
	if !strings.HasPrefix(req.SessionID, "sess_run_") {
		t.Fatalf("la question doit reprendre la session du développeur : %q", req.SessionID)
	}
	if !strings.Contains(req.Instructions, "Ne modifie AUCUN fichier") || strings.Contains(req.Input, "pas encore de session") {
		t.Fatalf("consignes de question : %q / %q", req.Instructions, req.Input)
	}
	ms := ev.messages(f.ID)
	if len(ms) != 2 {
		t.Fatalf("question + réponse attendues : %+v", ms)
	}
	expect(t, "question répondue", ms[0].Status, domain.MessageAnswered)
	a := ms[1]
	if a.Kind != domain.MessageAnswer || a.ReplyTo == nil || *a.ReplyTo != q.ID || a.Author != "agent:developer" || !strings.Contains(a.Body, "c'est fait") {
		t.Fatalf("réponse : %+v", a)
	}
	if !hasMsg(ev.n.msgs, "réponse du développeur") {
		t.Fatalf("notification : %v", ev.n.msgs)
	}
	// La réponse n'a changé aucun état.
	expect(t, "ticket inchangé", ev.ticket(f.ID).State, "AWAITING_PR_APPROVAL")
	expect(t, "feature inchangée", ev.feature(f.ID).State, "IN_DEV")
}

func TestChatQuestionWithoutSessionGetsFeatureContext(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	f, err := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Export CSV", "Exporter les frais en CSV")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ev.e.AskQuestion(ctx, f.ID, domain.RolePO, "C'est faisable en une semaine ?", "lilian"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	req, ok := ev.reqWith("faisable en une semaine")
	if !ok || req.SessionID != "" || !strings.Contains(req.Input, "pas encore de session") || !strings.Contains(req.Input, "Exporter les frais en CSV") {
		t.Fatalf("question sans session : %+v", req)
	}
	expect(t, "toujours une idée", ev.feature(f.ID).State, "IDEA")
}

func TestChatFailedAnswerNeverBlocksFeatureAndPushIsFlagged(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	f, _ := ev.startToDev()
	ev.drain()
	tk := ev.ticket(f.ID)

	// Réponse vide : la question échoue, la feature continue.
	ev.answer = func(string, hermes.RunRequest) string { return "" }
	if _, err := ev.e.AskQuestion(ctx, f.ID, domain.RoleReviewer, "Tu as tout relu ?", "lilian"); err != nil {
		t.Fatal(err)
	}
	ev.drain()
	q := ev.messages(f.ID)[0]
	if q.Status != domain.MessageFailed || q.Error == nil {
		t.Fatalf("question en échec : %+v", q)
	}
	expect(t, "feature non bloquée", ev.feature(f.ID).State, "IN_DEV")

	// L'agent pousse pendant une question : signalé dans la réponse et l'audit.
	ev.answer = func(string, hermes.RunRequest) string {
		gitRun(t, ev.root, "--git-dir", ev.g.bare, "update-ref", "refs/heads/"+*tk.Branch, ev.branchSHA("main"))
		return "J'ai corrigé au passage."
	}
	q2, err := ev.e.AskQuestion(ctx, f.ID, domain.RoleDeveloper, "Une idée ?", "lilian")
	if err != nil {
		t.Fatal(err)
	}
	ev.drain()
	got, _ := store.GetMessage(ctx, ev.s.Pool, q2.ID)
	if got.Status != domain.MessageAnswered || got.Error == nil || !strings.Contains(*got.Error, *tk.Branch) {
		t.Fatalf("poussée non signalée : %+v", got)
	}
	var n int
	_ = ev.s.Pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action=$1`, engine.AuditQuestionEffect).Scan(&n)
	expect(t, "audit de la poussée", n, 1)
}

func TestChatClosedOnFinishedFeature(t *testing.T) {
	ev := newEnv(t)
	ctx := context.Background()
	p := ev.project()
	f, _ := store.CreateFeature(ctx, ev.s.Pool, p.ID, "Vieille", "")
	if _, err := ev.e.FeatureAction(ctx, f.ID, "cancel", "admin"); err != nil {
		t.Fatal(err)
	}
	var ue *engine.UserError
	if _, err := ev.e.AskQuestion(ctx, f.ID, domain.RolePO, "Encore là ?", "lilian"); !errors.As(err, &ue) || ue.Code != "chat_closed" {
		t.Fatalf("feature annulée : %v", err)
	}
}
