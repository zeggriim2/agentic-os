package domain

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Le même fichier est utilisé par web/src/test/validation.test.ts : toute divergence
// entre les validations de l'UI et de l'API casse l'un des deux tests.
const fixturePath = "../../web/src/test/fixtures/validation-cases.json"

type cases struct {
	Valid   []json.RawMessage `json:"valid"`
	Invalid []json.RawMessage `json:"invalid"`
}

func decodeValue(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var r struct {
		Repeat string `json:"repeat"`
		Count  int    `json:"count"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.Count <= 0 {
		t.Fatalf("valeur de fixture invalide: %s", raw)
	}
	return strings.Repeat(r.Repeat, r.Count)
}

func label(s string) string {
	if len([]rune(s)) > 20 {
		return fmt.Sprintf("%q…(%d car.)", string([]rune(s)[:5]), len([]rune(s)))
	}
	return fmt.Sprintf("%q", s)
}

func TestValidationContractSharedWithUI(t *testing.T) {
	b, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}
	strRules := map[string]func(string) bool{
		"slug":                ValidSlug,
		"repository":          ValidRepository,
		"branch":              ValidBranch,
		"project_name":        ValidProjectName,
		"test_command":        ValidTestCommand,
		"required_check":      ValidCheckName,
		"feature_title":       ValidFeatureTitle,
		"feature_description": ValidFeatureDescription,
		"model":               ValidDesiredModel,
	}
	for name, rule := range strRules {
		raw, ok := all[name]
		if !ok {
			t.Fatalf("règle %s absente de la fixture", name)
		}
		var c cases
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		for _, v := range c.Valid {
			if s := decodeValue(t, v); !rule(s) {
				t.Errorf("%s : %s devrait être valide", name, label(s))
			}
		}
		for _, v := range c.Invalid {
			if s := decodeValue(t, v); rule(s) {
				t.Errorf("%s : %s devrait être invalide", name, label(s))
			}
		}
	}
	var tokens struct {
		Valid   []int `json:"valid"`
		Invalid []int `json:"invalid"`
	}
	_ = json.Unmarshal(all["max_tokens"], &tokens)
	for _, n := range tokens.Valid {
		if !ValidMaxTokens(n) {
			t.Errorf("max_tokens %d devrait être valide", n)
		}
	}
	for _, n := range tokens.Invalid {
		if ValidMaxTokens(n) {
			t.Errorf("max_tokens %d devrait être invalide", n)
		}
	}
	var maxChecks int
	_ = json.Unmarshal(all["required_checks_max"], &maxChecks)
	if maxChecks != MaxRequiredChecks {
		t.Errorf("required_checks_max %d ≠ %d", maxChecks, MaxRequiredChecks)
	}
}
