// Package pgtest fournit une base PostgreSQL jetable pour les tests d'intégration.
// Les tests sont ignorés si AOS_TEST_DATABASE_URL n'est pas défini.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/zeggriim2/agentic-os/internal/store"
)

// New crée une base dédiée, applique les migrations et la supprime à la fin du test.
func New(t *testing.T) *store.Store {
	t.Helper()
	base := os.Getenv("AOS_TEST_DATABASE_URL")
	if base == "" {
		t.Skip("AOS_TEST_DATABASE_URL non défini : test d'intégration PostgreSQL ignoré")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connexion admin: %v", err)
	}
	defer admin.Close(context.Background())
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "aos_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("création base: %v", err)
	}
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	s, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	t.Cleanup(func() {
		s.Close()
		c, err := pgx.Connect(context.Background(), base)
		if err != nil {
			return
		}
		defer c.Close(context.Background())
		_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})
	return s
}
