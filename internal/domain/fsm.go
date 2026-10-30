// Package domain contient les entités et les machines à états pures d'Agentic OS.
// Aucune dépendance d'E/S : tout est testable de façon déterministe.
package domain

import (
	"errors"
	"fmt"
)

// ErrTransition est renvoyée (enveloppée) pour toute transition non listée.
var ErrTransition = errors.New("transition refusée")

// TransitionError décrit une transition refusée.
type TransitionError struct {
	Entity string
	From   string
	Event  string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("transition refusée pour %s : état %s, événement %s", e.Entity, e.From, e.Event)
}

func (e *TransitionError) Unwrap() error { return ErrTransition }

// ---------------------------------------------------------------- Feature

// FeatureState est l'état d'une feature.
type FeatureState string

const (
	FeatureIdea             FeatureState = "IDEA"
	FeatureSpec             FeatureState = "SPEC"
	FeatureSpecReview       FeatureState = "SPEC_REVIEW"
	FeatureTechDesign       FeatureState = "TECH_DESIGN"
	FeatureTechDesignReview FeatureState = "TECH_DESIGN_REVIEW"
	FeatureIssuesOpened     FeatureState = "ISSUES_OPENED"
	FeatureHumanGate        FeatureState = "HUMAN_GATE"
	FeatureReadyForDev      FeatureState = "READY_FOR_DEV"
	FeatureInDev            FeatureState = "IN_DEV"
	// FeatureAcceptance : mode branche de feature, tous les tickets sont intégrés dans la branche ;
	// recette humaine puis merge de la PR finale vers la base.
	FeatureAcceptance FeatureState = "ACCEPTANCE"
	FeatureDone       FeatureState = "DONE"
	FeatureCancelled  FeatureState = "CANCELLED"
	FeatureBlocked    FeatureState = "BLOCKED"
)

// AllFeatureStates liste les états dans l'ordre du pipeline.
var AllFeatureStates = []FeatureState{
	FeatureIdea, FeatureSpec, FeatureSpecReview, FeatureTechDesign, FeatureTechDesignReview,
	FeatureIssuesOpened, FeatureHumanGate, FeatureReadyForDev, FeatureInDev, FeatureAcceptance, FeatureDone,
	FeatureCancelled, FeatureBlocked,
}

// Valid indique si l'état est connu.
func (s FeatureState) Valid() bool {
	for _, x := range AllFeatureStates {
		if x == s {
			return true
		}
	}
	return false
}

// Terminal indique un état final.
func (s FeatureState) Terminal() bool { return s == FeatureDone || s == FeatureCancelled }

// FeatureEvent est un événement appliqué à une feature.
type FeatureEvent string

const (
	FeatureEvStartSpec       FeatureEvent = "start_spec"
	FeatureEvSpecReady       FeatureEvent = "spec_ready"
	FeatureEvSpecFailed      FeatureEvent = "spec_failed"
	FeatureEvSpecApproved    FeatureEvent = "spec_approved"
	FeatureEvSpecRejected    FeatureEvent = "spec_rejected"
	FeatureEvDesignReady     FeatureEvent = "design_ready"
	FeatureEvDesignApproved  FeatureEvent = "design_approved"
	FeatureEvDesignRejected  FeatureEvent = "design_rejected"
	FeatureEvIssuesOpened    FeatureEvent = "issues_opened"
	FeatureEvDevGateApproved FeatureEvent = "dev_gate_approved"
	FeatureEvDevGateRejected FeatureEvent = "dev_gate_rejected"
	FeatureEvTicketStarted   FeatureEvent = "ticket_started"
	FeatureEvAllTicketsDone  FeatureEvent = "all_tickets_done"
	// FeatureEvTicketsIntegrated : mode branche de feature, tous les tickets sont terminaux et au
	// moins un a été mergé dans la branche de feature.
	FeatureEvTicketsIntegrated FeatureEvent = "tickets_integrated"
	// FeatureEvFeaturePRMerged : PR finale (branche de feature → base) mergée par un humain.
	FeatureEvFeaturePRMerged FeatureEvent = "feature_pr_merged"
	// FeatureEvCorrectionRequested : retour de recette humaine, un ticket de correction est ajouté
	// sur la branche de feature (recette, ou blocage depuis la recette → tickets en cours).
	FeatureEvCorrectionRequested FeatureEvent = "correction_requested"
	FeatureEvFail                FeatureEvent = "fail"
	FeatureEvCancel              FeatureEvent = "cancel"
	FeatureEvRetry               FeatureEvent = "retry"
)

type featureKey struct {
	from FeatureState
	ev   FeatureEvent
}

// featureTransitions est la table exhaustive (hors cancel/retry traités à part).
var featureTransitions = map[featureKey]FeatureState{
	{FeatureIdea, FeatureEvStartSpec}:                  FeatureSpec,
	{FeatureSpec, FeatureEvSpecReady}:                  FeatureSpecReview,
	{FeatureSpec, FeatureEvSpecFailed}:                 FeatureBlocked,
	{FeatureSpec, FeatureEvFail}:                       FeatureBlocked,
	{FeatureSpecReview, FeatureEvSpecApproved}:         FeatureTechDesign,
	{FeatureSpecReview, FeatureEvSpecRejected}:         FeatureSpec,
	{FeatureTechDesign, FeatureEvDesignReady}:          FeatureTechDesignReview,
	{FeatureTechDesign, FeatureEvFail}:                 FeatureBlocked,
	{FeatureTechDesignReview, FeatureEvDesignApproved}: FeatureIssuesOpened,
	{FeatureTechDesignReview, FeatureEvDesignRejected}: FeatureTechDesign,
	{FeatureIssuesOpened, FeatureEvIssuesOpened}:       FeatureHumanGate,
	{FeatureIssuesOpened, FeatureEvFail}:               FeatureBlocked,
	{FeatureHumanGate, FeatureEvDevGateApproved}:       FeatureReadyForDev,
	{FeatureHumanGate, FeatureEvDevGateRejected}:       FeatureCancelled,
	{FeatureReadyForDev, FeatureEvTicketStarted}:       FeatureInDev,
	{FeatureReadyForDev, FeatureEvAllTicketsDone}:      FeatureDone,
	{FeatureInDev, FeatureEvAllTicketsDone}:            FeatureDone,
	{FeatureReadyForDev, FeatureEvTicketsIntegrated}:   FeatureAcceptance,
	{FeatureInDev, FeatureEvTicketsIntegrated}:         FeatureAcceptance,
	{FeatureAcceptance, FeatureEvFeaturePRMerged}:      FeatureDone,
	{FeatureAcceptance, FeatureEvFail}:                 FeatureBlocked,
	{FeatureAcceptance, FeatureEvCorrectionRequested}:  FeatureInDev,
	{FeatureBlocked, FeatureEvCorrectionRequested}:     FeatureInDev,
}

// FeatureRetryTargets : états depuis lesquels une feature peut être bloquée puis reprise.
var FeatureRetryTargets = []FeatureState{FeatureSpec, FeatureTechDesign, FeatureIssuesOpened, FeatureAcceptance}

// ApplyFeature applique un événement ; toute transition non listée est refusée.
// L'événement retry doit passer par ResumeFeature (il dépend de l'état précédent).
func ApplyFeature(from FeatureState, ev FeatureEvent) (FeatureState, error) {
	refuse := &TransitionError{Entity: "feature", From: string(from), Event: string(ev)}
	if !from.Valid() {
		return from, refuse
	}
	if ev == FeatureEvCancel {
		if from.Terminal() {
			return from, refuse
		}
		return FeatureCancelled, nil
	}
	to, ok := featureTransitions[featureKey{from, ev}]
	if !ok {
		return from, refuse
	}
	return to, nil
}

// ResumeFeature implémente BLOCKED --retry--> état précédent.
func ResumeFeature(from, previous FeatureState) (FeatureState, error) {
	refuse := &TransitionError{Entity: "feature", From: string(from), Event: string(FeatureEvRetry)}
	if from != FeatureBlocked {
		return from, refuse
	}
	for _, s := range FeatureRetryTargets {
		if s == previous {
			return previous, nil
		}
	}
	return from, refuse
}

// ---------------------------------------------------------------- Ticket

// TicketState est l'état d'un ticket.
type TicketState string

const (
	TicketPlanned            TicketState = "PLANNED"
	TicketReadyForDev        TicketState = "READY_FOR_DEV"
	TicketDev                TicketState = "DEV"
	TicketReview             TicketState = "REVIEW"
	TicketQA                 TicketState = "QA"
	TicketChangesRequested   TicketState = "CHANGES_REQUESTED"
	TicketAwaitingPRApproval TicketState = "AWAITING_PR_APPROVAL"
	TicketPROpened           TicketState = "PR_OPENED"
	TicketHumanMerge         TicketState = "HUMAN_MERGE"
	TicketDone               TicketState = "DONE"
	TicketCancelled          TicketState = "CANCELLED"
	TicketBlocked            TicketState = "BLOCKED"
)

// AllTicketStates liste les états dans l'ordre du pipeline.
var AllTicketStates = []TicketState{
	TicketPlanned, TicketReadyForDev, TicketDev, TicketReview, TicketQA, TicketChangesRequested,
	TicketAwaitingPRApproval, TicketPROpened, TicketHumanMerge, TicketDone, TicketCancelled, TicketBlocked,
}

// Valid indique si l'état est connu.
func (s TicketState) Valid() bool {
	for _, x := range AllTicketStates {
		if x == s {
			return true
		}
	}
	return false
}

// Terminal indique un état final.
func (s TicketState) Terminal() bool { return s == TicketDone || s == TicketCancelled }

// TicketEvent est un événement appliqué à un ticket.
type TicketEvent string

const (
	TicketEvReady           TicketEvent = "ready"
	TicketEvDevStarted      TicketEvent = "dev_started"
	TicketEvDevPushed       TicketEvent = "dev_pushed"
	TicketEvReviewApproved  TicketEvent = "review_approved"
	TicketEvReviewChanges   TicketEvent = "review_changes_requested"
	TicketEvQAPassed        TicketEvent = "qa_passed"
	TicketEvQAFailed        TicketEvent = "qa_failed"
	TicketEvReworkApproved  TicketEvent = "rework_approved"
	TicketEvReworkRejected  TicketEvent = "rework_rejected"
	TicketEvReworkExhausted TicketEvent = "rework_exhausted"
	TicketEvPRApproved      TicketEvent = "pr_approved"
	TicketEvPRRejected      TicketEvent = "pr_rejected"
	TicketEvCISuccess       TicketEvent = "ci_success"
	TicketEvPRMerged        TicketEvent = "pr_merged"
	TicketEvPRClosed        TicketEvent = "pr_closed"
	TicketEvFail            TicketEvent = "fail"
	TicketEvCancel          TicketEvent = "cancel"
	TicketEvRetry           TicketEvent = "retry"
)

type ticketKey struct {
	from TicketState
	ev   TicketEvent
}

var ticketTransitions = map[ticketKey]TicketState{
	{TicketPlanned, TicketEvReady}:                    TicketReadyForDev,
	{TicketReadyForDev, TicketEvDevStarted}:           TicketDev,
	{TicketDev, TicketEvDevPushed}:                    TicketReview,
	{TicketReview, TicketEvReviewApproved}:            TicketQA,
	{TicketReview, TicketEvReviewChanges}:             TicketChangesRequested,
	{TicketQA, TicketEvQAPassed}:                      TicketAwaitingPRApproval,
	{TicketQA, TicketEvQAFailed}:                      TicketChangesRequested,
	{TicketChangesRequested, TicketEvReworkApproved}:  TicketDev,
	{TicketChangesRequested, TicketEvReworkRejected}:  TicketCancelled,
	{TicketChangesRequested, TicketEvReworkExhausted}: TicketBlocked,
	{TicketAwaitingPRApproval, TicketEvPRApproved}:    TicketPROpened,
	{TicketAwaitingPRApproval, TicketEvPRRejected}:    TicketCancelled,
	{TicketPROpened, TicketEvCISuccess}:               TicketHumanMerge,
	{TicketPROpened, TicketEvPRMerged}:                TicketDone,
	{TicketHumanMerge, TicketEvPRMerged}:              TicketDone,
	{TicketPROpened, TicketEvPRClosed}:                TicketCancelled,
	{TicketHumanMerge, TicketEvPRClosed}:              TicketCancelled,
	{TicketReadyForDev, TicketEvFail}:                 TicketBlocked,
	{TicketDev, TicketEvFail}:                         TicketBlocked,
	{TicketReview, TicketEvFail}:                      TicketBlocked,
	{TicketQA, TicketEvFail}:                          TicketBlocked,
	{TicketPROpened, TicketEvFail}:                    TicketBlocked,
}

// TicketRetryTargets : états précédents valides pour une reprise.
var TicketRetryTargets = []TicketState{
	TicketReadyForDev, TicketDev, TicketReview, TicketQA, TicketPROpened, TicketChangesRequested,
}

// ApplyTicket applique un événement ; toute transition non listée est refusée.
func ApplyTicket(from TicketState, ev TicketEvent) (TicketState, error) {
	refuse := &TransitionError{Entity: "ticket", From: string(from), Event: string(ev)}
	if !from.Valid() {
		return from, refuse
	}
	if ev == TicketEvCancel {
		if from.Terminal() {
			return from, refuse
		}
		return TicketCancelled, nil
	}
	to, ok := ticketTransitions[ticketKey{from, ev}]
	if !ok {
		return from, refuse
	}
	return to, nil
}

// ApplyTicketRework applique rework_approved avec sa garde attempt < max.
func ApplyTicketRework(from TicketState, attempt, maxRework int) (TicketState, error) {
	if attempt >= maxRework {
		return from, &TransitionError{Entity: "ticket", From: string(from), Event: string(TicketEvReworkApproved) + " (nombre maximal de reworks atteint)"}
	}
	return ApplyTicket(from, TicketEvReworkApproved)
}

// ResumeTicket implémente BLOCKED --retry--> état précédent.
func ResumeTicket(from, previous TicketState) (TicketState, error) {
	refuse := &TransitionError{Entity: "ticket", From: string(from), Event: string(TicketEvRetry)}
	if from != TicketBlocked {
		return from, refuse
	}
	for _, s := range TicketRetryTargets {
		if s == previous {
			return previous, nil
		}
	}
	return from, refuse
}
