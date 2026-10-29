package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// ErrInvalidDeliverable enveloppe toute erreur de validation de livrable.
var ErrInvalidDeliverable = errors.New("livrable invalide")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidDeliverable, fmt.Sprintf(format, a...))
}

const (
	maxStringLen = 20000
	maxListLen   = 200
)

// POSpec est le livrable du PO.
type POSpec struct {
	Summary            string   `json:"summary"`
	UserStories        []string `json:"user_stories"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	OpenQuestions      []string `json:"open_questions"`
}

// Decision est une décision technique.
type Decision struct {
	Decision             string   `json:"decision"`
	Rationale            string   `json:"rationale"`
	RejectedAlternatives []string `json:"rejected_alternatives"`
}

// DesignTicket est un ticket proposé par le Tech Lead.
type DesignTicket struct {
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

// TechDesign est le livrable du Tech Lead.
type TechDesign struct {
	Summary   string         `json:"summary"`
	Decisions []Decision     `json:"decisions"`
	Risks     []string       `json:"risks"`
	Tickets   []DesignTicket `json:"tickets"`
}

// CommandResult est une commande exécutée avec son code retour.
type CommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
}

// DevelopResult est le livrable du développeur.
type DevelopResult struct {
	Summary      string          `json:"summary"`
	FilesChanged []string        `json:"files_changed"`
	Tests        []CommandResult `json:"tests"`
	// Notes est facultatif : un simple commentaire manquant ne doit pas faire perdre un run entier.
	Notes string `json:"notes,omitempty"`
}

// Finding est une remarque de revue.
type Finding struct {
	Severity string `json:"severity"`
	File     string `json:"file"`
	Message  string `json:"message"`
}

// ReviewResult est le livrable du reviewer.
type ReviewResult struct {
	Verdict  string    `json:"verdict"`
	SHA      string    `json:"sha"`
	Findings []Finding `json:"findings"`
	Summary  string    `json:"summary"`
}

// AcceptanceCheck relie un critère à une preuve.
type AcceptanceCheck struct {
	Criterion string `json:"criterion"`
	Status    string `json:"status"`
	Evidence  string `json:"evidence"`
}

// QAResult est le livrable QA.
type QAResult struct {
	Verdict    string            `json:"verdict"`
	SHA        string            `json:"sha"`
	Checks     []CommandResult   `json:"checks"`
	Acceptance []AcceptanceCheck `json:"acceptance"`
	Summary    string            `json:"summary"`
}

var fenceRe = regexp.MustCompile("(?s)```json[ \t]*\r?\n(.*?)```")

// ExtractLastJSON renvoie le dernier bloc JSON (objet) de la sortie : bloc
// ```json … ``` en priorité, sinon le dernier objet brut de premier niveau.
func ExtractLastJSON(output string) ([]byte, error) {
	if ms := fenceRe.FindAllStringSubmatch(output, -1); len(ms) > 0 {
		b := bytes.TrimSpace([]byte(ms[len(ms)-1][1]))
		if len(b) == 0 || b[0] != '{' {
			return nil, invalid("le dernier bloc ```json``` n'est pas un objet")
		}
		return b, nil
	}
	// Objet brut : on part de la fin et on cherche l'objet de premier niveau
	// qui se termine le plus tard.
	bestStart, bestEnd := -1, -1
	tries := 0
	for i := len(output) - 1; i >= 0 && tries < 4000; i-- {
		if output[i] != '{' {
			continue
		}
		tries++
		dec := json.NewDecoder(strings.NewReader(output[i:]))
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			continue
		}
		end := i + int(dec.InputOffset())
		if bestStart < 0 || end >= bestEnd {
			// premier candidat, ou objet englobant le précédent
			bestStart, bestEnd = i, end
			continue
		}
		if end <= bestStart {
			// objet antérieur distinct : le dernier objet est trouvé.
			break
		}
	}
	if bestStart < 0 {
		return nil, invalid("aucun objet JSON trouvé dans la sortie")
	}
	return []byte(output[bestStart:bestEnd]), nil
}

// DecodeStrict décode data dans v (pointeur vers struct) : clés inconnues refusées,
// toutes les clés obligatoires, aucun null, aucune clé dupliquée, pas de données en trop.
func DecodeStrict(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return invalid("cible de décodage invalide")
	}
	if err := checkShape(json.RawMessage(data), rv.Elem().Type(), "$"); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return invalid("JSON non conforme : %v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalid("données en trop après l'objet JSON")
	}
	return nil
}

// checkShape vérifie récursivement clés exactes, absence de null et de doublons.
func checkShape(raw json.RawMessage, t reflect.Type, path string) error {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) || len(trimmed) == 0 {
		return invalid("%s : valeur nulle ou absente", path)
	}
	switch t.Kind() {
	case reflect.Struct:
		if trimmed[0] != '{' {
			return invalid("%s : objet attendu", path)
		}
		fields := map[string]reflect.Type{}
		optional := map[string]bool{} // clés marquées omitempty : peuvent être absentes, jamais nulles
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			parts := strings.Split(f.Tag.Get("json"), ",")
			name := parts[0]
			if name == "" || name == "-" {
				continue
			}
			fields[name] = f.Type
			optional[name] = slices.Contains(parts[1:], "omitempty")
		}
		obj, err := decodeObjectNoDup(trimmed, path)
		if err != nil {
			return err
		}
		for k := range obj {
			if _, ok := fields[k]; !ok {
				return invalid("%s : clé inconnue %q", path, k)
			}
		}
		for k, ft := range fields {
			v, ok := obj[k]
			if !ok {
				if optional[k] {
					continue
				}
				return invalid("%s : clé obligatoire %q absente", path, k)
			}
			if err := checkShape(v, ft, path+"."+k); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if trimmed[0] != '[' {
			return invalid("%s : tableau attendu", path)
		}
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return invalid("%s : tableau invalide", path)
		}
		if len(items) > maxListLen {
			return invalid("%s : trop d'éléments (max %d)", path, maxListLen)
		}
		for i, it := range items {
			if err := checkShape(it, t.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if trimmed[0] != '"' {
			return invalid("%s : chaîne attendue", path)
		}
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return invalid("%s : chaîne invalide", path)
		}
		if len(s) > maxStringLen {
			return invalid("%s : chaîne trop longue", path)
		}
	case reflect.Int, reflect.Int64:
		var n json.Number
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&n); err != nil {
			return invalid("%s : entier attendu", path)
		}
		if _, err := n.Int64(); err != nil {
			return invalid("%s : entier attendu", path)
		}
	}
	return nil
}

func decodeObjectNoDup(data []byte, path string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, invalid("%s : objet attendu", path)
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, invalid("%s : objet invalide", path)
		}
		k, _ := kt.(string)
		if _, dup := out[k]; dup {
			return nil, invalid("%s : clé dupliquée %q", path, k)
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, invalid("%s : valeur invalide pour %q", path, k)
		}
		out[k] = v
	}
	return out, nil
}

func nonEmpty(s, field string) error {
	if strings.TrimSpace(s) == "" {
		return invalid("%s ne doit pas être vide", field)
	}
	return nil
}

// ParsePOSpec extrait et valide le livrable PO.
func ParsePOSpec(output string) (*POSpec, error) {
	raw, err := ExtractLastJSON(output)
	if err != nil {
		return nil, err
	}
	var v POSpec
	if err := DecodeStrict(raw, &v); err != nil {
		return nil, err
	}
	if err := nonEmpty(v.Summary, "summary"); err != nil {
		return nil, err
	}
	if len(v.UserStories) == 0 {
		return nil, invalid("user_stories ne doit pas être vide")
	}
	if len(v.AcceptanceCriteria) == 0 {
		return nil, invalid("acceptance_criteria ne doit pas être vide")
	}
	return &v, nil
}

// MaxTickets borne le découpage d'une feature : les tickets sont développés l'un après
// l'autre et chacun produit une PR à merger ; au-delà, la feature doit être scindée.
const MaxTickets = 5

// ParseTechDesign extrait et valide le livrable Tech Lead (1..MaxTickets tickets).
func ParseTechDesign(output string) (*TechDesign, error) {
	raw, err := ExtractLastJSON(output)
	if err != nil {
		return nil, err
	}
	var v TechDesign
	if err := DecodeStrict(raw, &v); err != nil {
		return nil, err
	}
	if err := nonEmpty(v.Summary, "summary"); err != nil {
		return nil, err
	}
	if len(v.Tickets) < 1 || len(v.Tickets) > MaxTickets {
		return nil, invalid("tickets : entre 1 et %d attendus (reçu %d) ; regrouper les tickets ou scinder la feature", MaxTickets, len(v.Tickets))
	}
	for i, t := range v.Tickets {
		if strings.TrimSpace(t.Title) == "" || len(t.Title) > 200 {
			return nil, invalid("tickets[%d].title vide ou trop long", i)
		}
		if len(t.AcceptanceCriteria) == 0 {
			return nil, invalid("tickets[%d].acceptance_criteria ne doit pas être vide", i)
		}
		seen := make(map[string]struct{}, len(t.AcceptanceCriteria))
		for j, criterion := range t.AcceptanceCriteria {
			criterion = strings.TrimSpace(criterion)
			if criterion == "" {
				return nil, invalid("tickets[%d].acceptance_criteria[%d] vide", i, j)
			}
			if _, duplicate := seen[criterion]; duplicate {
				return nil, invalid("tickets[%d].acceptance_criteria[%d] dupliqué", i, j)
			}
			seen[criterion] = struct{}{}
			v.Tickets[i].AcceptanceCriteria[j] = criterion
		}
	}
	for i, d := range v.Decisions {
		if strings.TrimSpace(d.Decision) == "" {
			return nil, invalid("decisions[%d].decision vide", i)
		}
	}
	return &v, nil
}

// ParseDevelop extrait et valide le livrable développeur.
func ParseDevelop(output string) (*DevelopResult, error) {
	raw, err := ExtractLastJSON(output)
	if err != nil {
		return nil, err
	}
	var v DevelopResult
	if err := DecodeStrict(raw, &v); err != nil {
		return nil, err
	}
	if err := nonEmpty(v.Summary, "summary"); err != nil {
		return nil, err
	}
	return &v, nil
}

var severities = map[string]bool{"P0": true, "P1": true, "P2": true, "P3": true}

// ParseReview extrait et valide le livrable reviewer (fail-closed).
func ParseReview(output, expectedSHA string) (*ReviewResult, error) {
	raw, err := ExtractLastJSON(output)
	if err != nil {
		return nil, err
	}
	var v ReviewResult
	if err := DecodeStrict(raw, &v); err != nil {
		return nil, err
	}
	if v.Verdict != "approved" && v.Verdict != "changes_requested" {
		return nil, invalid("verdict %q hors allowlist", v.Verdict)
	}
	if !ValidSHA(expectedSHA) || v.SHA != expectedSHA {
		return nil, invalid("sha du livrable différent du head_sha attendu")
	}
	if err := nonEmpty(v.Summary, "summary"); err != nil {
		return nil, err
	}
	blocking := false
	for i, f := range v.Findings {
		if !severities[f.Severity] {
			return nil, invalid("findings[%d].severity %q hors allowlist", i, f.Severity)
		}
		if strings.TrimSpace(f.Message) == "" {
			return nil, invalid("findings[%d].message vide", i)
		}
		if f.Severity == "P0" || f.Severity == "P1" {
			blocking = true
		}
	}
	if v.Verdict == "approved" && blocking {
		return nil, invalid("verdict approved refusé : finding P0/P1 présent")
	}
	if v.Verdict == "changes_requested" && len(v.Findings) == 0 {
		return nil, invalid("changes_requested sans finding")
	}
	return &v, nil
}

// ParseQA extrait et valide le livrable QA (fail-closed). Chaque critère
// persistant doit apparaître exactement une fois dans le livrable.
func ParseQA(output, expectedSHA string, expectedAcceptance []string) (*QAResult, error) {
	raw, err := ExtractLastJSON(output)
	if err != nil {
		return nil, err
	}
	var v QAResult
	if err := DecodeStrict(raw, &v); err != nil {
		return nil, err
	}
	if v.Verdict != "passed" && v.Verdict != "failed" {
		return nil, invalid("verdict %q hors allowlist", v.Verdict)
	}
	if !ValidSHA(expectedSHA) || v.SHA != expectedSHA {
		return nil, invalid("sha du livrable différent du head_sha attendu")
	}
	if err := nonEmpty(v.Summary, "summary"); err != nil {
		return nil, err
	}
	allOK := true
	expected := make(map[string]struct{}, len(expectedAcceptance))
	for i, criterion := range expectedAcceptance {
		if criterion == "" {
			return nil, invalid("critère attendu [%d] vide", i)
		}
		if _, duplicate := expected[criterion]; duplicate {
			return nil, invalid("critère attendu dupliqué : %q", criterion)
		}
		expected[criterion] = struct{}{}
	}
	if len(expected) == 0 {
		return nil, invalid("aucun critère d'acceptation attendu")
	}
	for i, c := range v.Checks {
		if strings.TrimSpace(c.Command) == "" {
			return nil, invalid("checks[%d].command vide", i)
		}
		if c.ExitCode != 0 {
			allOK = false
		}
	}
	seen := make(map[string]struct{}, len(v.Acceptance))
	for i, a := range v.Acceptance {
		criterion := a.Criterion
		if _, ok := expected[criterion]; !ok {
			return nil, invalid("acceptance[%d].criterion %q inattendu", i, a.Criterion)
		}
		if _, duplicate := seen[criterion]; duplicate {
			return nil, invalid("acceptance[%d].criterion %q dupliqué", i, a.Criterion)
		}
		seen[criterion] = struct{}{}
		if a.Status != "met" && a.Status != "not_met" {
			return nil, invalid("acceptance[%d].status %q hors allowlist", i, a.Status)
		}
		if a.Status != "met" {
			allOK = false
		}
		if a.Status == "met" && strings.TrimSpace(a.Evidence) == "" {
			return nil, invalid("acceptance[%d] : preuve manquante", i)
		}
	}
	if len(seen) != len(expected) {
		missing := make([]string, 0, len(expected)-len(seen))
		for criterion := range expected {
			if _, ok := seen[criterion]; !ok {
				missing = append(missing, criterion)
			}
		}
		slices.Sort(missing)
		return nil, invalid("critères d'acceptation manquants : %s", strings.Join(missing, ", "))
	}
	if v.Verdict == "passed" {
		if len(v.Checks) == 0 || len(v.Acceptance) == 0 {
			return nil, invalid("passed refusé : checks et acceptance doivent être non vides")
		}
		if !allOK {
			return nil, invalid("passed refusé : check en échec ou critère not_met")
		}
	}
	return &v, nil
}

// RunOutcomeAcceptable implémente la règle fail-closed sur le run Hermes.
func RunOutcomeAcceptable(status string, partial, interrupted bool) error {
	if status != RunCompleted {
		return invalid("run non terminé avec succès (statut %s)", status)
	}
	if partial || interrupted {
		return invalid("run partiel ou interrompu")
	}
	return nil
}
