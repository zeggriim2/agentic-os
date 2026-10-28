// Package notify envoie des notifications Telegram (sendMessage). Un échec n'est jamais
// bloquant : il est journalisé sans le token.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Telegram est un notifieur Telegram.
type Telegram struct {
	BaseURL string
	Token   func() (string, error)
	ChatID  func() (string, error)
	HTTP    *http.Client
	Log     *slog.Logger
	// Async : si vrai, Notify envoie en arrière-plan.
	Async bool
}

// New construit un notifieur.
func New(baseURL string, token, chat func() (string, error), log *slog.Logger) *Telegram {
	return &Telegram{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, ChatID: chat,
		HTTP: &http.Client{Timeout: 10 * time.Second}, Log: log, Async: true}
}

// ErrNotConfigured indique l'absence de token ou de chat.
var ErrNotConfigured = errors.New("telegram non configuré")

// Destination désigne où envoyer : le canal d'un projet (groupe + sujet de forum éventuel) ou,
// ChatID vide, le chat par défaut. Label (nom du projet) préfixe le message envoyé au chat par
// défaut, pour distinguer les projets.
type Destination struct {
	ChatID   string
	ThreadID int64
	Label    string
}

// Send envoie un message (synchrone). L'erreur ne contient jamais le token.
func (t *Telegram) Send(ctx context.Context, to Destination, text string) error {
	token, err := t.Token()
	if err != nil || token == "" {
		return ErrNotConfigured
	}
	chat, thread := to.ChatID, to.ThreadID
	if chat == "" {
		if chat, err = t.ChatID(); err != nil || chat == "" {
			return ErrNotConfigured
		}
		thread = 0
		if to.Label != "" {
			text = "[" + to.Label + "] " + text
		}
	}
	if len(text) > 4000 {
		text = text[:4000]
	}
	msg := map[string]any{"chat_id": chat, "text": text, "disable_web_page_preview": true}
	if thread > 0 {
		msg["message_thread_id"] = thread
	}
	body, _ := json.Marshal(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.BaseURL+"/bot"+token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return errors.New("requête Telegram invalide")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("telegram injoignable: %s", redact(err.Error(), token))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telegram: HTTP %d", resp.StatusCode)
	}
	return nil
}

func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}

// Notify envoie sans bloquer le workflow ; un échec est seulement journalisé.
func (t *Telegram) Notify(ctx context.Context, to Destination, text string) {
	send := func() {
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := t.Send(c, to, text); err != nil {
			if errors.Is(err, ErrNotConfigured) {
				t.Log.Debug("notification Telegram ignorée (non configuré)")
				return
			}
			t.Log.Warn("notification Telegram en échec", "error", err.Error())
		}
	}
	if t.Async {
		go send()
		return
	}
	send()
}

// Check vérifie le token via getMe (doctor/health). Le chat par défaut est facultatif : les
// projets peuvent avoir leur propre canal.
func (t *Telegram) Check(ctx context.Context) error {
	token, err := t.Token()
	if err != nil || token == "" {
		return ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.BaseURL+"/bot"+token+"/getMe", nil)
	if err != nil {
		return errors.New("requête invalide")
	}
	resp, err := t.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("telegram injoignable: %s", redact(err.Error(), token))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("telegram: HTTP %d", resp.StatusCode)
	}
	return nil
}
