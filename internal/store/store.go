// Package store accède à PostgreSQL (pgx, sans ORM) et applique les migrations embarquées.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/zeggriim2/agentic-os/migrations"
)

// Q est implémenté par *pgxpool.Pool et pgx.Tx.
type Q interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Erreurs.
var (
	ErrNotFound        = errors.New("introuvable")
	ErrVersionConflict = errors.New("conflit de version (modification concurrente)")
	ErrAlreadyDecided  = errors.New("approbation déjà décidée")
	ErrDuplicate       = errors.New("doublon")
)

// Store regroupe le pool de connexions.
type Store struct {
	Pool *pgxpool.Pool
}

// Open ouvre le pool.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("DSN PostgreSQL invalide")
	}
	cfg.MaxConns = 16
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connexion PostgreSQL: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("PostgreSQL injoignable: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close ferme le pool.
func (s *Store) Close() { s.Pool.Close() }

// InTx exécute fn dans une transaction (commit si nil, rollback sinon).
func (s *Store) InTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsUniqueViolation détecte une violation de contrainte d'unicité.
func IsUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

// ------------------------------------------------------------------ migrations

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("nom de migration invalide: %s", e.Name())
		}
		b, err := fs.ReadFile(migrations.FS, e.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: e.Name(), sql: string(b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i].version == out[i-1].version {
			return nil, fmt.Errorf("version de migration dupliquée: %d", out[i].version)
		}
	}
	return out, nil
}

// ExpectedSchemaVersion renvoie la version de la dernière migration embarquée.
func ExpectedSchemaVersion() int {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 {
		return 0
	}
	return ms[len(ms)-1].version
}

// Migrate applique les migrations manquantes (idempotent, verrou consultatif).
func (s *Store) Migrate(ctx context.Context) ([]int, error) {
	ms, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(727001)`); err != nil {
		return nil, err
	}
	defer func() { _, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(727001)`) }()
	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	var applied []int
	for _, m := range ms {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, m.version).Scan(&exists); err != nil {
			return applied, err
		}
		if exists {
			continue
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, err
		}
		if _, err := tx.Exec(ctx, m.sql); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, m.version); err != nil {
			_ = tx.Rollback(ctx)
			return applied, err
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied = append(applied, m.version)
	}
	return applied, nil
}

// SchemaVersion renvoie la version appliquée (0 si aucune).
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v *int
	err := s.Pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&v)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "42P01" {
			return 0, nil
		}
		return 0, err
	}
	if v == nil {
		return 0, nil
	}
	return *v, nil
}

// ------------------------------------------------------------------ notifications

// Channel est le canal LISTEN/NOTIFY des événements temps réel.
const Channel = "aos_events"

// Notify publie un événement livré au commit de la transaction.
func Notify(ctx context.Context, q Q, typ string, id int64) error {
	b, _ := json.Marshal(map[string]any{"type": typ, "id": id})
	_, err := q.Exec(ctx, `SELECT pg_notify($1, $2)`, Channel, string(b))
	return err
}

// ------------------------------------------------------------------ pagination

// Page décrit une demande de page.
type Page struct {
	Limit  int
	Cursor int64 // id exclusif (ordre décroissant) ; 0 = début
}

// NextCursor calcule le curseur suivant à partir des ids (limit+1 lus).
func NextCursor[T any](items []T, limit int, id func(T) int64) ([]T, *string) {
	if len(items) <= limit {
		return items, nil
	}
	items = items[:limit]
	c := strconv.FormatInt(id(items[len(items)-1]), 10)
	return items, &c
}

func cursorOrMax(c int64) int64 {
	if c <= 0 {
		return 1<<62 - 1
	}
	return c
}
