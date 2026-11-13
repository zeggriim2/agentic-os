-- 1.12.0 : discussion avec les agents depuis la page d'une feature.
-- - consigne (instruction) : transmise à l'agent en cours (steer Hermes) ou gardée pour son prochain run ;
-- - question : posée à un rôle, répondue dans la dernière session Hermes de ce rôle sur la feature
--   (job answer_question), la réponse est un message de kind answer.
ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_check CHECK (
    kind IN ('po_spec','tech_design','open_issues','develop','review','qa','publish_pr','execute_approval','sync_agents',
             'publish_feature_pr','switch_integration','open_correction','refresh_feature_pr','answer_question')
);

-- Session Hermes du run (relue à la fin du run) : une question y reprend tout le contexte de l'agent.
ALTER TABLE runs ADD COLUMN hermes_session_id text CHECK (hermes_session_id IS NULL OR char_length(hermes_session_id) <= 200);

CREATE TABLE feature_messages (
    id          BIGSERIAL PRIMARY KEY,
    feature_id  BIGINT NOT NULL REFERENCES features(id),
    kind        TEXT NOT NULL CHECK (kind IN ('instruction','question','answer')),
    author      TEXT NOT NULL CHECK (char_length(author) BETWEEN 1 AND 200),
    target_role TEXT CHECK (target_role IN ('po','tech_lead','developer','reviewer','qa')),
    body        TEXT NOT NULL CHECK (char_length(body) BETWEEN 1 AND 12000),
    -- instruction : delivered (steer accepté) | pending (prochain run) | consumed (ajoutée à un run)
    -- question : answering | answered | failed ; answer : answered
    status      TEXT NOT NULL CHECK (status IN ('delivered','pending','consumed','answering','answered','failed')),
    reply_to    BIGINT REFERENCES feature_messages(id),
    run_id      BIGINT REFERENCES runs(id),
    error       TEXT CHECK (error IS NULL OR char_length(error) <= 2000),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (kind <> 'question' OR target_role IS NOT NULL),
    CHECK (kind <> 'answer' OR reply_to IS NOT NULL)
);
CREATE INDEX feature_messages_feature_idx ON feature_messages(feature_id, id);
CREATE INDEX feature_messages_pending_idx ON feature_messages(feature_id) WHERE status = 'pending';
