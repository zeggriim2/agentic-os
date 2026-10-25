-- 0003 : arrêt des runs confirmé côté Hermes, livraisons webhook retraitables.

-- Runs : état intermédiaire « stopping » tant que l'arrêt n'est pas confirmé par Hermes.
ALTER TABLE runs DROP CONSTRAINT runs_status_check;
ALTER TABLE runs ADD CONSTRAINT runs_status_check
    CHECK (status IN ('submitted','running','stopping','completed','failed','stopped','lost'));
ALTER TABLE runs
    ADD COLUMN stop_requested_at TIMESTAMPTZ,
    ADD COLUMN stop_attempts     INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN stop_error        TEXT;
CREATE INDEX runs_stopping_idx ON runs(id) WHERE status = 'stopping';

-- Livraisons GitHub : cycle reçu → en cours → traité | échoué (avec reprise).
ALTER TABLE github_deliveries
    ADD COLUMN status          TEXT NOT NULL DEFAULT 'received'
        CHECK (status IN ('received','processing','processed','failed')),
    ADD COLUMN attempts        INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN payload         JSONB,
    ADD COLUMN last_error      TEXT,
    ADD COLUMN next_attempt_at TIMESTAMPTZ,
    ADD COLUMN claimed_at      TIMESTAMPTZ;
-- Les livraisons antérieures n'ont pas conservé leur corps : non retraitables.
UPDATE github_deliveries SET
    status = CASE WHEN processed THEN 'processed' ELSE 'failed' END,
    last_error = CASE WHEN processed THEN NULL ELSE 'corps non conservé (livraison antérieure à la migration 0003)' END;
CREATE INDEX github_deliveries_retry_idx ON github_deliveries(next_attempt_at) WHERE status = 'failed';
