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
	"github.com/zeggriim2/agentic-os/internal/store"
)

// Mode d'intégration « branche de feature » : les tickets d'une feature visent sa branche de feature
// (feature/F<id>-<titre>), créée depuis la base du projet ; une fois tous les tickets intégrés, la
// feature passe en recette (ACCEPTANCE) et une PR finale branche de feature → base est ouverte après
// approbation ; son merge par un humain termine la feature.

// ticketProject renvoie le projet tel que le voient les tickets d'une feature : BaseBranch est la
// branche cible des tickets (branche de feature en mode feature_branch). Ce projet ne doit jamais être
// persisté. ensure=true crée la branche de feature à distance si besoin (effet externe, hors
// transaction) ; ensure=false se contente de la branche déjà fixée.
func (e *Engine) ticketProject(ctx context.Context, q store.Q, t *store.Ticket, ensure bool) (*store.Project, *store.Feature, error) {
	p, err := store.GetProject(ctx, q, t.ProjectID)
	if err != nil {
		return nil, nil, err
	}
	f, err := store.GetFeature(ctx, q, t.FeatureID)
	if err != nil {
		return nil, nil, err
	}
	cp := *p
	if f.IntegrationMode == domain.IntegrationFeatureBranch && ensure {
		branch, err := e.ensureFeatureBranch(ctx, f, p)
		if err != nil {
			return nil, nil, err
		}
		cp.BaseBranch = branch
		return &cp, f, nil
	}
	cp.BaseBranch = f.TargetBranch(p.BaseBranch)
	return &cp, f, nil
}

// ensureFeatureBranch fixe le nom de la branche de feature (si absent) puis la crée à distance depuis
// la pointe de la base du projet si elle n'existe pas encore. Une branche existante est conservée.
func (e *Engine) ensureFeatureBranch(ctx context.Context, f *store.Feature, p *store.Project) (string, error) {
	branch := str(f.FeatureBranch)
	if branch == "" {
		branch = domain.FeatureBranchName(f.ID, f.Title)
		err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			lf, err := store.LockFeature(ctx, tx, f.ID)
			if err != nil {
				return err
			}
			if lf.IntegrationMode != domain.IntegrationFeatureBranch {
				return permanent(domain.ErrClassPolicy, "la feature n'est plus en mode branche de feature")
			}
			if lf.FeatureBranch != nil && *lf.FeatureBranch != "" {
				branch = *lf.FeatureBranch
				return nil
			}
			return store.SetFeatureIntegration(ctx, tx, f.ID, domain.IntegrationFeatureBranch, &branch)
		})
		if err != nil {
			return "", err
		}
	}
	if err := e.fence(ctx, "création de la branche de feature"); err != nil {
		return "", err
	}
	sha, created, err := e.Git.EnsureRemoteBranch(ctx, p.Slug, p.Repository, branch, p.BaseBranch)
	if err != nil {
		return "", err
	}
	if created {
		if err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "feature.branch_created", SubjectType: "feature", SubjectID: f.ID,
				After: map[string]any{"branch": branch, "from": p.BaseBranch}, SHA: sha}); err != nil {
				return err
			}
			return store.Notify(ctx, tx, "feature.updated", f.ID)
		}); err != nil {
			return "", err
		}
		e.notifyProject(ctx, p.ID, fmt.Sprintf("🌿 Branche de feature %s créée depuis %s (%s) pour « %s ».", branch, p.BaseBranch, short(sha), f.Title))
	}
	return branch, nil
}

// integrationLabel décrit un mode d'intégration pour les textes d'approbation et de notification.
func integrationLabel(f *store.Feature, p *store.Project) string {
	if f.IntegrationMode == domain.IntegrationFeatureBranch {
		branch := str(f.FeatureBranch)
		if branch == "" {
			branch = domain.FeatureBranchName(f.ID, f.Title)
		}
		return fmt.Sprintf("branche de feature %s (créée depuis %s ; PR des tickets vers elle, puis PR finale vers %s après recette)",
			branch, p.BaseBranch, p.BaseBranch)
	}
	return "directe (PR de chaque ticket vers " + p.BaseBranch + ")"
}

func devGateEffect(f *store.Feature, p *store.Project, tickets int) string {
	return fmt.Sprintf("Autoriser le développement autonome de %d ticket(s) sur %s : branches aos/<ticket>, commits et push par Agentic OS, sans PR ni merge. Intégration : %s.",
		tickets, p.Repository, integrationLabel(f, p))
}

func publishPREffect(p *store.Project, branch, sha string) string {
	return fmt.Sprintf("Créer une PR draft sur %s de %s vers %s au SHA %s (aucun merge, aucun déploiement).", p.Repository, branch, p.BaseBranch, short(sha))
}

// completeFeature applique la fin des tickets : DONE en mode direct (ou si aucun ticket n'a été
// intégré), recette + approbation de la PR finale en mode branche de feature. Renvoie l'état atteint.
func (e *Engine) completeFeature(ctx context.Context, tx pgx.Tx, f *store.Feature, tickets []store.Ticket) (domain.FeatureState, error) {
	integrated := 0
	var issues []int
	for _, t := range tickets {
		if t.State == string(domain.TicketDone) {
			integrated++
			if t.IssueNumber != nil {
				issues = append(issues, *t.IssueNumber)
			}
		}
	}
	if f.IntegrationMode != domain.IntegrationFeatureBranch || integrated == 0 || str(f.FeatureBranch) == "" {
		_, err := e.featureEvent(ctx, tx, f, domain.FeatureEvAllTicketsDone, audit.System, "tous les tickets sont terminés")
		return domain.FeatureDone, err
	}
	p, err := store.GetProject(ctx, tx, f.ProjectID)
	if err != nil {
		return "", err
	}
	branch := *f.FeatureBranch
	nf, err := e.featureEvent(ctx, tx, f, domain.FeatureEvTicketsIntegrated, audit.System,
		fmt.Sprintf("%d ticket(s) intégré(s) dans %s : recette humaine", integrated, branch))
	if err != nil {
		return "", err
	}
	return domain.FeatureAcceptance, e.requestFeaturePR(ctx, tx, nf, p, issues)
}

func (e *Engine) requestFeaturePR(ctx context.Context, tx pgx.Tx, f *store.Feature, p *store.Project, issues []int) error {
	branch := str(f.FeatureBranch)
	closes := make([]string, 0, len(issues))
	for _, n := range issues {
		closes = append(closes, fmt.Sprintf("#%d", n))
	}
	effect := fmt.Sprintf("Créer une PR draft sur %s de %s vers %s : « %s » (aucun merge, aucun déploiement).", p.Repository, branch, p.BaseBranch, featurePRTitle(f))
	if len(closes) > 0 {
		effect += " Son merge fermera les issues " + strings.Join(closes, ", ") + "."
	}
	_, err := e.createApproval(ctx, tx, store.NewApproval{Kind: domain.ApprovalPublishFeaturePR, SubjectType: "feature", SubjectID: f.ID,
		Title:   "Publier la PR finale : " + f.Title,
		Effect:  effect,
		Details: withProjectSnapshot(p, map[string]any{"head": branch, "issues": issues})})
	return err
}

func featurePRTitle(f *store.Feature) string { return fmt.Sprintf("F%d · %s", f.ID, f.Title) }

// featurePRBody : résumé court de la feature, tickets intégrés (PR et issue), Closes des issues.
func featurePRBody(f *store.Feature, tickets []store.Ticket) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Feature\n%s\n\n", shorten(f.Title, 200))
	if d := shorten(f.Description, prSummaryMax); d != "" {
		b.WriteString(d + "\n\n")
	}
	b.WriteString("## Tickets intégrés\n")
	var closes []string
	for _, t := range tickets {
		if t.State != string(domain.TicketDone) {
			continue
		}
		line := fmt.Sprintf("- `%s` %s", t.Key, shorten(t.Title, 120))
		if t.PRNumber != nil {
			line += fmt.Sprintf(" — PR #%d", *t.PRNumber)
		}
		b.WriteString(line + "\n")
		if t.IssueNumber != nil {
			closes = append(closes, fmt.Sprintf("Closes #%d", *t.IssueNumber))
		}
	}
	b.WriteString("\nChaque ticket a été revu, validé en QA et mergé dans la branche de feature. Recette humaine avant merge.\n")
	if len(closes) > 0 {
		b.WriteString("\n" + strings.Join(closes, "\n") + "\n")
	}
	b.WriteString("\n" + gh.Marker(fmt.Sprintf("feature-%d", f.ID)) + "\n")
	return b.String()
}

// handlePublishFeaturePR ouvre (ou retrouve) la PR finale draft branche de feature → base.
func (e *Engine) handlePublishFeaturePR(ctx context.Context, j *store.Job, owner string) error {
	f, err := store.GetFeature(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if f.State != string(domain.FeatureAcceptance) || f.FeaturePRNumber != nil {
		return e.skip(ctx, j, owner, "feature hors recette ou PR finale déjà ouverte")
	}
	branch := str(f.FeatureBranch)
	if branch == "" {
		return permanent(domain.ErrClassPolicy, "branche de feature absente")
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	tickets, err := store.ListTicketsByFeature(ctx, e.Store.Pool, f.ID)
	if err != nil {
		return err
	}
	remote, err := e.GitHub.BranchSHA(ctx, p.Repository, branch)
	if err != nil {
		return err
	}
	if err := e.fence(ctx, "publication de la PR finale"); err != nil {
		return err
	}
	pr, created, err := e.GitHub.EnsureDraftPR(ctx, p.Repository, branch, p.BaseBranch, featurePRTitle(f), featurePRBody(f, tickets))
	if err != nil {
		return err
	}
	if pr.Head.SHA != remote || pr.Head.Ref != branch || pr.Base.Ref != p.BaseBranch || pr.State != "open" || !pr.Draft || pr.Number <= 0 {
		return permanent(domain.ErrClassPolicy, "relecture de la PR finale incohérente (head/base/draft)")
	}
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error {
		lf, err := store.LockFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		if lf.State != string(domain.FeatureAcceptance) {
			return nil
		}
		n, u := pr.Number, pr.HTMLURL
		if err := store.SetFeaturePR(ctx, tx, f.ID, &n, &u); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "feature.pr_published", SubjectType: "feature", SubjectID: f.ID,
			After: map[string]any{"pr_number": pr.Number, "pr_url": pr.HTMLURL, "head": branch, "base": p.BaseBranch, "created": created}, SHA: remote}); err != nil {
			return err
		}
		return store.Notify(ctx, tx, "feature.updated", f.ID)
	})
	if err == nil {
		e.notifyProject(ctx, f.ProjectID, fmt.Sprintf("🧪 PR finale #%d ouverte pour « %s » (%s → %s) : recette humaine puis merge. %s",
			pr.Number, f.Title, branch, p.BaseBranch, pr.HTMLURL))
	}
	return err
}

// ReconcileFeaturePR observe la PR finale d'une feature en recette : mergée → DONE ; fermée sans
// merge → BLOCKED (la reprise redemandera l'approbation d'une nouvelle PR).
func (e *Engine) ReconcileFeaturePR(ctx context.Context, featureID int64) error {
	e.Init()
	f, err := store.GetFeature(ctx, e.Store.Pool, featureID)
	if err != nil {
		return err
	}
	if f.State != string(domain.FeatureAcceptance) || f.FeaturePRNumber == nil {
		return nil
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	pr, err := e.GitHub.GetPR(ctx, p.Repository, *f.FeaturePRNumber)
	if err != nil {
		return err
	}
	if !pr.Merged && pr.State != "closed" {
		return nil
	}
	var notice string
	err = e.Store.InTx(ctx, func(tx pgx.Tx) error {
		lf, err := store.LockFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		if lf.State != f.State || lf.Version != f.Version {
			return nil
		}
		if pr.Merged {
			if _, err := e.featureEvent(ctx, tx, lf, domain.FeatureEvFeaturePRMerged, audit.System, fmt.Sprintf("PR finale #%d mergée", pr.Number)); err != nil {
				return err
			}
			notice = fmt.Sprintf("🎉 PR finale #%d mergée : « %s » est terminée.", pr.Number, f.Title)
			return nil
		}
		if err := store.SetFeaturePR(ctx, tx, f.ID, nil, nil); err != nil {
			return err
		}
		if _, err := e.featureEvent(ctx, tx, lf, domain.FeatureEvFail, audit.System, fmt.Sprintf("PR finale #%d fermée sans merge", pr.Number)); err != nil {
			return err
		}
		notice = fmt.Sprintf("⛔ PR finale #%d de « %s » fermée sans merge : feature bloquée (Reprendre pour redemander une PR).", pr.Number, f.Title)
		return nil
	})
	if err == nil && notice != "" {
		e.notifyProject(ctx, f.ProjectID, notice)
	}
	return err
}

// SetFeatureIntegration change le mode d'intégration d'une feature. Autorisé tant qu'aucun ticket
// n'est mergé et que la feature n'est ni en recette ni terminée. Avant le feu vert, seul le réglage
// change ; après, un job crée la branche de feature si besoin et redirige les PR déjà ouvertes.
func (e *Engine) SetFeatureIntegration(ctx context.Context, id int64, mode, login string) (*store.Feature, error) {
	e.Init()
	if !domain.ValidIntegrationMode(mode) {
		return nil, userErr(422, "invalid_integration_mode", "mode d'intégration inconnu %q (direct ou feature_branch)", mode)
	}
	actor := audit.User(login)
	err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
		f, err := store.LockFeature(ctx, tx, id)
		if err != nil {
			return err
		}
		if f.IntegrationMode == mode {
			return nil
		}
		st := domain.FeatureState(f.State)
		if st.Terminal() || st == domain.FeatureAcceptance || (st == domain.FeatureBlocked && str(f.PreviousState) == string(domain.FeatureAcceptance)) {
			return userErr(409, "integration_locked", "le mode d'intégration ne peut plus changer (feature %s)", f.State)
		}
		tickets, err := store.ListTicketsByFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		for _, t := range tickets {
			if t.State == string(domain.TicketDone) {
				return userErr(409, "integration_locked", "le ticket %s est déjà mergé : le mode d'intégration ne peut plus changer", t.Key)
			}
		}
		p, err := store.GetProject(ctx, tx, f.ProjectID)
		if err != nil {
			return err
		}
		started := st == domain.FeatureReadyForDev || st == domain.FeatureInDev
		var branch *string
		if mode == domain.IntegrationFeatureBranch && started {
			b := str(f.FeatureBranch)
			if b == "" {
				b = domain.FeatureBranchName(f.ID, f.Title)
			}
			branch = &b
		}
		if err := store.SetFeatureIntegration(ctx, tx, f.ID, mode, branch); err != nil {
			return err
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor, Action: "feature.integration_changed", SubjectType: "feature", SubjectID: f.ID,
			Before: map[string]any{"integration_mode": f.IntegrationMode, "feature_branch": f.FeatureBranch},
			After:  map[string]any{"integration_mode": mode, "feature_branch": branch}}); err != nil {
			return err
		}
		nf, err := store.LockFeature(ctx, tx, f.ID)
		if err != nil {
			return err
		}
		// Approbations en attente dont le texte ou la cible dépendent du mode.
		if ap, err := store.PendingApproval(ctx, tx, domain.ApprovalDevGate, "feature", f.ID); err == nil {
			if _, err := store.UpdatePendingApproval(ctx, tx, domain.ApprovalDevGate, "feature", f.ID, devGateEffect(nf, p, len(tickets)), ap.Details); err != nil {
				return err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		target := *p
		target.BaseBranch = nf.TargetBranch(p.BaseBranch)
		for _, t := range tickets {
			ap, err := store.PendingApproval(ctx, tx, domain.ApprovalPublishPR, "ticket", t.ID)
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			details := map[string]any{}
			_ = json.Unmarshal(ap.Details, &details)
			if _, err := store.UpdatePendingApproval(ctx, tx, domain.ApprovalPublishPR, "ticket", t.ID,
				publishPREffect(&target, str(t.Branch), str(t.HeadSHA)), withProjectSnapshot(&target, details)); err != nil {
				return err
			}
		}
		if started {
			if _, err := e.enqueue(ctx, tx, store.NewJob{Kind: domain.JobSwitchIntegration, SubjectType: "feature", SubjectID: f.ID, ProjectID: &f.ProjectID,
				IdempotencyKey: jobKey(domain.JobSwitchIntegration, "feature", f.ID, fmt.Sprintf("%s-v%d", mode, nf.Version))}); err != nil {
				return err
			}
		}
		return store.Notify(ctx, tx, "feature.updated", f.ID)
	})
	if err != nil {
		return nil, err
	}
	return store.GetFeature(ctx, e.Store.Pool, id)
}

// handleSwitchIntegration applique un changement de mode après le feu vert : crée la branche de
// feature (mode feature_branch) puis redirige vers la nouvelle cible les PR de tickets déjà ouvertes.
func (e *Engine) handleSwitchIntegration(ctx context.Context, j *store.Job, owner string) error {
	f, err := store.GetFeature(ctx, e.Store.Pool, j.SubjectID)
	if err != nil {
		return err
	}
	if f.State != string(domain.FeatureReadyForDev) && f.State != string(domain.FeatureInDev) {
		return e.skip(ctx, j, owner, "feature hors développement")
	}
	p, err := store.GetProject(ctx, e.Store.Pool, f.ProjectID)
	if err != nil {
		return err
	}
	target := p.BaseBranch
	if f.IntegrationMode == domain.IntegrationFeatureBranch {
		if target, err = e.ensureFeatureBranch(ctx, f, p); err != nil {
			return err
		}
	}
	tickets, err := store.ListTicketsByFeature(ctx, e.Store.Pool, f.ID)
	if err != nil {
		return err
	}
	var moved []string
	for _, t := range tickets {
		if t.PRNumber == nil || (t.State != string(domain.TicketPROpened) && t.State != string(domain.TicketHumanMerge)) {
			continue
		}
		if err := e.fence(ctx, "redirection de PR"); err != nil {
			return err
		}
		pr, err := e.GitHub.RetargetPR(ctx, p.Repository, *t.PRNumber, target)
		if err != nil {
			return err
		}
		moved = append(moved, fmt.Sprintf("#%d", pr.Number))
		if err := e.Store.InTx(ctx, func(tx pgx.Tx) error {
			if err := audit.Write(ctx, tx, audit.Entry{Actor: audit.System, Action: "ticket.pr_retargeted", SubjectType: "ticket", SubjectID: t.ID,
				After: map[string]any{"pr_number": pr.Number, "base": target}}); err != nil {
				return err
			}
			return store.Notify(ctx, tx, "ticket.updated", t.ID)
		}); err != nil {
			return err
		}
	}
	err = e.finish(ctx, j, owner, func(tx pgx.Tx) error { return store.Notify(ctx, tx, "feature.updated", f.ID) })
	if err == nil {
		msg := fmt.Sprintf("🔀 « %s » passe en intégration %s.", f.Title, integrationLabel(f, p))
		if len(moved) > 0 {
			msg += " PR redirigée(s) vers " + target + " : " + strings.Join(moved, ", ") + "."
		}
		e.notifyProject(ctx, f.ProjectID, msg)
	}
	return err
}
