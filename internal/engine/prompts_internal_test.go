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

func TestToolRolePromptsAreContextFrugalAndUseRemoteBase(t *testing.T) {
	p := &store.Project{Name: "P", Repository: "o/r", BaseBranch: "develop", TestCommand: "make test"}
	tk := &store.Ticket{Key: "p-F1-T1", Title: "T", Body: "b", Acceptance: []byte(`["c"]`)}
	sha := "0123456789abcdef0123456789abcdef01234567"
	type role struct {
		name         string
		instr, input string
	}
	var roles []role
	i, in := developerPrompt(p, tk, "/w/p-F1-T1", "aos/p-F1-T1", "")
	roles = append(roles, role{"développeur", i, in})
	i, in = reviewerPrompt(p, tk, "/w/p-F1-T1-review", sha)
	roles = append(roles, role{"reviewer", i, in})
	i, in = qaPrompt(p, tk, "/w/p-F1-T1-qa", sha)
	roles = append(roles, role{"qa", i, in})
	i, in = techLeadPrompt(p, &store.Feature{Title: "F"}, "")
	roles = append(roles, role{"tech lead", i, in})
	for _, r := range roles {
		for _, want := range []string{"Économie de contexte", "une seule fois chacune", "offset/limit", "--stat ou --name-only",
			"| tail -n 60", "puis la suite complète une seule fois", "regroupe les commandes indépendantes", "se lit en une fois"} {
			if !strings.Contains(r.instr, want) {
				t.Fatalf("%s : consigne absente %q", r.name, want)
			}
		}
		if r.name == "tech lead" {
			continue
		}
		if !strings.Contains(r.instr, "origin/develop (jamais la branche locale develop") || !strings.Contains(r.input, "Base : origin/develop") {
			t.Fatalf("%s : base distante non imposée\n%s\n%s", r.name, r.instr, r.input)
		}
		// Les instructions restent identiques d'un run à l'autre (préfixe réutilisable en cache) : ni SHA ni worktree.
		if strings.Contains(r.instr, sha) || strings.Contains(r.instr, "/w/") {
			t.Fatalf("%s : donnée propre au run dans les instructions", r.name)
		}
	}
	if !strings.Contains(roles[1].input, "git diff --stat origin/develop..."+sha+" && git diff origin/develop..."+sha+" | head -n 400") {
		t.Fatalf("reviewer : première commande absente\n%s", roles[1].input)
	}
	// Instructions du développeur : la règle d'économie ajoute peu (≈ 1 000 caractères).
	if n := len(roles[0].instr); n > 3500 {
		t.Fatalf("instructions du développeur trop longues : %d caractères", n)
	}
}
