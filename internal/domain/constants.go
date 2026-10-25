package domain

import "regexp"

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
)

// JobKinds liste les types de jobs connus.
var JobKinds = []string{JobPOSpec, JobTechDesign, JobOpenIssues, JobDevelop, JobReview, JobQA, JobPublishPR, JobExecuteApproval, JobSyncAgents}

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
)

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

// ValidCheckName valide un nom de check CI requis.
func ValidCheckName(s string) bool { return checkNameRe.MatchString(s) }

// ValidModelName valide un nom de modèle.
func ValidModelName(s string) bool { return modelNameRe.MatchString(s) }

func containsDotDot(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '.' && s[i+1] == '.' {
			return true
		}
	}
	return false
}

func hasSuffix(s, suf string) bool { return len(s) >= len(suf) && s[len(s)-len(suf):] == suf }
