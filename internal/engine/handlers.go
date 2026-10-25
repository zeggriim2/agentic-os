package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	gh "github.com/zeggriim2/agentic-os/internal/github"
	"github.com/zeggriim2/agentic-os/internal/hermes"
	"github.com/zeggriim2/agentic-os/internal/store"
)

type jobPayload struct {
	Feedback string `json:"feedback,omitempty"`
}

func payloadOf(j *store.Job) jobPayload {
	var p jobPayload
	_ = json.Unmarshal(j.Payload, &p)
	return p
}

func (e *Engine) handle(ctx context.Context, j *store.Job, owner string) error {
	if j.Kind != domain.JobSyncAgents && j.Kind != domain.JobExecuteApproval {
		p, err := e.projectFor(ctx, e.Store.Pool, j)
		if err != nil {
			return err
		}
		if p != nil && !p.Enabled {
			return permanent(domain.ErrClassPolicy, "projet %s désactivé", p.Slug)
		}
	}
	switch j.Kind {
	case domain.JobPOSpec:
		return e.handlePOSpec(ctx, j, owner)
	case domain.JobTechDesign:
		return e.handleTechDesign(ctx, j, owner)
	case domain.JobOpenIssues:
		return e.handleOpenIssues(ctx, j, owner)
	case domain.JobDevelop:
		return e.handleDevelop(ctx, j, owner)
	case domain.JobReview:
		return e.handleReview(ctx, j, owner)
	case domain.JobQA:
		return e.handleQA(ctx, j, owner)
	case domain.JobPublishPR:
		return e.handlePublishPR(ctx, j, owner)
	case domain.JobExecuteApproval:
		return e.handleExecuteApproval(ctx, j, owner)
	case domain.JobSyncAgents:
		return e.handleSyncAgents(ctx, j, owner)
	}
	return permanent(domain.ErrClassInternal, "type de job inconnu %q", j.Kind)
}

// skip termine un job devenu sans objet (sujet déplacé entre-temps).
func (e *Engine) skip(ctx context.Context, j *store.Job, owner, why string) error {
	e.Log.Info("job sans objet", "job_id", j.ID, "reason", why)
	return e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "job.skipped", SubjectType: j.SubjectType, SubjectID: j.SubjectID,
			After: map[string]any{"job_id": j.ID, "reason": why}})
	})
}

// ------------------------------------------------------------------ po_spec

func (e *Engine) handlePOSpec(ctx context.Context, j *store.Job, owner string) error {
	f, err := store.GetFeature(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if f.State != string(domain.FeatureSpec) {
		return e.skip(ctx, j, owner, "feature hors SPEC")
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	instr, input := poPrompt(p, f, payloadOf(j).Feedback)
	o, err := e.runAgent(ctx, j, domain.RolePO, 0, hermes.RunRequest{Input: input, Instructions: instr})
	if err != nil {
		return err
	}
	art, err := e.evaluate(ctx, o, func(s string) (any, error) { return domain.ParsePOSpec(s) })
	if err != nil {
		return err
	}
	spec := art.(*domain.POSpec)
	var notice string
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		f, err := store.LockFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		if f.State != string(domain.FeatureSpec) {
			return nil
		}
		if err := store.SetFeatureSpec(ctx, tx, f.ID, spec); err != nil {
			return err
		}
		if _, err := e.featureEvent(ctx, tx, f, domain.FeatureEvSpecReady, audit.Agent(domain.RolePO), ""); err != nil {
			return err
		}
		effect := "Valider la spécification et lancer la conception technique (Tech Lead)."
		if len(spec.OpenQuestions) > 0 {
			effect += fmt.Sprintf(" %d question(s) ouverte(s) : rejeter avec un commentaire pour y répondre.", len(spec.OpenQuestions))
		}
		_, err = e.createApproval(ctx, tx, store.NewApproval{Kind: domain.ApprovalSpecReview, SubjectType: "feature", SubjectID: f.ID,
			Title: "Spécification : " + f.Title, Effect: effect,
			Details: map[string]any{"spec": spec, "open_questions": spec.OpenQuestions, "run_id": o.Run.ID}})
		notice = fmt.Sprintf("📝 Spécification prête pour « %s » : approbation requise.", f.Title)
		return err
	})
	if err == nil && notice != "" {
		e.Notifier.Notify(ctx, notice)
	}
	return err
}

// ------------------------------------------------------------------ tech_design

func (e *Engine) handleTechDesign(ctx context.Context, j *store.Job, owner string) error {
	f, err := store.GetFeature(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if f.State != string(domain.FeatureTechDesign) {
		return e.skip(ctx, j, owner, "feature hors TECH_DESIGN")
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	instr, input := techLeadPrompt(p, f, payloadOf(j).Feedback)
	o, err := e.runAgent(ctx, j, domain.RoleTechLead, 0, hermes.RunRequest{Input: input, Instructions: instr})
	if err != nil {
		return err
	}
	art, err := e.evaluate(ctx, o, func(s string) (any, error) { return domain.ParseTechDesign(s) })
	if err != nil {
		return err
	}
	design := art.(*domain.TechDesign)
	var notice string
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		f, err := store.LockFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		if f.State != string(domain.FeatureTechDesign) {
			return nil
		}
		if err := store.SetFeatureDesign(ctx, tx, f.ID, design); err != nil {
			return err
		}
		if _, err := e.featureEvent(ctx, tx, f, domain.FeatureEvDesignReady, audit.Agent(domain.RoleTechLead), ""); err != nil {
			return err
		}
		titles := make([]string, len(design.Tickets))
		for i, t := range design.Tickets {
			titles[i] = t.Title
		}
		_, err = e.createApproval(ctx, tx, store.NewApproval{Kind: domain.ApprovalDesignReview, SubjectType: "feature", SubjectID: f.ID,
			Title:   "Conception : " + f.Title,
			Effect:  fmt.Sprintf("Créer %d issues sur %s (marqueur agentic-os, aucune autre écriture).", len(design.Tickets), p.Repository),
			Details: withProjectSnapshot(p, map[string]any{"design": design, "tickets": titles, "run_id": o.Run.ID})})
		notice = fmt.Sprintf("🧭 Conception prête pour « %s » : approbation requise.", f.Title)
		return err
	})
	if err == nil && notice != "" {
		e.Notifier.Notify(ctx, notice)
	}
	return err
}

// ------------------------------------------------------------------ open_issues

func ticketKey(slug string, featureID int64, n int) string {
	return fmt.Sprintf("%s-F%d-T%d", slug, featureID, n)
}

func issueBody(t domain.DesignTicket, featureTitle string) string {
	var b strings.Builder
	b.WriteString(t.Body)
	b.WriteString("\n\n### Critères d'acceptation\n")
	for _, c := range t.AcceptanceCriteria {
		b.WriteString("- [ ] " + c + "\n")
	}
	b.WriteString("\n_Feature : " + featureTitle + " — géré par Agentic OS._")
	return b.String()
}

func (e *Engine) handleOpenIssues(ctx context.Context, j *store.Job, owner string) error {
	f, err := store.GetFeature(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if f.State != string(domain.FeatureIssuesOpened) {
		return e.skip(ctx, j, owner, "feature hors ISSUES_OPENED")
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	var design domain.TechDesign
	if err := json.Unmarshal(f.Design, &design); err != nil || len(design.Tickets) == 0 {
		return permanent(domain.ErrClassInternal, "conception absente ou illisible")
	}
	for i, dt := range design.Tickets {
		t, err := store.InsertTicket(ctx, e.Store.Pool, store.TicketInput{FeatureID: f.ID, ProjectID: p.ID, Key: ticketKey(p.Slug, f.ID, i+1),
			Seq: i + 1, Title: dt.Title, Body: dt.Body, Acceptance: dt.AcceptanceCriteria})
		if err != nil {
			return err
		}
		if t.IssueNumber != nil {
			continue
		}
		if err := e.fence(ctx, "création d'issue"); err != nil {
			return err
		}
		is, created, err := e.GitHub.EnsureIssue(ctx, p.Repository, t.Key, t.Title, issueBody(dt, f.Title))
		if err != nil {
			return err
		}
		if err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			if err := store.SetTicketIssue(ctx, tx, t.ID, is.Number, is.HTMLURL); err != nil {
				return err
			}
			if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "ticket.issue_linked", SubjectType: "ticket", SubjectID: t.ID,
				After: map[string]any{"issue_number": is.Number, "issue_url": is.HTMLURL, "created": created}}); err != nil {
				return err
			}
			return store.Notify(ctx, tx, "ticket.updated", t.ID)
		}); err != nil {
			return err
		}
	}
	var notice string
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		f, err := store.LockFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		if f.State != string(domain.FeatureIssuesOpened) {
			return nil
		}
		tickets, err := store.ListTicketsByFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		issues := []map[string]any{}
		for _, t := range tickets {
			if t.IssueNumber == nil {
				return permanent(domain.ErrClassExternal, "ticket %s sans issue", t.Key)
			}
			issues = append(issues, map[string]any{"key": t.Key, "title": t.Title, "issue_number": *t.IssueNumber, "issue_url": str(t.IssueURL)})
		}
		if _, err := e.featureEvent(ctx, tx, f, domain.FeatureEvIssuesOpened, audit.System, ""); err != nil {
			return err
		}
		_, err = e.createApproval(ctx, tx, store.NewApproval{Kind: domain.ApprovalDevGate, SubjectType: "feature", SubjectID: f.ID,
			Title: "Feu vert développement : " + f.Title,
			Effect: fmt.Sprintf("Autoriser le développement autonome de %d ticket(s) sur %s : branches aos/<ticket>, commits et push par Agentic OS, sans PR ni merge.",
				len(tickets), p.Repository),
			Details: withProjectSnapshot(p, map[string]any{"tickets": issues})})
		notice = fmt.Sprintf("🚦 Issues créées pour « %s » : feu vert développement requis.", f.Title)
		return err
	})
	if err == nil && notice != "" {
		e.Notifier.Notify(ctx, notice)
	}
	return err
}

// ------------------------------------------------------------------ develop

func branchFor(t *store.Ticket) string { return "aos/" + t.Key }

func (e *Engine) handleDevelop(ctx context.Context, j *store.Job, owner string) error {
	t, err := store.GetTicket(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if t.State == string(domain.TicketReadyForDev) {
		err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			lt, err := store.LockTicket(ctx, tx, t.ID)
			if err != nil {
				return err
			}
			if lt.State != string(domain.TicketReadyForDev) {
				return nil
			}
			if t, err = e.ticketEvent(ctx, tx, lt, domain.TicketEvDevStarted, audit.Agent(domain.RoleDeveloper), ""); err != nil {
				return err
			}
			f, err := store.LockFeature(ctx, tx, lt.FeatureID)
			if err != nil {
				return err
			}
			if f.State == string(domain.FeatureReadyForDev) {
				_, err = e.featureEvent(ctx, tx, f, domain.FeatureEvTicketStarted, audit.System, "")
			}
			return err
		})
		if err != nil {
			return err
		}
		if t, err = store.GetTicket(ctx, e.Store.Pool, j.SubjectID); err != nil {
			return err
		}
	}
	if t.State != string(domain.TicketDev) {
		return e.skip(ctx, j, owner, "ticket hors DEV")
	}
	p, err := store.GetProject(ctx, e.Store.Pool, t.ProjectID)
	if err != nil {
		return err
	}
	branch := branchFor(t)
	wt, err := e.Git.EnsureBranchWorktree(ctx, p.Slug, p.Repository, t.Key, branch, p.BaseBranch)
	if err != nil {
		return err
	}
	baseRef := "refs/remotes/origin/" + p.BaseBranch
	baseSHA, err := e.Git.RevParse(ctx, wt, baseRef)
	if err != nil {
		return err
	}
	run, err := store.LatestRunForJob(ctx, e.Store.Pool, j.ID)
	if err != nil {
		return err
	}
	if run == nil || run.HermesRunID == nil {
		// avant soumission : le worktree doit être propre
		if err := e.Git.EnsureClean(ctx, wt); err != nil {
			return permanent(domain.ErrClassPolicy, "worktree non propre avant le run : %v", err)
		}
	}
	// La branche distante ne doit contenir que ce qu'Agentic OS y a poussé.
	if err := e.checkRemoteBranch(ctx, p, t, wt, branch); err != nil {
		return err
	}
	instr, input := developerPrompt(p, t, wt, branch, payloadOf(j).Feedback)
	o, err := e.runAgent(ctx, j, domain.RoleDeveloper, t.Attempt, hermes.RunRequest{Input: input, Instructions: instr})
	if err != nil {
		return err
	}
	if _, err := e.evaluate(ctx, o, func(s string) (any, error) { return domain.ParseDevelop(s) }); err != nil {
		return err
	}
	// Vérifications fail-closed avant push.
	if err := e.Git.EnsureClean(ctx, wt); err != nil {
		return permanent(domain.ErrClassPolicy, "%v", err)
	}
	if cur, err := e.Git.CurrentBranch(ctx, wt); err != nil || cur != branch {
		return permanent(domain.ErrClassPolicy, "le worktree n'est plus sur la branche %s", branch)
	}
	head, err := e.Git.HeadSHA(ctx, wt)
	if err != nil {
		return err
	}
	if head == baseSHA {
		return permanent(domain.ErrClassPolicy, "aucun commit par rapport à la base")
	}
	if t.HeadSHA != nil && head == *t.HeadSHA {
		return permanent(domain.ErrClassPolicy, "aucun nouveau commit depuis le dernier push")
	}
	deleted, err := e.Git.DeletedTestFiles(ctx, wt, baseRef)
	if err != nil {
		return err
	}
	var forbidden []string
	for _, d := range deleted {
		if !strings.Contains(t.Body, d) {
			forbidden = append(forbidden, d)
		}
	}
	if len(forbidden) > 0 {
		return permanent(domain.ErrClassPolicy, "suppression de tests non autorisée : %s", strings.Join(forbidden, ", "))
	}
	if err := e.checkRemoteBranch(ctx, p, t, wt, branch); err != nil {
		return err
	}
	if err := e.fence(ctx, "push Git"); err != nil {
		return err
	}
	if err := e.Git.Push(ctx, wt, branch); err != nil {
		return err
	}
	remote, err := e.GitHub.BranchSHA(ctx, p.Repository, branch)
	if err != nil {
		return err
	}
	if remote != head {
		return retryable(domain.ErrClassExternal, "SHA distant %s différent du SHA poussé %s", short(remote), short(head))
	}
	return e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		lt, err := store.LockTicket(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if lt.State != string(domain.TicketDev) {
			return nil
		}
		if err := store.SetTicketHead(ctx, tx, t.ID, branch, head); err != nil {
			return err
		}
		lt.HeadSHA = &head
		if _, err := e.setTicket(ctx, tx, lt, domain.TicketReview, domain.TicketEvDevPushed, audit.Agent(domain.RoleDeveloper), "", head); err != nil {
			return err
		}
		_, err = e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobReview, SubjectType: "ticket", SubjectID: t.ID, ProjectID: &t.ProjectID,
			IdempotencyKey: jobKey(domain.JobReview, "ticket", t.ID, fmt.Sprintf("after-job-%d", j.ID))})
		return err
	})
}

// checkRemoteBranch refuse une branche distante modifiée hors d'Agentic OS. Valeurs
// admises : absente si rien n'a encore été poussé, le head_sha vérifié du ticket, ou le
// HEAD local (push déjà effectué par une tentative interrompue avant sa finalisation).
func (e *Engine) checkRemoteBranch(ctx context.Context, p *store.Project, t *store.Ticket, wt, branch string) error {
	expected := str(t.HeadSHA)
	remote, err := e.GitHub.BranchSHA(ctx, p.Repository, branch)
	if errors.Is(err, gh.ErrNotFound) {
		if expected == "" {
			return nil
		}
		return permanent(domain.ErrClassPolicy, "la branche distante %s a disparu (attendu %s)", branch, short(expected))
	}
	if err != nil {
		return err
	}
	if remote == expected {
		return nil
	}
	if local, err := e.Git.HeadSHA(ctx, wt); err == nil && local == remote {
		return nil
	}
	if expected == "" {
		return permanent(domain.ErrClassPolicy, "la branche distante %s existe déjà (%s) sans avoir été poussée par Agentic OS", branch, short(remote))
	}
	return permanent(domain.ErrClassPolicy, "la branche distante %s a été modifiée hors d'Agentic OS (%s au lieu de %s)", branch, short(remote), short(expected))
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ------------------------------------------------------------------ review / qa

// verifiedWorktree vérifie le SHA distant puis prépare un worktree détaché.
func (e *Engine) verifiedWorktree(ctx context.Context, p *store.Project, t *store.Ticket, suffix string) (string, string, error) {
	if t.HeadSHA == nil || t.Branch == nil || !domain.ValidSHA(*t.HeadSHA) {
		return "", "", permanent(domain.ErrClassPolicy, "head_sha absent")
	}
	sha := *t.HeadSHA
	remote, err := e.GitHub.BranchSHA(ctx, p.Repository, *t.Branch)
	if err != nil {
		return "", "", err
	}
	if remote != sha {
		return "", "", permanent(domain.ErrClassPolicy, "le SHA distant (%s) ne correspond plus au head_sha (%s)", short(remote), short(sha))
	}
	wt, err := e.Git.EnsureDetachedWorktree(ctx, p.Slug, p.Repository, t.Key+"-"+suffix, *t.Branch, sha)
	if err != nil {
		return "", "", err
	}
	return wt, sha, nil
}

func (e *Engine) checkWorktreeAfter(ctx context.Context, wt, sha string) error {
	if err := e.Git.EnsureClean(ctx, wt); err != nil {
		return permanent(domain.ErrClassPolicy, "%v", err)
	}
	head, err := e.Git.HeadSHA(ctx, wt)
	if err != nil {
		return err
	}
	if head != sha {
		return permanent(domain.ErrClassPolicy, "le worktree a quitté le SHA vérifié")
	}
	return nil
}

func (e *Engine) loadTicketCtx(ctx context.Context, j *store.Job, state domain.TicketState) (*store.Ticket, *store.Project, bool, error) {
	t, err := store.GetTicket(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return nil, nil, false, err
	}
	if t.State != string(state) {
		return t, nil, false, nil
	}
	p, err := store.GetProject(ctx, e.Store.Pool, t.ProjectID)
	return t, p, true, err
}

func (e *Engine) handleReview(ctx context.Context, j *store.Job, owner string) error {
	t, p, ok, err := e.loadTicketCtx(ctx, j, domain.TicketReview)
	if err != nil {
		return err
	}
	if !ok {
		return e.skip(ctx, j, owner, "ticket hors REVIEW")
	}
	wt, sha, err := e.verifiedWorktree(ctx, p, t, "review")
	if err != nil {
		return err
	}
	instr, input := reviewerPrompt(p, t, wt, sha)
	o, err := e.runAgent(ctx, j, domain.RoleReviewer, t.Attempt, hermes.RunRequest{Input: input, Instructions: instr})
	if err != nil {
		return err
	}
	art, err := e.evaluate(ctx, o, func(s string) (any, error) { return domain.ParseReview(s, sha) })
	if err != nil {
		return err
	}
	if err := e.checkWorktreeAfter(ctx, wt, sha); err != nil {
		return err
	}
	rv := art.(*domain.ReviewResult)
	var notice string
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		lt, err := store.LockTicket(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if lt.State != string(domain.TicketReview) || str(lt.HeadSHA) != sha {
			return nil
		}
		if rv.Verdict == "approved" {
			if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvReviewApproved, audit.Agent(domain.RoleReviewer), rv.Summary); err != nil {
				return err
			}
			_, err = e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobQA, SubjectType: "ticket", SubjectID: t.ID, ProjectID: &t.ProjectID,
				IdempotencyKey: jobKey(domain.JobQA, "ticket", t.ID, fmt.Sprintf("after-job-%d", j.ID))})
			return err
		}
		if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvReviewChanges, audit.Agent(domain.RoleReviewer), rv.Summary); err != nil {
			return err
		}
		notice = fmt.Sprintf("🔁 Revue de %s : corrections demandées, décision de rework requise.", t.Key)
		return e.reworkApproval(ctx, tx, lt, "review", rv.Summary, findingsText(rv.Findings), sha)
	})
	if err == nil && notice != "" {
		e.Notifier.Notify(ctx, notice)
	}
	return err
}

func findingsText(fs []domain.Finding) string {
	var b strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&b, "- [%s] %s : %s\n", f.Severity, f.File, f.Message)
	}
	return b.String()
}

func qaFindings(q *domain.QAResult) string {
	var b strings.Builder
	for _, c := range q.Checks {
		if c.ExitCode != 0 {
			fmt.Fprintf(&b, "- check en échec : %s (exit %d)\n", c.Command, c.ExitCode)
		}
	}
	for _, a := range q.Acceptance {
		if a.Status != "met" {
			fmt.Fprintf(&b, "- critère non satisfait : %s (%s)\n", a.Criterion, a.Evidence)
		}
	}
	if b.Len() == 0 {
		b.WriteString(q.Summary)
	}
	return b.String()
}

func (e *Engine) reworkApproval(ctx context.Context, tx pgx.Tx, t *store.Ticket, source, summary, findings, sha string) error {
	effect := fmt.Sprintf("Relancer le développement de %s avec ces corrections (tentative %d/%d).", t.Key, t.Attempt+1, e.MaxRework)
	if t.Attempt >= e.MaxRework {
		effect = fmt.Sprintf("Nombre maximal de reworks atteint (%d) : approuver bloquera le ticket, rejeter l'annulera.", e.MaxRework)
	}
	_, err := e.createApproval(ctx, tx, store.NewApproval{Kind: domain.ApprovalRework, SubjectType: "ticket", SubjectID: t.ID,
		Title: "Rework : " + t.Title, Effect: effect,
		Details: map[string]any{"source": source, "summary": summary, "findings": findings, "sha": sha, "attempt": t.Attempt, "max_rework": e.MaxRework}})
	return err
}

func (e *Engine) handleQA(ctx context.Context, j *store.Job, owner string) error {
	t, p, ok, err := e.loadTicketCtx(ctx, j, domain.TicketQA)
	if err != nil {
		return err
	}
	if !ok {
		return e.skip(ctx, j, owner, "ticket hors QA")
	}
	wt, sha, err := e.verifiedWorktree(ctx, p, t, "qa")
	if err != nil {
		return err
	}
	var expectedAcceptance []string
	if err := json.Unmarshal(t.Acceptance, &expectedAcceptance); err != nil || len(expectedAcceptance) == 0 {
		return permanent(domain.ErrClassInternal, "critères d'acceptation du ticket absents ou illisibles")
	}
	instr, input := qaPrompt(p, t, wt, sha)
	o, err := e.runAgent(ctx, j, domain.RoleQA, t.Attempt, hermes.RunRequest{Input: input, Instructions: instr})
	if err != nil {
		return err
	}
	art, err := e.evaluate(ctx, o, func(s string) (any, error) { return domain.ParseQA(s, sha, expectedAcceptance) })
	if err != nil {
		return err
	}
	if err := e.checkWorktreeAfter(ctx, wt, sha); err != nil {
		return err
	}
	qa := art.(*domain.QAResult)
	var notice string
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		lt, err := store.LockTicket(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if lt.State != string(domain.TicketQA) || str(lt.HeadSHA) != sha {
			return nil
		}
		if qa.Verdict == "passed" {
			if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvQAPassed, audit.Agent(domain.RoleQA), qa.Summary); err != nil {
				return err
			}
			notice = fmt.Sprintf("✅ %s validé par la revue et la QA : approbation de la PR draft requise.", t.Key)
			_, err = e.createApproval(ctx, tx, store.NewApproval{Kind: domain.ApprovalPublishPR, SubjectType: "ticket", SubjectID: t.ID,
				Title: "Publier la PR : " + t.Title,
				Effect: fmt.Sprintf("Créer une PR draft sur %s de %s vers %s au SHA %s (aucun merge, aucun déploiement).",
					p.Repository, str(lt.Branch), p.BaseBranch, short(sha)),
				Details: withProjectSnapshot(p, map[string]any{"head": str(lt.Branch), "sha": sha, "qa": qa})})
			return err
		}
		if _, err := e.ticketEvent(ctx, tx, lt, domain.TicketEvQAFailed, audit.Agent(domain.RoleQA), qa.Summary); err != nil {
			return err
		}
		notice = fmt.Sprintf("🔁 QA de %s en échec : décision de rework requise.", t.Key)
		return e.reworkApproval(ctx, tx, lt, "qa", qa.Summary, qaFindings(qa), sha)
	})
	if err == nil && notice != "" {
		e.Notifier.Notify(ctx, notice)
	}
	return err
}

// ------------------------------------------------------------------ publish_pr

func (e *Engine) latestArtifact(ctx context.Context, ticketID int64, role string) json.RawMessage {
	runs, err := store.ListRunsForSubject(ctx, e.Store.Pool, "ticket", ticketID)
	if err != nil {
		return nil
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].AgentRole == role && len(runs[i].Artifact) > 0 && runs[i].Status == domain.RunCompleted {
			return runs[i].Artifact
		}
	}
	return nil
}

func (e *Engine) prBody(ctx context.Context, f *store.Feature, t *store.Ticket) string {
	var b strings.Builder
	var design domain.TechDesign
	_ = json.Unmarshal(f.Design, &design)
	var rv domain.ReviewResult
	_ = json.Unmarshal(e.latestArtifact(ctx, t.ID, domain.RoleReviewer), &rv)
	var qa domain.QAResult
	_ = json.Unmarshal(e.latestArtifact(ctx, t.ID, domain.RoleQA), &qa)
	var dev domain.DevelopResult
	_ = json.Unmarshal(e.latestArtifact(ctx, t.ID, domain.RoleDeveloper), &dev)

	fmt.Fprintf(&b, "## Résumé\n%s\n\n%s\n\n", t.Title, dev.Summary)
	if len(design.Decisions) > 0 {
		b.WriteString("## Décisions techniques\n")
		for _, d := range design.Decisions {
			fmt.Fprintf(&b, "- **%s** — %s", d.Decision, d.Rationale)
			if len(d.RejectedAlternatives) > 0 {
				fmt.Fprintf(&b, " (alternatives rejetées : %s)", strings.Join(d.RejectedAlternatives, " ; "))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "## Verdicts\n- Reviewer : **%s** — %s\n- QA : **%s** — %s\n- SHA vérifié : `%s`\n\n", rv.Verdict, rv.Summary, qa.Verdict, qa.Summary, str(t.HeadSHA))
	if len(qa.Acceptance) > 0 {
		b.WriteString("## Critères ↔ preuves\n| Critère | Statut | Preuve |\n|---|---|---|\n")
		for _, a := range qa.Acceptance {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", mdCell(a.Criterion), a.Status, mdCell(a.Evidence))
		}
		b.WriteString("\n")
	}
	if len(qa.Checks) > 0 {
		b.WriteString("## Commandes\n")
		for _, c := range qa.Checks {
			fmt.Fprintf(&b, "- `%s` → exit %d\n", strings.ReplaceAll(c.Command, "`", "'"), c.ExitCode)
		}
		b.WriteString("\n")
	}
	if len(design.Risks) > 0 {
		b.WriteString("## Risques\n")
		for _, r := range design.Risks {
			b.WriteString("- " + r + "\n")
		}
		b.WriteString("\n")
	}
	if t.IssueNumber != nil {
		fmt.Fprintf(&b, "Closes #%d\n", *t.IssueNumber)
	}
	b.WriteString("\n" + gh.Marker(t.Key) + "\n")
	return b.String()
}

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func (e *Engine) handlePublishPR(ctx context.Context, j *store.Job, owner string) error {
	t, p, ok, err := e.loadTicketCtx(ctx, j, domain.TicketPROpened)
	if err != nil {
		return err
	}
	if !ok {
		return e.skip(ctx, j, owner, "ticket hors PR_OPENED")
	}
	if t.HeadSHA == nil || t.Branch == nil {
		return permanent(domain.ErrClassPolicy, "branche ou head_sha absent")
	}
	sha := *t.HeadSHA
	remote, err := e.GitHub.BranchSHA(ctx, p.Repository, *t.Branch)
	if err != nil {
		return err
	}
	if remote != sha {
		return permanent(domain.ErrClassPolicy, "le SHA distant ne correspond plus au SHA validé")
	}
	f, err := store.GetFeature(ctx, e.Store.Pool, t.FeatureID)
	if err != nil {
		return err
	}
	if err := e.fence(ctx, "publication de la PR"); err != nil {
		return err
	}
	pr, created, err := e.GitHub.EnsureDraftPR(ctx, p.Repository, *t.Branch, p.BaseBranch, t.Title, e.prBody(ctx, f, t))
	if err != nil {
		return err
	}
	if pr.Head.SHA != sha || pr.Base.Ref != p.BaseBranch || pr.State != "open" || !pr.Draft || pr.Number <= 0 {
		return permanent(domain.ErrClassPolicy, "relecture de la PR incohérente (head/base/draft)")
	}
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		if err := store.SetTicketPR(ctx, tx, t.ID, pr.Number, pr.HTMLURL); err != nil {
			return err
		}
		if err := store.SetTicketCI(ctx, tx, t.ID, domain.CIPending); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "ticket.pr_published", SubjectType: "ticket", SubjectID: t.ID,
			After: map[string]any{"pr_number": pr.Number, "pr_url": pr.HTMLURL, "draft": pr.Draft, "created": created}, SHA: sha}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "ticket.updated", t.ID)
	})
	if err == nil {
		e.Notifier.Notify(ctx, fmt.Sprintf("🔀 PR draft #%d ouverte pour %s : %s", pr.Number, t.Key, pr.HTMLURL))
	}
	return err
}

// ------------------------------------------------------------------ sync_agents

func (e *Engine) handleSyncAgents(ctx context.Context, j *store.Job, owner string) error {
	agents, err := store.ListAgents(ctx, e.Store.Pool)
	if err != nil {
		return err
	}
	results := map[string]string{}
	for _, a := range agents {
		state := domain.SyncUnknown
		var model, provider *string
		if err := e.Hermes.Capabilities(ctx, a.HermesProfile); err != nil {
			state = domain.SyncUnreachable
		} else {
			model, provider, err = store.LastObservedRuntime(ctx, e.Store.Pool, a.Role)
			if err != nil {
				return err
			}
			if model == nil {
				if models, err := e.Hermes.Models(ctx, a.HermesProfile); err == nil && len(models) == 1 {
					m := models[0]
					model = &m
				}
			}
			switch {
			case model == nil:
				state = domain.SyncUnknown
			case a.DesiredModel == "" || a.DesiredModel == *model:
				state = domain.SyncSynced
			default:
				state = domain.SyncDrift
			}
		}
		if err := store.SetAgentObservation(ctx, e.Store.Pool, a.Role, state, model, provider); err != nil {
			return err
		}
		results[a.Role] = state
	}
	return e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		return audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "agents.synced", SubjectType: "system", After: results})
	})
}
