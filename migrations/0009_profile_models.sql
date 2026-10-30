-- 1.8.0 : modèle par défaut des profils Hermes `aos-*`, choisi dans Agentic OS et appliqué sur l'hôte
-- par deploy/agents/sync-profile-models.sh (Hermes n'expose pas d'API d'écriture du modèle d'un profil).
-- Agentic OS écrit l'état voulu (PENDING) ; le script l'applique, le relit et note APPLIED ou FAILED
-- pour la révision lue (une demande plus récente reste PENDING).
CREATE TABLE profile_models (
    profile          TEXT PRIMARY KEY CHECK (profile ~ '^aos-[a-z0-9][a-z0-9_-]{0,59}$'),
    desired_provider TEXT NOT NULL CHECK (desired_provider ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
    desired_model    TEXT NOT NULL CHECK (desired_model ~ '^[A-Za-z0-9][A-Za-z0-9._:/@-]{0,199}$'),
    revision         INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0),
    state            TEXT NOT NULL DEFAULT 'PENDING' CHECK (state IN ('PENDING', 'APPLIED', 'FAILED')),
    error            TEXT,
    requested_by     TEXT NOT NULL,
    requested_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    applied_at       TIMESTAMPTZ
);

-- Dernier passage du script de synchronisation (une seule ligne) : sans passage récent, l'UI signale
-- que le timer n'est pas installé ou ne tourne plus.
CREATE TABLE profile_sync_status (
    id           SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    last_seen_at TIMESTAMPTZ NOT NULL,
    host         TEXT NOT NULL DEFAULT ''
);
