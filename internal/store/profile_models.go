package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// ProfileModel est le modèle par défaut voulu pour un profil Hermes `aos-*` (table profile_models).
type ProfileModel struct {
	Profile         string     `db:"profile"`
	DesiredProvider string     `db:"desired_provider"`
	DesiredModel    string     `db:"desired_model"`
	Revision        int        `db:"revision"`
	State           string     `db:"state"`
	Error           *string    `db:"error"`
	RequestedBy     string     `db:"requested_by"`
	RequestedAt     time.Time  `db:"requested_at"`
	AppliedAt       *time.Time `db:"applied_at"`
}

// États d'une demande de modèle de profil.
const (
	ProfileModelPending = "PENDING"
	ProfileModelApplied = "APPLIED"
	ProfileModelFailed  = "FAILED"
)

const profileModelCols = `profile, desired_provider, desired_model, revision, state, error, requested_by, requested_at, applied_at`

// ListProfileModels renvoie toutes les demandes, par profil.
func ListProfileModels(ctx context.Context, q Q) ([]ProfileModel, error) {
	return many[ProfileModel](ctx, q, `SELECT `+profileModelCols+` FROM profile_models ORDER BY profile`)
}

// RequestProfileModel enregistre (ou remplace) le modèle voulu d'un profil : nouvelle révision, état
// PENDING, erreur effacée. Le script de l'hôte appliquera cette révision.
func RequestProfileModel(ctx context.Context, q Q, profile, provider, model, actor string) (*ProfileModel, error) {
	return one[ProfileModel](ctx, q, `INSERT INTO profile_models (profile, desired_provider, desired_model, requested_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (profile) DO UPDATE SET desired_provider=EXCLUDED.desired_provider, desired_model=EXCLUDED.desired_model,
			revision=profile_models.revision+1, state='PENDING', error=NULL, requested_by=EXCLUDED.requested_by,
			requested_at=now(), applied_at=NULL
		RETURNING `+profileModelCols, profile, provider, model, actor)
}

// ProfileSyncLastSeen renvoie le dernier passage du script de synchronisation (nil : jamais vu).
func ProfileSyncLastSeen(ctx context.Context, q Q) (*time.Time, string, error) {
	var at time.Time
	var host string
	err := q.QueryRow(ctx, `SELECT last_seen_at, host FROM profile_sync_status WHERE id=1`).Scan(&at, &host)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	return &at, host, nil
}

// AllProjects renvoie tous les projets (vue des profils : usages et écarts de fournisseur).
func AllProjects(ctx context.Context, q Q) ([]Project, error) {
	return many[Project](ctx, q, `SELECT `+projectCols+` FROM projects ORDER BY slug`)
}
