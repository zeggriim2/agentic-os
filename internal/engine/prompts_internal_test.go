package engine

import (
	"strings"
	"testing"

	"github.com/zeggriim2/agentic-os/internal/store"
)

func TestTechLeadPromptAsksForMinimalSplit(t *testing.T) {
	instr, _ := techLeadPrompt(&store.Project{Name: "P", Repository: "o/r", BaseBranch: "main"}, &store.Feature{Title: "F"}, "")
	for _, want := range []string{"le MOINS de tickets possible", "Une petite feature = 1 ticket", "5 tickets au maximum",
		"Pas de ticket d'analyse", "pas de ticket « tests » séparé"} {
		if !strings.Contains(instr, want) {
			t.Fatalf("consigne absente : %q", want)
		}
	}
	if strings.Contains(instr, "1 à 10") {
		t.Fatal("ancienne borne de 10 tickets encore présente")
	}
}
