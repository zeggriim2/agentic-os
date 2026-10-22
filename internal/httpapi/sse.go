package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zeggriim2/agentic-os/internal/store"
)

// Event est un événement temps réel diffusé en SSE.
type Event struct {
	Type string
	Data json.RawMessage
}

// Hub diffuse les événements aux abonnés SSE.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

// NewHub construit un hub.
func NewHub() *Hub { return &Hub{subs: map[chan Event]struct{}{}} }

// Subscribe renvoie un canal d'événements et sa fonction de désabonnement.
func (h *Hub) Subscribe() (chan Event, func()) {
	ch := make(chan Event, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// Publish diffuse sans bloquer (un abonné lent perd des événements).
func (h *Hub) Publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribers renvoie le nombre d'abonnés.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

var allowedEvents = map[string]bool{
	"job.updated": true, "run.updated": true, "run.event": true, "approval.updated": true,
	"feature.updated": true, "ticket.updated": true, "overview.invalidate": true,
}

// ListenPG relaie les NOTIFY PostgreSQL (émis au commit) vers le hub ; reconnexion automatique.
func ListenPG(ctx context.Context, pool *pgxpool.Pool, hub *Hub, log *slog.Logger) {
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, hub)
		if ctx.Err() != nil {
			return
		}
		log.Warn("écoute LISTEN interrompue, reconnexion", "error", fmt.Sprint(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, hub *Hub) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+store.Channel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var p struct {
			Type string `json:"type"`
			ID   int64  `json:"id"`
		}
		if json.Unmarshal([]byte(n.Payload), &p) != nil || !allowedEvents[p.Type] {
			continue
		}
		data, _ := json.Marshal(map[string]any{"id": p.ID})
		hub.Publish(Event{Type: p.Type, Data: data})
	}
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, 500, "streaming_unsupported", "flux non supporté")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	_, _ = fmt.Fprint(w, ": connecté\n\n")
	fl.Flush()
	ch, unsub := s.Hub.Subscribe()
	defer unsub()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case ev := <-ch:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
