package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Discussion avec les agents (1.12) : depuis la page d'une feature, l'humain envoie
//   - une consigne : transmise immédiatement à l'agent en cours (steer Hermes, sans redémarrage),
//     sinon gardée et ajoutée aux instructions du prochain run de la feature ;
//   - une question à un rôle : l'agent répond dans sa dernière session Hermes sur la feature (tout
//     son contexte), sans rien modifier ; la réponse arrive dans le fil.
// Rien ne contourne une porte humaine : une consigne ou une réponse ne change aucun état.

// AuditMessage* : actions d'audit de la discussion.
const (
	AuditInstruction    = "message.instruction"
	AuditQuestion       = "message.question"
	AuditAnswer         = "message.answer"
	AuditQuestionEffect = "message.question_side_effect"
)

func featureOpenForChat(f *store.Feature) error {
	switch domain.FeatureState(f.State) {
	case domain.FeatureDone, domain.FeatureCancelled:
		return userErr(409, "chat_closed", "la feature est terminée (%s)", f.State)
	}
	return nil
}

func cleanMessage(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", userErr(422, "invalid_message", "message vide")
	}
	if utf8.RuneCountInString(body) > domain.MaxMessageLen {
		return "", userErr(422, "invalid_message", "message trop long (%d caractères au plus)", domain.MaxMessageLen)
	}
	return body, nil
}

func validChatRole(role string, required bool) error {
	if role == "" && !required {
		return nil
	}
	if !slices.Contains(domain.Roles, role) {
		return userErr(422, "invalid_role", "destinataire inconnu (po, tech_lead, developer, reviewer, qa)")
	}
	return nil
}

// steerText : la consigne telle que l'agent la reçoit.
func steerText(login, body string) string {
	return fmt.Sprintf("Consigne de %s (humain responsable de la feature) : %s", login, body)
}

// PostInstruction enregistre une consigne et la transmet à l'agent en cours si possible.
// role vide = l'agent qui travaille sur la feature, quel que soit son rôle.
func (e *Engine) PostInstruction(ctx context.Context, featureID int64, role, body, login string) (*store.FeatureMessage, error) {
	e.Init()
	body, err := cleanMessage(body)
	if err != nil {
		return nil, err
	}
	if err := validChatRole(role, false); err != nil {
		return nil, err
	}
	f, err := store.GetFeature(ctx, e.Store.Pool, featureID)
	if err != nil {
		return nil, err
	}
	if err := featureOpenForChat(f); err != nil {
		return nil, err
	}
	status, note := domain.MessagePending, ""
	var runID *int64
	active, err := store.ActiveFeatureRun(ctx, e.Store.Pool, featureID)
	if err != nil {
		return nil, err
	}
	if active != nil && (role == "" || active.AgentRole == role) {
		switch serr := e.Hermes.Steer(ctx, active.HermesProfile, *active.HermesRunID, steerText(login, body)); {
		case serr == nil:
			status, runID = domain.MessageDelivered, &active.ID
		case errors.Is(serr, hermes.ErrSteerRefused):
			note = "l'agent venait de terminer : consigne gardée pour le prochain passage"
		default:
			note = "Hermes injoignable : consigne gardée pour le prochain passage"
			e.Log.Warn("consigne non transmise", "feature_id", featureID, "run_id", active.ID, "error", serr)
		}
	}
	var msg *store.FeatureMessage
	err = e.Store.InTx(ctx, func(tx pgx.Tx) error {
		msg, err = store.InsertMessage(ctx, tx, store.NewMessage{FeatureID: featureID, Kind: domain.MessageInstruction,
			Author: audit.User(login), TargetRole: role, Body: body, Status: status, RunID: runID})
		if err != nil {
			return err
		}
		if note != "" {
			if err := store.SetMessageStatus(ctx, tx, msg.ID, status, nil, note); err != nil {
				return err
			}
			msg.Error = &note
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.User(login), Action: AuditInstruction, SubjectType: "feature",
			SubjectID: featureID, RunID: runID, After: map[string]any{"message_id": msg.ID, "status": status, "role": role}}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "feature.message", featureID)
	})
	return msg, err
}

// AskQuestion enregistre une question à un rôle et programme sa réponse (job answer_question).
func (e *Engine) AskQuestion(ctx context.Context, featureID int64, role, body, login string) (*store.FeatureMessage, error) {
	e.Init()
	body, err := cleanMessage(body)
	if err != nil {
		return nil, err
	}
	if err := validChatRole(role, true); err != nil {
		return nil, err
	}
	var msg *store.FeatureMessage
	err = e.Store.InTx(ctx, func(tx pgx.Tx) error {
		f, err := store.LockFeature(ctx, tx, featureID)
		if err != nil {
			return err
		}
		if err := featureOpenForChat(f); err != nil {
			return err
		}
		busy, err := store.RoleRunning(ctx, tx, featureID, role)
		if err != nil {
			return err
		}
		if busy {
			return userErr(409, "agent_busy", "cet agent travaille en ce moment sur la feature : envoie-lui plutôt une consigne")
		}
		msg, err = store.InsertMessage(ctx, tx, store.NewMessage{FeatureID: featureID, Kind: domain.MessageQuestion,
			Author: audit.User(login), TargetRole: role, Body: body, Status: domain.MessageAnswering})
		if err != nil {
			return err
		}
		pid := f.ProjectID
		if _, _, err := store.EnqueueJob(ctx, tx, store.NewJob{Kind: domain.JobAnswerQuestion, SubjectType: "feature", SubjectID: featureID,
			ProjectID: &pid, IdempotencyKey: fmt.Sprintf("question-%d", msg.ID), Payload: map[string]any{"message_id": msg.ID},
			MaxAttempts: 2}); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.User(login), Action: AuditQuestion, SubjectType: "feature",
			SubjectID: featureID, After: map[string]any{"message_id": msg.ID, "role": role}}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "feature.message", featureID)
	})
	return msg, err
}

type questionPayload struct {
	MessageID int64 `json:"message_id"`
}

// branchHeads relit les SHA distants des branches de la feature (tickets et branche de feature) :
// comparés avant/après une question pour détecter un agent qui aurait poussé.
func (e *Engine) branchHeads(ctx context.Context, p *store.Project, f *store.Feature) map[string]string {
	out := map[string]string{}
	branches := []string{}
	if b := str(f.FeatureBranch); b != "" {
		branches = append(branches, b)
	}
	if tickets, err := store.ListTicketsByFeature(ctx, e.Store.Pool, f.ID); err == nil {
		for _, t := range tickets {
			if b := str(t.Branch); b != "" {
				branches = append(branches, b)
			}
		}
	}
	for _, b := range branches {
		if sha, err := e.GitHub.BranchSHA(ctx, p.Repository, b); err == nil {
			out[b] = sha
		}
	}
	return out
}

// roleName : nom du rôle sans article (« PO », « développeur »…).
func roleName(role string) string {
	switch role {
	case domain.RolePO:
		return "PO"
	case domain.RoleTechLead:
		return "tech lead"
	case domain.RoleDeveloper:
		return "développeur"
	case domain.RoleReviewer:
		return "reviewer"
	case domain.RoleQA:
		return "QA"
	}
	return role
}

// questionPrompt : consignes d'une question. Avec une session reprise, l'agent a déjà tout son
// contexte ; sinon il reçoit celui de la feature.
func questionPrompt(p *store.Project, f *store.Feature, role, login, question string, resumed bool) (string, string) {
	instr := `Tu réponds à une question de ` + login + `, l'humain responsable de la feature, dans le fil de discussion
d'Agentic OS. C'est une QUESTION, pas une tâche :
- Réponds en français, directement et précisément, en t'appuyant sur ce que tu as fait ou lu. Dis-le quand tu ne sais pas.
- Ne modifie AUCUN fichier, ne fais aucun commit, aucun push, aucune action externe. Lecture seule : tu peux relire
  un fichier ou un diff si c'est indispensable pour répondre, sans lancer de commande longue.
- Pas de livrable JSON : seulement ta réponse, en texte (Markdown simple), en moins de 300 mots si possible.
- Ne lis ni n'affiche aucun secret.`
	var b strings.Builder
	if !resumed {
		fmt.Fprintf(&b, "Tu es le %s d'Agentic OS sur le projet %s (%s), branche de base %s. Tu n'as pas encore de session sur cette feature :\nvoici son contexte.\n\n",
			roleName(role), p.Name, p.Repository, p.BaseBranch)
		b.WriteString(untrusted("feature", "Titre : "+f.Title+"\nÉtat : "+f.State+"\n\n"+f.Description))
		if len(f.Spec) > 0 {
			b.WriteString("\n\nSpécification :\n" + untrusted("spec", string(f.Spec)))
		}
		if len(f.Design) > 0 {
			b.WriteString("\n\nConception technique :\n" + untrusted("design", string(f.Design)))
		}
		b.WriteString("\n\n")
	}
	b.WriteString("Question de " + login + " :\n" + question)
	return instr, b.String()
}

func (e *Engine) handleAnswerQuestion(ctx context.Context, j *store.Job, owner string) error {
	var pl questionPayload
	_ = jsonUnmarshal(j.Payload, &pl)
	q, err := store.GetMessage(ctx, e.Store.Pool, pl.MessageID)
	if err != nil {
		return err
	}
	if q.Kind != domain.MessageQuestion || q.Status != domain.MessageAnswering || q.FeatureID != j.SubjectID {
		return e.skip(ctx, j, owner, "question déjà traitée")
	}
	role := str(q.TargetRole)
	f, err := store.GetFeature(ctx, e.Store.Pool, q.FeatureID)
	if err != nil {
		return err
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	// Session à reprendre : la dernière du rôle sur la feature (relue chez Hermes pour les runs
	// antérieurs à la 1.12, qui ne l'enregistraient pas).
	session := ""
	if prev, err := store.LatestRoleRun(ctx, e.Store.Pool, f.ID, role); err != nil {
		return err
	} else if prev != nil {
		session = str(prev.HermesSessionID)
		if session == "" {
			if st, err := e.Hermes.GetRun(ctx, prev.HermesProfile, *prev.HermesRunID); err == nil {
				session = st.SessionID
			}
		}
	}
	login := strings.TrimPrefix(q.Author, "user:")
	instr, input := questionPrompt(p, f, role, login, q.Body, session != "")
	before := e.branchHeads(ctx, p, f)
	o, err := e.runAgent(ctx, j, p, role, 0, hermes.RunRequest{Input: input, Instructions: instr, SessionID: session})
	if err != nil {
		return err // failQuestion marque la question en échec quand le job abandonne
	}
	answer := strings.TrimSpace(o.Status.Output)
	if o.Status.NormalizedStatus() != domain.RunCompleted || answer == "" {
		reason := "l'agent n'a pas répondu"
		if o.Status.Error != "" {
			reason = "l'agent n'a pas pu répondre : " + truncate(o.Status.Error, 300)
		}
		return e.finish(ctx, j, owner, func(tx pgx.Tx) error {
			if err := store.SetMessageStatus(ctx, tx, q.ID, domain.MessageFailed, &o.Run.ID, reason); err != nil {
				return err
			}
			return store.Notify(ctx, tx, "feature.message", f.ID)
		})
	}
	if utf8.RuneCountInString(answer) > domain.MaxAnswerLen {
		answer = string([]rune(answer)[:domain.MaxAnswerLen]) + "\n\n… (réponse tronquée, texte complet sur la page du run)"
	}
	// Garde-fou lecture seule : une branche de la feature qui a bougé pendant la question est signalée.
	moved := []string{}
	for b, sha := range e.branchHeads(ctx, p, f) {
		if old, ok := before[b]; ok && old != sha {
			moved = append(moved, b)
		}
	}
	slices.Sort(moved)
	warn := ""
	if len(moved) > 0 {
		warn = "⚠️ la branche " + strings.Join(moved, ", ") + " a changé pendant la question : vérifie ce que l'agent a poussé"
	}
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		if _, err := store.InsertMessage(ctx, tx, store.NewMessage{FeatureID: f.ID, Kind: domain.MessageAnswer,
			Author: audit.Agent(role), TargetRole: role, Body: answer, Status: domain.MessageAnswered, ReplyTo: &q.ID,
			RunID: &o.Run.ID}); err != nil {
			return err
		}
		if err := store.SetMessageStatus(ctx, tx, q.ID, domain.MessageAnswered, &o.Run.ID, warn); err != nil {
			return err
		}
		after := map[string]any{"message_id": q.ID, "role": role, "resumed_session": session != "",
			"fresh_tokens": freshTokens(o.Status.Usage)}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.Agent(role), Action: AuditAnswer, SubjectType: "feature",
			SubjectID: f.ID, RunID: &o.Run.ID, After: after}); err != nil {
			return err
		}
		if len(moved) > 0 {
			if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: AuditQuestionEffect, SubjectType: "feature",
				SubjectID: f.ID, RunID: &o.Run.ID, After: map[string]any{"message_id": q.ID, "branches": moved}}); err != nil {
				return err
			}
		}
		return store.Notify(ctx, tx, "feature.message", f.ID)
	})
	if err == nil {
		text := fmt.Sprintf("💬 F%d « %s » — réponse du %s : %s", f.ID, shorten(f.Title, 60), roleName(role), shorten(answer, 400))
		if warn != "" {
			text += "\n" + warn
		}
		e.notifyProject(ctx, f.ProjectID, text)
	}
	return err
}

// humanInstructions renvoie la section des consignes humaines à ajouter aux instructions d'un run et
// les rattache au run. includePending=false (re-soumission d'un run existant) : seules les consignes
// déjà rattachées à ce run, pour garder un corps identique sous la même clé d'idempotence.
func (e *Engine) humanInstructions(ctx context.Context, j *store.Job, run *store.Run, role string, includePending bool) (string, error) {
	if j.Kind == domain.JobAnswerQuestion {
		return "", nil
	}
	featureID, err := store.FeatureOfSubject(ctx, e.Store.Pool, j.SubjectType, j.SubjectID)
	if err != nil || featureID == 0 {
		return "", nil //nolint:nilerr // sujet sans feature : aucune consigne
	}
	msgs, err := store.InstructionsForRun(ctx, e.Store.Pool, featureID, run.ID, role)
	if err != nil {
		return "", err
	}
	var ids []int64
	var b strings.Builder
	for _, m := range msgs {
		if m.Status == domain.MessagePending && !includePending {
			continue
		}
		if m.Status == domain.MessagePending {
			ids = append(ids, m.ID)
		}
		fmt.Fprintf(&b, "- %s\n", m.Body)
	}
	if b.Len() == 0 {
		return "", nil
	}
	if err := store.ConsumeInstructions(ctx, e.Store.Pool, ids, run.ID); err != nil {
		return "", err
	}
	if len(ids) > 0 {
		_ = store.Notify(ctx, e.Store.Pool, "feature.message", featureID)
	}
	return "\n\nConsignes de l'humain responsable de la feature (à appliquer dans ta tâche, sans enfreindre les règles\n" +
		"permanentes ni changer le livrable demandé) :\n" + b.String(), nil
}

// failQuestion termine en échec un job de question : la question passe en « failed », la feature
// n'est jamais bloquée.
func (e *Engine) failQuestion(ctx context.Context, j *store.Job, owner string, je *JobError) error {
	var pl questionPayload
	_ = jsonUnmarshal(j.Payload, &pl)
	return e.Store.InTx(ctx, func(tx pgx.Tx) error {
		if err := store.FailJob(ctx, tx, j.ID, owner, je.Class, je.Msg); err != nil {
			return err
		}
		if err := store.Notify(ctx, tx, "job.updated", j.ID); err != nil {
			return err
		}
		if pl.MessageID > 0 {
			if err := store.SetMessageStatus(ctx, tx, pl.MessageID, domain.MessageFailed, nil, "l'agent n'a pas pu répondre : "+truncate(je.Msg, 300)); err != nil {
				return err
			}
		}
		return store.Notify(ctx, tx, "feature.message", j.SubjectID)
	})
}
