package domain

import (
	"errors"
	"testing"
)

func TestFeatureAllowedTransitions(t *testing.T) {
	cases := []struct {
		from FeatureState
		ev   FeatureEvent
		to   FeatureState
	}{
		{FeatureIdea, FeatureEvStartSpec, FeatureSpec},
		{FeatureSpec, FeatureEvSpecReady, FeatureSpecReview},
		{FeatureSpec, FeatureEvSpecFailed, FeatureBlocked},
		{FeatureSpec, FeatureEvFail, FeatureBlocked},
		{FeatureSpecReview, FeatureEvSpecApproved, FeatureTechDesign},
		{FeatureSpecReview, FeatureEvSpecRejected, FeatureSpec},
		{FeatureTechDesign, FeatureEvDesignReady, FeatureTechDesignReview},
		{FeatureTechDesign, FeatureEvFail, FeatureBlocked},
		{FeatureTechDesignReview, FeatureEvDesignApproved, FeatureIssuesOpened},
		{FeatureTechDesignReview, FeatureEvDesignRejected, FeatureTechDesign},
		{FeatureIssuesOpened, FeatureEvIssuesOpened, FeatureHumanGate},
		{FeatureIssuesOpened, FeatureEvFail, FeatureBlocked},
		{FeatureHumanGate, FeatureEvDevGateApproved, FeatureReadyForDev},
		{FeatureHumanGate, FeatureEvDevGateRejected, FeatureCancelled},
		{FeatureReadyForDev, FeatureEvTicketStarted, FeatureInDev},
		{FeatureReadyForDev, FeatureEvAllTicketsDone, FeatureDone},
		{FeatureInDev, FeatureEvAllTicketsDone, FeatureDone},
		{FeatureReadyForDev, FeatureEvTicketsIntegrated, FeatureAcceptance},
		{FeatureInDev, FeatureEvTicketsIntegrated, FeatureAcceptance},
		{FeatureAcceptance, FeatureEvFeaturePRMerged, FeatureDone},
		{FeatureAcceptance, FeatureEvCorrectionRequested, FeatureInDev},
		{FeatureBlocked, FeatureEvCorrectionRequested, FeatureInDev},
		{FeatureAcceptance, FeatureEvFail, FeatureBlocked},
	}
	for _, s := range AllFeatureStates {
		if !s.Terminal() {
			cases = append(cases, struct {
				from FeatureState
				ev   FeatureEvent
				to   FeatureState
			}{s, FeatureEvCancel, FeatureCancelled})
		}
	}
	for _, c := range cases {
		t.Run(string(c.from)+"/"+string(c.ev), func(t *testing.T) {
			got, err := ApplyFeature(c.from, c.ev)
			if err != nil || got != c.to {
				t.Fatalf("ApplyFeature(%s,%s) = %s,%v ; attendu %s", c.from, c.ev, got, err, c.to)
			}
		})
	}
}

func TestFeatureRefusesUnlistedTransitions(t *testing.T) {
	events := []FeatureEvent{
		FeatureEvStartSpec, FeatureEvSpecReady, FeatureEvSpecFailed, FeatureEvSpecApproved, FeatureEvSpecRejected,
		FeatureEvDesignReady, FeatureEvDesignApproved, FeatureEvDesignRejected, FeatureEvIssuesOpened,
		FeatureEvDevGateApproved, FeatureEvDevGateRejected, FeatureEvTicketStarted, FeatureEvAllTicketsDone,
		FeatureEvTicketsIntegrated, FeatureEvFeaturePRMerged, FeatureEvCorrectionRequested, FeatureEvFail, FeatureEvCancel, FeatureEvRetry, FeatureEvent("inconnu"),
	}
	allowed := 0
	for _, s := range AllFeatureStates {
		for _, ev := range events {
			_, listed := featureTransitions[featureKey{s, ev}]
			if ev == FeatureEvCancel && !s.Terminal() {
				listed = true
			}
			got, err := ApplyFeature(s, ev)
			if listed {
				allowed++
				if err != nil {
					t.Errorf("%s/%s devrait être autorisée : %v", s, ev, err)
				}
				continue
			}
			if err == nil || !errors.Is(err, ErrTransition) {
				t.Errorf("%s/%s devrait être refusée, obtenu %s", s, ev, got)
			}
			if got != s {
				t.Errorf("%s/%s : l'état ne doit pas changer en cas de refus", s, ev)
			}
		}
	}
	if allowed != len(featureTransitions)+11 { // annulation depuis chaque état non terminal (11)
		t.Fatalf("nombre de transitions autorisées inattendu: %d", allowed)
	}
	if _, err := ApplyFeature(FeatureState("NOPE"), FeatureEvCancel); err == nil {
		t.Fatal("état inconnu accepté")
	}
}

func TestFeatureResume(t *testing.T) {
	for _, prev := range FeatureRetryTargets {
		got, err := ResumeFeature(FeatureBlocked, prev)
		if err != nil || got != prev {
			t.Fatalf("resume vers %s: %s %v", prev, got, err)
		}
	}
	for _, prev := range []FeatureState{FeatureIdea, FeatureDone, FeatureBlocked, FeatureHumanGate, ""} {
		if _, err := ResumeFeature(FeatureBlocked, prev); err == nil {
			t.Fatalf("resume vers %s devrait être refusé", prev)
		}
	}
	if _, err := ResumeFeature(FeatureSpec, FeatureSpec); err == nil {
		t.Fatal("retry hors BLOCKED accepté")
	}
}

func TestTicketAllowedTransitions(t *testing.T) {
	cases := []struct {
		from TicketState
		ev   TicketEvent
		to   TicketState
	}{
		{TicketPlanned, TicketEvReady, TicketReadyForDev},
		{TicketReadyForDev, TicketEvDevStarted, TicketDev},
		{TicketDev, TicketEvDevPushed, TicketReview},
		{TicketReview, TicketEvReviewApproved, TicketQA},
		{TicketReview, TicketEvReviewChanges, TicketChangesRequested},
		{TicketQA, TicketEvQAPassed, TicketAwaitingPRApproval},
		{TicketQA, TicketEvQAFailed, TicketChangesRequested},
		{TicketChangesRequested, TicketEvReworkApproved, TicketDev},
		{TicketChangesRequested, TicketEvReworkRejected, TicketCancelled},
		{TicketChangesRequested, TicketEvReworkExhausted, TicketBlocked},
		{TicketAwaitingPRApproval, TicketEvPRApproved, TicketPROpened},
		{TicketAwaitingPRApproval, TicketEvPRRejected, TicketCancelled},
		{TicketPROpened, TicketEvCISuccess, TicketHumanMerge},
		{TicketPROpened, TicketEvPRMerged, TicketDone},
		{TicketHumanMerge, TicketEvPRMerged, TicketDone},
		{TicketPROpened, TicketEvPRClosed, TicketCancelled},
		{TicketHumanMerge, TicketEvPRClosed, TicketCancelled},
		{TicketReadyForDev, TicketEvFail, TicketBlocked},
		{TicketDev, TicketEvFail, TicketBlocked},
		{TicketReview, TicketEvFail, TicketBlocked},
		{TicketQA, TicketEvFail, TicketBlocked},
		{TicketPROpened, TicketEvFail, TicketBlocked},
	}
	for _, c := range cases {
		got, err := ApplyTicket(c.from, c.ev)
		if err != nil || got != c.to {
			t.Errorf("ApplyTicket(%s,%s) = %s,%v ; attendu %s", c.from, c.ev, got, err, c.to)
		}
	}
	if len(cases) != len(ticketTransitions) {
		t.Fatalf("table de test incomplète: %d cas pour %d transitions", len(cases), len(ticketTransitions))
	}
	for _, s := range AllTicketStates {
		got, err := ApplyTicket(s, TicketEvCancel)
		if s.Terminal() {
			if err == nil {
				t.Errorf("cancel depuis %s accepté", s)
			}
		} else if err != nil || got != TicketCancelled {
			t.Errorf("cancel depuis %s refusé: %v", s, err)
		}
	}
}

func TestTicketRefusesUnlistedTransitions(t *testing.T) {
	events := []TicketEvent{
		TicketEvReady, TicketEvDevStarted, TicketEvDevPushed, TicketEvReviewApproved, TicketEvReviewChanges,
		TicketEvQAPassed, TicketEvQAFailed, TicketEvReworkApproved, TicketEvReworkRejected, TicketEvReworkExhausted,
		TicketEvPRApproved, TicketEvPRRejected, TicketEvCISuccess, TicketEvPRMerged, TicketEvPRClosed,
		TicketEvFail, TicketEvRetry, TicketEvent("x"),
	}
	for _, s := range AllTicketStates {
		for _, ev := range events {
			if _, listed := ticketTransitions[ticketKey{s, ev}]; listed {
				continue
			}
			got, err := ApplyTicket(s, ev)
			if !errors.Is(err, ErrTransition) || got != s {
				t.Errorf("%s/%s devrait être refusée (obtenu %s, %v)", s, ev, got, err)
			}
		}
	}
}

func TestTicketReworkGuard(t *testing.T) {
	cases := []struct {
		attempt, max int
		ok           bool
	}{{0, 3, true}, {2, 3, true}, {3, 3, false}, {4, 3, false}, {0, 0, false}}
	for _, c := range cases {
		got, err := ApplyTicketRework(TicketChangesRequested, c.attempt, c.max)
		if c.ok && (err != nil || got != TicketDev) {
			t.Errorf("rework %d/%d refusé: %v", c.attempt, c.max, err)
		}
		if !c.ok && err == nil {
			t.Errorf("rework %d/%d accepté", c.attempt, c.max)
		}
	}
	if _, err := ApplyTicketRework(TicketQA, 0, 3); err == nil {
		t.Fatal("rework depuis QA accepté")
	}
}

func TestTicketResume(t *testing.T) {
	for _, prev := range TicketRetryTargets {
		if got, err := ResumeTicket(TicketBlocked, prev); err != nil || got != prev {
			t.Fatalf("resume %s: %v", prev, err)
		}
	}
	for _, prev := range []TicketState{TicketDone, TicketCancelled, TicketPlanned, TicketHumanMerge, TicketAwaitingPRApproval} {
		if _, err := ResumeTicket(TicketBlocked, prev); err == nil {
			t.Fatalf("resume vers %s accepté", prev)
		}
	}
	if _, err := ResumeTicket(TicketDev, TicketDev); err == nil {
		t.Fatal("retry hors BLOCKED accepté")
	}
}

func TestValidators(t *testing.T) {
	good := map[string]func(string) bool{
		"demo":           ValidSlug,
		"owner/repo":     ValidRepository,
		"aos/demo-F1-T1": ValidBranch,
		"aos-dev":        ValidProfile,
		"demo-F12-T3":    ValidTicketKey,
		"0123456789abcdef0123456789abcdef01234567": ValidSHA,
	}
	for v, f := range good {
		if !f(v) {
			t.Errorf("%q devrait être valide", v)
		}
	}
	bad := map[string]func(string) bool{
		"../x":     ValidSlug,
		"owner/..": ValidRepository,
		"a/../b":   ValidBranch,
		"x.lock":   ValidBranch,
		"Bad Name": ValidProfile,
		"../etc":   ValidProfile,
		"demo-F1":  ValidTicketKey,
		"abc":      ValidSHA,
		"-leading": ValidBranch,
	}
	for v, f := range bad {
		if f(v) {
			t.Errorf("%q devrait être invalide", v)
		}
	}
}

func TestFeatureBranchName(t *testing.T) {
	cases := map[string]string{
		"Alerte Frais réels potentiellement avantageux": "feature/F6-alerte-frais-reels-potentiellement-avant",
		"  Export CSV (v2) !  ":                         "feature/F6-export-csv-v2",
		"Œuvre à part":                                  "feature/F6-oeuvre-a-part",
		"!!!":                                           "feature/F6",
	}
	for title, want := range cases {
		got := FeatureBranchName(6, title)
		if got != want {
			t.Errorf("%q → %q, attendu %q", title, got, want)
		}
		if !ValidBranch(got) {
			t.Errorf("%q : branche invalide", got)
		}
	}
	for _, m := range []string{IntegrationDirect, IntegrationFeatureBranch} {
		if !ValidIntegrationMode(m) {
			t.Fatal(m)
		}
	}
	if ValidIntegrationMode("") || ValidIntegrationMode("branch") {
		t.Fatal("mode inconnu accepté")
	}
}
