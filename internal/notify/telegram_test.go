package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func static(v string) func() (string, error) { return func() (string, error) { return v, nil } }

func TestSendMessage(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	tg := New(srv.URL, static("123:SECRET"), static("42"), slog.Default())
	if err := tg.Send(context.Background(), Destination{}, "bonjour"); err != nil {
		t.Fatal(err)
	}
	if path != "/bot123:SECRET/sendMessage" || got["chat_id"] != "42" || got["text"] != "bonjour" || got["message_thread_id"] != nil {
		t.Fatalf("requête: %s %v", path, got)
	}
	// chat par défaut : le nom du projet préfixe le message, le sujet est ignoré
	if err := tg.Send(context.Background(), Destination{ThreadID: 9, Label: "FraisZen"}, "PR ouverte"); err != nil {
		t.Fatal(err)
	}
	if got["chat_id"] != "42" || got["text"] != "[FraisZen] PR ouverte" || got["message_thread_id"] != nil {
		t.Fatalf("défaut : %v", got)
	}
	// canal du projet : groupe + sujet, sans préfixe
	if err := tg.Send(context.Background(), Destination{ChatID: "-1004205411589", ThreadID: 13, Label: "FraisZen"}, "PR ouverte"); err != nil {
		t.Fatal(err)
	}
	if got["chat_id"] != "-1004205411589" || got["text"] != "PR ouverte" || got["message_thread_id"] != float64(13) {
		t.Fatalf("canal du projet : %v", got)
	}
}

func TestFailureIsLoggedWithoutToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer srv.Close()
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	tg := New(srv.URL, static("123:SECRET"), static("42"), log)
	tg.Async = false
	tg.Notify(context.Background(), Destination{}, "x") // ne doit ni paniquer ni bloquer
	if !strings.Contains(buf.String(), "HTTP 500") || strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("log: %s", buf.String())
	}
	// injoignable : l'URL (qui contient le token) ne doit pas fuiter
	buf.Reset()
	tg2 := New("http://127.0.0.1:1", static("123:SECRET"), static("42"), log)
	tg2.Async = false
	tg2.Notify(context.Background(), Destination{}, "x")
	if !strings.Contains(buf.String(), "injoignable") || strings.Contains(buf.String(), "SECRET") {
		t.Fatalf("log: %s", buf.String())
	}
}

func TestNotConfigured(t *testing.T) {
	tg := New("http://x", func() (string, error) { return "", errors.New("absent") }, static("1"), slog.Default())
	if err := tg.Send(context.Background(), Destination{}, "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatal(err)
	}
	// sans chat par défaut : le canal d'un projet suffit, le chat par défaut est refusé
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"ok":true}`)) }))
	defer srv.Close()
	noDefault := New(srv.URL, static("123:SECRET"), func() (string, error) { return "", errors.New("absent") }, slog.Default())
	if err := noDefault.Send(context.Background(), Destination{}, "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("chat par défaut absent : %v", err)
	}
	if err := noDefault.Send(context.Background(), Destination{ChatID: "-100"}, "x"); err != nil {
		t.Fatalf("canal du projet : %v", err)
	}
	if err := noDefault.Check(context.Background()); err != nil {
		t.Fatalf("Check sans chat par défaut : %v", err)
	}
}
