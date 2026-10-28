package domain

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Règles de saisie partagées avec l'UI : le contrat est fixé par
// web/src/test/fixtures/validation-cases.json, testé côté Go et côté TypeScript.
// Les longueurs sont comptées en caractères (points de code), pas en octets.

// Bornes de saisie.
const (
	MaxProjectNameLen  = 200
	MaxTestCommandLen  = 500
	MaxRequiredChecks  = 20
	MaxFeatureTitleLen = 200
	MaxFeatureDescLen  = 20000
	MinTokensPerRun    = 1000
	MaxTokensPerRunCap = 50_000_000
)

func runes(s string) int { return utf8.RuneCountInString(s) }

// ValidProjectName : non vide après trim, 200 caractères max.
func ValidProjectName(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && runes(t) <= MaxProjectNameLen
}

// ValidTestCommand : 500 caractères max (vide autorisé).
func ValidTestCommand(s string) bool { return runes(strings.TrimSpace(s)) <= MaxTestCommandLen }

// ValidFeatureTitle : non vide après trim, 200 caractères max.
func ValidFeatureTitle(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && runes(t) <= MaxFeatureTitleLen
}

// ValidFeatureDescription : non vide après trim (le PO s'en sert), 20 000 caractères max.
func ValidFeatureDescription(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && runes(t) <= MaxFeatureDescLen
}

// ValidDesiredModel : vide (aucun modèle imposé) ou nom de modèle valide.
func ValidDesiredModel(s string) bool { return s == "" || ValidModelName(s) }

// ValidMaxTokens : budget par run entre 1 000 et 50 000 000 tokens.
func ValidMaxTokens(n int) bool { return n >= MinTokensPerRun && n <= MaxTokensPerRunCap }

var telegramChatRe = regexp.MustCompile(`^-?[0-9]{1,20}$`)

// ValidTelegramChat : identifiant de chat Telegram (vide = chat par défaut). Les groupes sont négatifs.
func ValidTelegramChat(s string) bool { return s == "" || telegramChatRe.MatchString(s) }

// ValidTelegramThread : identifiant de sujet de forum (0 = aucun).
func ValidTelegramThread(n int64) bool { return n >= 0 && n <= 1<<31 }
