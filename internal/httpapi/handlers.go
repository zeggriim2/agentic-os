package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/audit"
	"github.com/zeggriim2/agentic-os/internal/domain"
	"github.com/zeggriim2/agentic-os/internal/store"
)

// ================================================================== overview

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := store.GetOverview(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	paused, err := store.DispatchPaused(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	recent, err := store.ListAudit(ctx, s.Store.Pool, store.AuditFilter{}, store.Page{Limit: 20})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(recent) > 20 {
		recent = recent[:20]
	}
	alerts := []map[string]any{}
	if paused {
		alerts = append(alerts, map[string]any{"level": "warning", "message": "Le dispatch est en pause : aucun nouveau job n'est pris.", "link": "/"})
	}
	if o.BlockedFeatures+o.BlockedTickets > 0 {
		alerts = append(alerts, map[string]any{"level": "error", "message": "Des éléments sont bloqués et attendent une action.", "link": "/pipeline"})
	}
	if o.PendingApprovals > 0 {
		alerts = append(alerts, map[string]any{"level": "info", "message": "Des approbations attendent votre décision.", "link": "/approvals"})
	}
	agents, err := store.ListAgents(ctx, s.Store.Pool)
	if err == nil {
		for _, a := range agents {
			if a.SyncState == domain.SyncDrift || a.SyncState == domain.SyncUnreachable {
				alerts = append(alerts, map[string]any{"level": "warning", "message": "Agent " + a.Role + " : " + a.SyncState, "link": "/agents"})
			}
		}
	}
	if stopping, err := store.ListStoppingRuns(ctx, s.Store.Pool); err == nil {
		for _, r := range stopping {
			if r.StopAttempts >= 3 {
				alerts = append(alerts, map[string]any{"level": "error", "message": "Arrêt du run #" + itoa(r.ID) + " non confirmé par Hermes", "link": "/runs/" + itoa(r.ID)})
			}
		}
	}
	tickets, err := store.ListTicketsByStates(ctx, s.Store.Pool, []string{"PR_OPENED", "HUMAN_MERGE"})
	if err == nil {
		for _, t := range tickets {
			if t.CIState == domain.CIFailure {
				alerts = append(alerts, map[string]any{"level": "error", "message": "CI en échec pour " + t.Key, "link": "/tickets/" + itoa(t.ID)})
			}
		}
	}
	tokens7d := o.Tokens7d
	if tokens7d == nil {
		tokens7d = []store.DayTokens{}
	}
	writeJSON(w, 200, map[string]any{
		"pending_approvals": o.PendingApprovals,
		"blocked":           map[string]int{"features": o.BlockedFeatures, "tickets": o.BlockedTickets},
		"active_runs":       o.ActiveRuns,
		"tokens_today":      o.TokensToday,
		"tokens_7d":         tokens7d,
		"dispatch_paused":   paused,
		"alerts":            alerts,
		"recent_activity":   mapSlice(recent, viewAudit),
	})
}

// ================================================================== projects

type projectCreate struct {
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Repository     string   `json:"repository"`
	BaseBranch     string   `json:"base_branch"`
	RequiredChecks []string `json:"required_checks"`
	Mode           string   `json:"mode"`
	TestCommand    string   `json:"test_command"`
}

func validateProject(in *store.ProjectInput) (string, string) {
	in.Name, in.BaseBranch, in.TestCommand = trim(in.Name), trim(in.BaseBranch), trim(in.TestCommand)
	if in.BaseBranch == "" {
		in.BaseBranch = "main"
	}
	if in.Mode == "" {
		in.Mode = "sandbox"
	}
	switch {
	case !domain.ValidSlug(in.Slug):
		return "invalid_slug", "slug invalide (minuscules, chiffres, tirets ; 40 caractères max)"
	case !domain.ValidProjectName(in.Name):
		return "invalid_name", "nom requis (200 caractères max)"
	case !domain.ValidRepository(in.Repository):
		return "invalid_repository", "dépôt invalide (format owner/repo)"
	case !domain.ValidBranch(in.BaseBranch):
		return "invalid_base_branch", "branche de base invalide"
	case in.Mode != "sandbox" && in.Mode != "product":
		return "invalid_mode", "mode invalide (sandbox ou product)"
	case !domain.ValidTestCommand(in.TestCommand):
		return "invalid_test_command", "commande de test trop longue"
	case len(in.RequiredChecks) > domain.MaxRequiredChecks:
		return "invalid_required_checks", "20 checks requis maximum"
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, c := range in.RequiredChecks {
		c = trim(c)
		if !domain.ValidCheckName(c) {
			return "invalid_required_checks", "nom de check requis invalide"
		}
		if !seen[c] {
			seen[c] = true
			clean = append(clean, c)
		}
	}
	in.RequiredChecks = clean
	return "", ""
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	items, err := store.ListProjects(r.Context(), s.Store.Pool, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, list(items, p.Limit, func(x store.Project) int64 { return x.ID }, func(x *store.Project) any { return viewProject(x) }))
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req projectCreate
	if !decode(w, r, &req) {
		return
	}
	in := store.ProjectInput{Slug: trim(req.Slug), Name: req.Name, Repository: trim(req.Repository), BaseBranch: req.BaseBranch,
		RequiredChecks: req.RequiredChecks, Mode: req.Mode, TestCommand: req.TestCommand, Enabled: false}
	if code, msg := validateProject(&in); code != "" {
		writeError(w, 422, code, msg)
		return
	}
	var p *store.Project
	err := s.Store.InTx(r.Context(), func(tx pgx.Tx) error {
		var err error
		if p, err = store.CreateProject(r.Context(), tx, in); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, audit.Entry{Actor: audit.User(actorLogin(r)), Action: "project.created", SubjectType: "project",
			SubjectID: p.ID, After: viewProject(p)})
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 201, viewProject(p))
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	p, err := store.GetProject(r.Context(), s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, viewProject(p))
}

type projectPatch struct {
	Name           *string   `json:"name"`
	Repository     *string   `json:"repository"`
	BaseBranch     *string   `json:"base_branch"`
	RequiredChecks *[]string `json:"required_checks"`
	Mode           *string   `json:"mode"`
	Enabled        *bool     `json:"enabled"`
	TestCommand    *string   `json:"test_command"`
	Confirm        *string   `json:"confirm"`
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req projectPatch
	if !decode(w, r, &req) {
		return
	}
	var out *store.Project
	err := s.Store.InTx(r.Context(), func(tx pgx.Tx) error {
		cur, err := store.LockProject(r.Context(), tx, id)
		if err != nil {
			return err
		}
		in := store.ProjectInput{Slug: cur.Slug, Name: cur.Name, Repository: cur.Repository, BaseBranch: cur.BaseBranch,
			RequiredChecks: cur.RequiredChecks, Mode: cur.Mode, Enabled: cur.Enabled, TestCommand: cur.TestCommand}
		if req.Name != nil {
			in.Name = *req.Name
		}
		if req.Repository != nil {
			in.Repository = trim(*req.Repository)
		}
		if req.BaseBranch != nil {
			in.BaseBranch = *req.BaseBranch
		}
		if req.RequiredChecks != nil {
			in.RequiredChecks = *req.RequiredChecks
		}
		if req.Mode != nil {
			in.Mode = *req.Mode
		}
		if req.Enabled != nil {
			in.Enabled = *req.Enabled
		}
		if req.TestCommand != nil {
			in.TestCommand = *req.TestCommand
		}
		if code, msg := validateProject(&in); code != "" {
			return &userError{422, code, msg}
		}
		structuralChange := in.Repository != cur.Repository || in.BaseBranch != cur.BaseBranch || !slices.Equal(in.RequiredChecks, cur.RequiredChecks)
		if structuralChange {
			active, err := store.ProjectHasActiveFeatures(r.Context(), tx, id)
			if err != nil {
				return err
			}
			if active {
				return &userError{409, "project_in_use", "dépôt, branche de base et checks requis sont figés tant qu'une feature est active"}
			}
		}
		becomesActiveProduct := in.Enabled && in.Mode == "product" && !(cur.Enabled && cur.Mode == "product")
		if becomesActiveProduct && (req.Confirm == nil || *req.Confirm != cur.Slug) {
			return &userError{422, "confirmation_required", "activer un projet product exige {\"enabled\":true,\"confirm\":\"" + cur.Slug + "\"}"}
		}
		if out, err = store.UpdateProject(r.Context(), tx, id, in); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, audit.Entry{Actor: audit.User(actorLogin(r)), Action: "project.updated", SubjectType: "project",
			SubjectID: id, Before: viewProject(cur), After: viewProject(out)})
	})
	if err != nil {
		s.failUser(w, r, err)
		return
	}
	writeJSON(w, 200, viewProject(out))
}

type userError struct {
	status        int
	code, message string
}

func (e *userError) Error() string { return e.message }

func (s *Server) failUser(w http.ResponseWriter, r *http.Request, err error) {
	if ue, ok := err.(*userError); ok {
		writeError(w, ue.status, ue.code, ue.message)
		return
	}
	s.fail(w, r, err)
}

// ================================================================== agents

func (s *Server) listAgents(w http.ResponseWriter, r *http.Request) {
	items, err := store.ListAgents(r.Context(), s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": mapSlice(items, viewAgent), "next_cursor": nil})
}

type agentPatch struct {
	DesiredModel    *string `json:"desired_model"`
	MaxTokensPerRun *int    `json:"max_tokens_per_run"`
	Enabled         *bool   `json:"enabled"`
	HermesProfile   *string `json:"hermes_profile"`
}

func (s *Server) patchAgent(w http.ResponseWriter, r *http.Request) {
	role := chi.URLParam(r, "role")
	var req agentPatch
	if !decode(w, r, &req) {
		return
	}
	var out *store.Agent
	err := s.Store.InTx(r.Context(), func(tx pgx.Tx) error {
		cur, err := store.GetAgent(r.Context(), tx, role)
		if err != nil {
			return err
		}
		next := *cur
		if req.DesiredModel != nil {
			m := trim(*req.DesiredModel)
			if !domain.ValidDesiredModel(m) {
				return &userError{422, "invalid_model", "nom de modèle invalide"}
			}
			next.DesiredModel = m
		}
		if req.MaxTokensPerRun != nil {
			if !domain.ValidMaxTokens(*req.MaxTokensPerRun) {
				return &userError{422, "invalid_max_tokens", "max_tokens_per_run doit être compris entre 1 000 et 50 000 000"}
			}
			next.MaxTokensPerRun = *req.MaxTokensPerRun
		}
		if req.Enabled != nil {
			next.Enabled = *req.Enabled
		}
		if req.HermesProfile != nil {
			if !domain.ValidProfile(*req.HermesProfile) {
				return &userError{422, "invalid_profile", "nom de profil Hermes invalide"}
			}
			next.HermesProfile = *req.HermesProfile
		}
		if out, err = store.UpdateAgent(r.Context(), tx, &next); err != nil {
			return err
		}
		return audit.Write(r.Context(), tx, audit.Entry{Actor: audit.User(actorLogin(r)), Action: "agent.updated", SubjectType: "agent",
			SubjectID: cur.ID, Before: viewAgent(cur), After: viewAgent(out)})
	})
	if err != nil {
		s.failUser(w, r, err)
		return
	}
	writeJSON(w, 200, viewAgent(out))
}

func (s *Server) syncAgents(w http.ResponseWriter, r *http.Request) {
	j, err := s.Engine.EnqueueSync(r.Context(), actorLogin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 202, map[string]any{"job": viewJob(j)})
}

// ================================================================== features

func (s *Server) listFeatures(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	pid, ok := queryInt(w, r, "project_id")
	if !ok {
		return
	}
	state := r.URL.Query().Get("state")
	if state != "" && !domain.FeatureState(state).Valid() {
		writeError(w, 400, "invalid_state", "état inconnu")
		return
	}
	items, err := store.ListFeatures(r.Context(), s.Store.Pool, store.FeatureFilter{State: state, ProjectID: pid}, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _ := store.ProjectNames(r.Context(), s.Store.Pool)
	writeJSON(w, 200, list(items, p.Limit, func(x store.Feature) int64 { return x.ID }, func(x *store.Feature) any { return viewFeature(x, names) }))
}

type featureCreate struct {
	ProjectID   int64  `json:"project_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (s *Server) createFeature(w http.ResponseWriter, r *http.Request) {
	var req featureCreate
	if !decode(w, r, &req) {
		return
	}
	req.Title = trim(req.Title)
	if !domain.ValidFeatureTitle(req.Title) {
		writeError(w, 422, "invalid_title", "titre requis (200 caractères max)")
		return
	}
	if !domain.ValidFeatureDescription(req.Description) {
		writeError(w, 422, "invalid_description", "description requise (20 000 caractères max)")
		return
	}
	ctx := r.Context()
	var f *store.Feature
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockProject(ctx, tx, req.ProjectID); err != nil {
			if err == store.ErrNotFound {
				return &userError{422, "invalid_project", "projet inexistant"}
			}
			return err
		}
		var err error
		if f, err = store.CreateFeature(ctx, tx, req.ProjectID, req.Title, req.Description); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.User(actorLogin(r)), Action: "feature.created", SubjectType: "feature",
			SubjectID: f.ID, After: map[string]any{"title": f.Title, "state": f.State}}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "feature.updated", f.ID)
	})
	if err != nil {
		s.failUser(w, r, err)
		return
	}
	names, _ := store.ProjectNames(ctx, s.Store.Pool)
	writeJSON(w, 201, viewFeature(f, names))
}

func (s *Server) getFeature(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	f, err := store.GetFeature(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _ := store.ProjectNames(ctx, s.Store.Pool)
	tickets, err := store.ListTicketsByFeature(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	approvals, err := store.ListApprovalsForSubject(ctx, s.Store.Pool, "feature", id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	runs, err := store.ListRunsForSubject(ctx, s.Store.Pool, "feature", id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	jobs, err := store.ListJobsForSubject(ctx, s.Store.Pool, "feature", id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tids := make([]int64, 0, len(tickets))
	for _, t := range tickets {
		tids = append(tids, t.ID)
	}
	timeline, timelineTruncated, err := store.ListAuditForSubjects(ctx, s.Store.Pool, id, tids)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"feature":   viewFeature(f, names),
		"spec":      rawOrNull(f.Spec),
		"design":    rawOrNull(f.Design),
		"tickets":   mapSlice(tickets, func(t *store.Ticket) ticketView { return viewTicket(t, names) }),
		"approvals": mapSlice(approvals, viewApproval),
		"runs":      mapSlice(runs, func(x *store.Run) runView { return viewRun(x, false) }),
		"jobs":      mapSlice(jobs, viewJob),
		"timeline":  mapSlice(timeline, viewAudit),
		// timeline_truncated : les entrées les plus anciennes sont omises (voir /api/audit).
		"timeline_truncated": timelineTruncated,
	})
}

type actionReq struct {
	Action string `json:"action"`
}

func (s *Server) featureAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req actionReq
	if !decode(w, r, &req) {
		return
	}
	if req.Action != "start_spec" && req.Action != "cancel" && req.Action != "retry" {
		writeError(w, 422, "invalid_action", "action invalide (start_spec, cancel ou retry)")
		return
	}
	f, err := s.Engine.FeatureAction(r.Context(), id, req.Action, actorLogin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _ := store.ProjectNames(r.Context(), s.Store.Pool)
	writeJSON(w, 200, viewFeature(f, names))
}

// ================================================================== tickets

func (s *Server) getTicket(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	t, err := store.GetTicket(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _ := store.ProjectNames(ctx, s.Store.Pool)
	runs, err := store.ListRunsForSubject(ctx, s.Store.Pool, "ticket", id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	approvals, err := store.ListApprovalsForSubject(ctx, s.Store.Pool, "ticket", id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	jobs, err := store.ListJobsForSubject(ctx, s.Store.Pool, "ticket", id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	timeline, timelineTruncated, err := store.ListAuditForSubjects(ctx, s.Store.Pool, 0, []int64{id})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"ticket":             viewTicket(t, names),
		"runs":               mapSlice(runs, func(x *store.Run) runView { return viewRun(x, true) }),
		"approvals":          mapSlice(approvals, viewApproval),
		"jobs":               mapSlice(jobs, viewJob),
		"timeline":           mapSlice(timeline, viewAudit),
		"timeline_truncated": timelineTruncated,
	})
}

func (s *Server) ticketAction(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req actionReq
	if !decode(w, r, &req) {
		return
	}
	if req.Action != "cancel" && req.Action != "retry" {
		writeError(w, 422, "invalid_action", "action invalide (cancel ou retry)")
		return
	}
	t, err := s.Engine.TicketAction(r.Context(), id, req.Action, actorLogin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _ := store.ProjectNames(r.Context(), s.Store.Pool)
	writeJSON(w, 200, viewTicket(t, names))
}

// ================================================================== pipeline

var pipelineColumns = []struct{ state, label string }{
	{"IDEA", "Idées"}, {"SPEC", "Spécification"}, {"SPEC_REVIEW", "Revue de spec"}, {"TECH_DESIGN", "Conception"},
	{"TECH_DESIGN_REVIEW", "Revue de conception"}, {"ISSUES_OPENED", "Issues"}, {"HUMAN_GATE", "Feu vert"},
	{"READY_FOR_DEV", "Prêt pour le dev"}, {"IN_DEV", "En développement"}, {"DEV", "Développement"}, {"REVIEW", "Revue"},
	{"QA", "QA"}, {"CHANGES_REQUESTED", "Corrections demandées"}, {"AWAITING_PR_APPROVAL", "PR à approuver"},
	{"PR_OPENED", "PR ouverte"}, {"HUMAN_MERGE", "Merge humain"}, {"BLOCKED", "Bloqué"}, {"DONE", "Terminé"},
}

var attentionStates = map[string]bool{"SPEC_REVIEW": true, "TECH_DESIGN_REVIEW": true, "HUMAN_GATE": true, "CHANGES_REQUESTED": true,
	"AWAITING_PR_APPROVAL": true, "HUMAN_MERGE": true, "BLOCKED": true}

func (s *Server) pipeline(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	features, err := store.ListPipelineFeatures(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tickets, err := store.ListPipelineTickets(ctx, s.Store.Pool)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names, _ := store.ProjectNames(ctx, s.Store.Pool)
	byState := map[string][]map[string]any{}
	for _, f := range features {
		byState[f.State] = append(byState[f.State], map[string]any{"type": "feature", "id": f.ID, "title": f.Title, "project": names[f.ProjectID],
			"state": f.State, "updated_at": ts(f.UpdatedAt), "attention": attentionStates[f.State]})
	}
	for _, t := range tickets {
		if t.State == "PLANNED" {
			continue
		}
		byState[t.State] = append(byState[t.State], map[string]any{"type": "ticket", "id": t.ID, "title": t.Key + " · " + t.Title,
			"project": names[t.ProjectID], "state": t.State, "updated_at": ts(t.UpdatedAt),
			"attention": attentionStates[t.State] || t.CIState == domain.CIFailure})
	}
	cols := make([]map[string]any, 0, len(pipelineColumns))
	for _, c := range pipelineColumns {
		items := byState[c.state]
		if items == nil {
			items = []map[string]any{}
		}
		cols = append(cols, map[string]any{"state": c.state, "label": c.label, "items": items})
	}
	writeJSON(w, 200, map[string]any{"columns": cols})
}

// ================================================================== runs

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	pid, ok := queryInt(w, r, "project_id")
	if !ok {
		return
	}
	q := r.URL.Query()
	f := store.RunFilter{Status: q.Get("status"), Role: q.Get("role"), ProjectID: pid}
	items, err := store.ListRuns(r.Context(), s.Store.Pool, f, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, list(items, p.Limit, func(x store.Run) int64 { return x.ID }, func(x *store.Run) any { return viewRun(x, false) }))
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	run, err := store.GetRun(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	job, err := store.GetJob(ctx, s.Store.Pool, run.JobID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	evs, eventsTruncated, err := store.ListRunEvents(ctx, s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	events := make([]map[string]any, 0, len(evs))
	for i := range evs {
		events = append(events, runEventView(&evs[i]))
	}
	output := ""
	if run.Output != nil {
		output = *run.Output
	}
	writeJSON(w, 200, map[string]any{"run": viewRun(run, true), "job": viewJob(job), "events": events, "events_truncated": eventsTruncated, "output": output})
}

func (s *Server) stopRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	run, err := s.Engine.StopRun(r.Context(), id, actorLogin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, viewRun(run, false))
}

// ================================================================== approvals

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	status := r.URL.Query().Get("status")
	switch status {
	case "", "pending", "approved", "rejected", "executed", "execution_failed", "expired":
	default:
		writeError(w, 400, "invalid_status", "statut inconnu")
		return
	}
	items, err := store.ListApprovals(r.Context(), s.Store.Pool, store.ApprovalFilter{Status: status}, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, list(items, p.Limit, func(x store.Approval) int64 { return x.ID }, func(x *store.Approval) any { return viewApproval(x) }))
}

func (s *Server) getApproval(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, err := store.GetApproval(r.Context(), s.Store.Pool, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, viewApproval(a))
}

type decisionReq struct {
	Decision string `json:"decision"`
	Comment  string `json:"comment"`
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req decisionReq
	if !decode(w, r, &req) {
		return
	}
	a, err := s.Engine.DecideApproval(r.Context(), id, req.Decision, req.Comment, actorLogin(r))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, viewApproval(a))
}

// ================================================================== audit

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	p, ok := page(w, r)
	if !ok {
		return
	}
	sid, ok := queryInt(w, r, "subject_id")
	if !ok {
		return
	}
	q := r.URL.Query()
	items, err := store.ListAudit(r.Context(), s.Store.Pool, store.AuditFilter{SubjectType: q.Get("subject_type"), SubjectID: sid, Actor: q.Get("actor")}, p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, list(items, p.Limit, func(x store.AuditEntry) int64 { return x.ID }, func(x *store.AuditEntry) any { return viewAudit(x) }))
}

// ================================================================== system

func (s *Server) systemHealth(w http.ResponseWriter, r *http.Request) {
	s.healthMu.Lock()
	if s.healthCache != nil && time.Since(s.healthAt) < 15*time.Second {
		cached := s.healthCache
		s.healthMu.Unlock()
		writeJSON(w, 200, cached)
		return
	}
	s.healthMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	out := map[string]any{"version": s.Version, "revision": s.Revision, "workers": s.Workers}
	if err := s.Store.Ping(ctx); err != nil {
		out["database"] = "down"
	} else {
		out["database"] = "ok"
	}
	v, _ := s.Store.SchemaVersion(ctx)
	out["schema_version"] = v
	if s.Engine != nil {
		out["active_jobs"] = s.Engine.ActiveWorkers()
	}
	status := func(err error, okVal string) string {
		if err == nil {
			return okVal
		}
		if strings.Contains(err.Error(), "non configuré") || strings.Contains(err.Error(), "indisponible") {
			return "not_configured"
		}
		return "down"
	}
	profiles := []map[string]any{}
	hermesOK := 0
	agents, _ := store.ListAgents(ctx, s.Store.Pool)
	seen := map[string]bool{}
	for _, a := range agents {
		if seen[a.HermesProfile] {
			continue
		}
		seen[a.HermesProfile] = true
		st := "unknown"
		if s.Health.Hermes != nil {
			st = status(s.Health.Hermes.Capabilities(ctx, a.HermesProfile), "ok")
		}
		if st == "ok" {
			hermesOK++
		}
		profiles = append(profiles, map[string]any{"profile": a.HermesProfile, "status": st})
	}
	switch {
	case len(profiles) > 0 && hermesOK == len(profiles):
		out["hermes"] = "ok"
	case hermesOK > 0:
		out["hermes"] = "degraded"
	default:
		out["hermes"] = "down"
	}
	out["profiles"] = profiles
	out["github"], out["telegram"] = "unknown", "unknown"
	if s.Health.GitHub != nil {
		out["github"] = status(s.Health.GitHub.Check(ctx), "ok")
	}
	if s.Health.Telegram != nil {
		out["telegram"] = status(s.Health.Telegram.Check(ctx), "ok")
	}
	s.healthMu.Lock()
	s.healthCache, s.healthAt = out, time.Now()
	s.healthMu.Unlock()
	writeJSON(w, 200, out)
}

type dispatchReq struct {
	Paused *bool `json:"paused"`
}

func (s *Server) setDispatch(w http.ResponseWriter, r *http.Request) {
	var req dispatchReq
	if !decode(w, r, &req) {
		return
	}
	if req.Paused == nil {
		writeError(w, 422, "invalid_request", "champ paused (booléen) requis")
		return
	}
	ctx := r.Context()
	err := s.Store.InTx(ctx, func(tx pgx.Tx) error {
		if err := store.SetDispatchPaused(ctx, tx, *req.Paused); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.User(actorLogin(r)), Action: "system.dispatch", SubjectType: "system",
			After: map[string]any{"paused": *req.Paused}}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "overview.invalidate", 0)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"dispatch_paused": *req.Paused})
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
