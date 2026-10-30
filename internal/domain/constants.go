package domain

import (
	"regexp"
	"strconv"
	"strings"
)

// Types de jobs.
const (
	JobPOSpec          = "po_spec"
	JobTechDesign      = "tech_design"
	JobOpenIssues      = "open_issues"
	JobDevelop         = "develop"
	JobReview          = "review"
	JobQA              = "qa"
	JobPublishPR       = "publish_pr"
	JobExecuteApproval = "execute_approval"
	JobSyncAgents      = "sync_agents"
	// JobPublishFeaturePR ouvre la PR finale branche de feature → base (mode branche de feature).
	JobPublishFeaturePR = "publish_feature_pr"
	// JobSwitchIntegration crée la branche de feature et redirige les PR ouvertes après un
	// changement de mode d'intégration en cours de développement.
	JobSwitchIntegration = "switch_integration"
	// JobOpenCorrection crée l'issue d'un ticket de correction (retour de recette) puis le rend prêt.
	JobOpenCorrection = "open_correction"
	// JobRefreshFeaturePR met à jour titre et corps de la PR finale déjà ouverte (Closes des corrections).
	JobRefreshFeaturePR = "refresh_feature_pr"
)

// JobKinds liste les types de jobs connus.
var JobKinds = []string{JobPOSpec, JobTechDesign, JobOpenIssues, JobDevelop, JobReview, JobQA, JobPublishPR, JobExecuteApproval, JobSyncAgents,
	JobPublishFeaturePR, JobSwitchIntegration, JobOpenCorrection, JobRefreshFeaturePR}

// Statuts de job.
const (
	JobQueued    = "queued"
	JobLeased    = "leased"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// Classes d'erreur.
const (
	ErrClassTimeout       = "timeout"
	ErrClassBudget        = "budget"
	ErrClassPolicy        = "policy"
	ErrClassProvider      = "provider"
	ErrClassInvalidOutput = "invalid_output"
	ErrClassExternal      = "external"
	ErrClassInternal      = "internal"
)

// Statuts de run.
const (
	RunSubmitted = "submitted"
	RunRunning   = "running"
	RunStopping  = "stopping" // arrêt demandé, pas encore confirmé par Hermes
	RunCompleted = "completed"
	RunFailed    = "failed"
	RunStopped   = "stopped"
	RunLost      = "lost"
)

// RunTerminal indique un statut de run final.
func RunTerminal(s string) bool {
	return s == RunCompleted || s == RunFailed || s == RunStopped || s == RunLost
}

// Types d'approbation.
const (
	ApprovalSpecReview   = "spec_review"
	ApprovalDesignReview = "design_review"
	ApprovalDevGate      = "dev_gate"
	ApprovalRework       = "rework"
	ApprovalPublishPR    = "publish_pr"
	// ApprovalPublishFeaturePR autorise la PR finale d'une feature en mode branche de feature.
	ApprovalPublishFeaturePR = "publish_feature_pr"
)

// Modes d'intégration des tickets d'une feature.
const (
	// IntegrationDirect : chaque ticket ouvre sa PR vers la branche de base du projet.
	IntegrationDirect = "direct"
	// IntegrationFeatureBranch : les tickets visent une branche de feature, mergée dans la base
	// par une PR finale après recette humaine.
	IntegrationFeatureBranch = "feature_branch"
)

// ValidIntegrationMode valide un mode d'intégration.
func ValidIntegrationMode(s string) bool {
	return s == IntegrationDirect || s == IntegrationFeatureBranch
}

// FeatureBranchName renvoie la branche de feature : feature/F<id>-<titre court> (ASCII, minuscules,
// tirets, 40 caractères au plus pour le titre).
func FeatureBranchName(id int64, title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(foldAccents(title)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-")
	}
	if slug == "" {
		return "feature/F" + strconv.FormatInt(id, 10)
	}
	return "feature/F" + strconv.FormatInt(id, 10) + "-" + slug
}

var accentFold = strings.NewReplacer(
	"à", "a", "â", "a", "ä", "a", "á", "a", "ã", "a", "å", "a", "ç", "c", "é", "e", "è", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i", "ì", "i", "ô", "o", "ö", "o", "ó", "o", "ò", "o", "õ", "o", "ù", "u", "û", "u",
	"ü", "u", "ú", "u", "ÿ", "y", "ñ", "n", "œ", "oe", "æ", "ae",
	"À", "a", "Â", "a", "Ä", "a", "Ç", "c", "É", "e", "È", "e", "Ê", "e", "Ë", "e", "Î", "i", "Ï", "i", "Ô", "o",
	"Ö", "o", "Ù", "u", "Û", "u", "Ü", "u", "Œ", "oe", "Æ", "ae")

func foldAccents(s string) string { return accentFold.Replace(s) }

// Statuts d'approbation.
const (
	ApprovalPending         = "pending"
	ApprovalApproved        = "approved"
	ApprovalRejected        = "rejected"
	ApprovalExecuted        = "executed"
	ApprovalExecutionFailed = "execution_failed"
	ApprovalExpired         = "expired"
)

// Rôles d'agents.
const (
	RolePO        = "po"
	RoleTechLead  = "tech_lead"
	RoleDeveloper = "developer"
	RoleReviewer  = "reviewer"
	RoleQA        = "qa"
)

// Roles liste les rôles.
var Roles = []string{RolePO, RoleTechLead, RoleDeveloper, RoleReviewer, RoleQA}

// RoleForJob renvoie le rôle d'agent d'un job (vide si aucun agent).
func RoleForJob(kind string) string {
	switch kind {
	case JobPOSpec:
		return RolePO
	case JobTechDesign:
		return RoleTechLead
	case JobDevelop:
		return RoleDeveloper
	case JobReview:
		return RoleReviewer
	case JobQA:
		return RoleQA
	}
	return ""
}

// États de synchronisation des agents.
const (
	SyncSynced      = "SYNCED"
	SyncDrift       = "DRIFT_DETECTED"
	SyncUnknown     = "UNKNOWN"
	SyncUnreachable = "UNREACHABLE"
)

// États CI.
const (
	CIUnverified = "unverified"
	CIPending    = "pending"
	CISuccess    = "success"
	CIFailure    = "failure"
)

var (
	slugRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	repoRe      = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}/[A-Za-z0-9_.-]{1,100}$`)
	branchRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	profileRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	shaRe       = regexp.MustCompile(`^[0-9a-f]{40}$`)
	ticketKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}-F[0-9]+-T[0-9]+$`)
	checkNameRe = regexp.MustCompile(`^[^\x00-\x1f]{1,200}$`)
	modelNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}$`)
	providerRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ValidSlug valide un slug de projet.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// ValidRepository valide owner/repo.
func ValidRepository(s string) bool {
	return repoRe.MatchString(s) && !containsDotDot(s)
}

// ValidBranch valide un nom de branche (pas de "..", pas d'espace).
func ValidBranch(s string) bool {
	return branchRe.MatchString(s) && !containsDotDot(s) && s[len(s)-1] != '/' && !hasSuffix(s, ".lock")
}

// ValidProfile valide un nom de profil Hermes.
func ValidProfile(s string) bool { return profileRe.MatchString(s) }

// ValidSHA valide un SHA git complet.
func ValidSHA(s string) bool { return shaRe.MatchString(s) }

// ValidTicketKey valide une clé de ticket.
func ValidTicketKey(s string) bool { return ticketKeyRe.MatchString(s) }

// ValidCheckName valide un nom de check CI requis (après trim : non vide, 200 caractères
// max, sans caractère de contrôle).
func ValidCheckName(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && checkNameRe.MatchString(t)
}

// ValidModelName valide un nom de modèle.
func ValidModelName(s string) bool { return modelNameRe.MatchString(s) }

// ValidProviderName valide un identifiant de fournisseur LLM (slug Hermes : anthropic, openai-codex…).
func ValidProviderName(s string) bool { return providerRe.MatchString(s) }

// ModelChoice est un modèle imposé pour un rôle : fournisseur et modèle (tels que Hermes les nomme).
type ModelChoice struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Empty indique l'absence de choix (défaut).
func (m ModelChoice) Empty() bool { return m.Provider == "" && m.Model == "" }

func containsDotDot(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }
