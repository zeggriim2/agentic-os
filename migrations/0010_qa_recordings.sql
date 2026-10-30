-- 1.9.0 : QA navigateur. Par projet, le QA peut jouer un scénario Playwright dans l'image des agents
-- (aos-qa-browser) ; la session est enregistrée (vidéo, trace, captures, rapport) puis copiée par
-- Agentic OS dans son stockage (hors du dossier monté chez l'agent) et conservée 15 jours.
ALTER TABLE projects ADD COLUMN qa_browser BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE qa_recordings (
    id          BIGSERIAL PRIMARY KEY,
    run_id      BIGINT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    ticket_id   BIGINT NOT NULL REFERENCES tickets(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('video', 'trace', 'screenshot', 'report', 'scenario', 'log')),
    -- Chemin relatif dans le dossier du run (video.webm, shots/01-recap.png…), validé à l'ingestion.
    name        TEXT NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9._-]{0,80}(/[a-z0-9][a-z0-9._-]{0,80})?$'),
    label       TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 200),
    criterion   INTEGER CHECK (criterion IS NULL OR criterion > 0),
    size_bytes  BIGINT NOT NULL CHECK (size_bytes >= 0),
    sha256      TEXT NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (run_id, name)
);
CREATE INDEX qa_recordings_ticket_idx ON qa_recordings (ticket_id, run_id);
CREATE INDEX qa_recordings_expiry_idx ON qa_recordings (expires_at);
