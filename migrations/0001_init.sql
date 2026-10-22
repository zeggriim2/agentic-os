-- 0001 : schéma initial Agentic OS V1

CREATE TABLE projects (
    id              BIGSERIAL PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,39}$'),
    name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    repository      TEXT NOT NULL CHECK (repository ~ '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'),
    base_branch     TEXT NOT NULL DEFAULT 'main',
    required_checks TEXT[] NOT NULL DEFAULT '{}',
    mode            TEXT NOT NULL DEFAULT 'sandbox' CHECK (mode IN ('sandbox','product')),
    enabled         BOOLEAN NOT NULL DEFAULT false,
    test_command    TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agents (
    id                    BIGSERIAL PRIMARY KEY,
    role                  TEXT NOT NULL UNIQUE CHECK (role IN ('po','tech_lead','developer','reviewer','qa')),
    hermes_profile        TEXT NOT NULL,
    desired_model         TEXT NOT NULL DEFAULT '',
    observed_model        TEXT,
    observed_provider     TEXT,
    sync_state            TEXT NOT NULL DEFAULT 'UNKNOWN' CHECK (sync_state IN ('SYNCED','DRIFT_DETECTED','UNKNOWN','UNREACHABLE')),
    observed_at           TIMESTAMPTZ,
    max_tokens_per_run    INTEGER NOT NULL DEFAULT 400000 CHECK (max_tokens_per_run > 0),
    enabled               BOOLEAN NOT NULL DEFAULT true,
    instructions_revision INTEGER NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO agents (role, hermes_profile) VALUES
    ('po', 'aos-po'),
    ('tech_lead', 'aos-tech-lead'),
    ('developer', 'aos-developer'),
    ('reviewer', 'aos-reviewer'),
    ('qa', 'aos-qa');

CREATE TABLE features (
    id             BIGSERIAL PRIMARY KEY,
    project_id     BIGINT NOT NULL REFERENCES projects(id),
    title          TEXT NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
    description    TEXT NOT NULL DEFAULT '',
    state          TEXT NOT NULL DEFAULT 'IDEA',
    previous_state TEXT,
    state_reason   TEXT NOT NULL DEFAULT '',
    spec           JSONB,
    design         JSONB,
    version        INTEGER NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX features_state_idx ON features(state);
CREATE INDEX features_project_idx ON features(project_id);

CREATE TABLE tickets (
    id             BIGSERIAL PRIMARY KEY,
    feature_id     BIGINT NOT NULL REFERENCES features(id),
    project_id     BIGINT NOT NULL REFERENCES projects(id),
    key            TEXT NOT NULL UNIQUE,
    seq            INTEGER NOT NULL,
    title          TEXT NOT NULL,
    body           TEXT NOT NULL DEFAULT '',
    acceptance     JSONB NOT NULL DEFAULT '[]',
    state          TEXT NOT NULL DEFAULT 'PLANNED',
    previous_state TEXT,
    state_reason   TEXT NOT NULL DEFAULT '',
    issue_number   INTEGER,
    issue_url      TEXT,
    branch         TEXT,
    head_sha       TEXT,
    attempt        INTEGER NOT NULL DEFAULT 0,
    pr_number      INTEGER,
    pr_url         TEXT,
    ci_state       TEXT NOT NULL DEFAULT 'unverified' CHECK (ci_state IN ('unverified','pending','success','failure')),
    version        INTEGER NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (feature_id, seq)
);
CREATE INDEX tickets_state_idx ON tickets(state);

CREATE TABLE jobs (
    id               BIGSERIAL PRIMARY KEY,
    kind             TEXT NOT NULL CHECK (kind IN ('po_spec','tech_design','open_issues','develop','review','qa','publish_pr','sync_agents')),
    subject_type     TEXT NOT NULL CHECK (subject_type IN ('feature','ticket','approval','system')),
    subject_id       BIGINT NOT NULL,
    project_id       BIGINT REFERENCES projects(id),
    status           TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','leased','succeeded','failed','cancelled')),
    attempts         INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    run_after        TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner      TEXT,
    lease_expires_at TIMESTAMPTZ,
    idempotency_key  TEXT NOT NULL UNIQUE,
    last_error       TEXT,
    error_class      TEXT CHECK (error_class IN ('timeout','budget','policy','provider','invalid_output','external','internal')),
    payload          JSONB NOT NULL DEFAULT '{}',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX jobs_queue_idx ON jobs(status, run_after, id);
CREATE INDEX jobs_subject_idx ON jobs(subject_type, subject_id);
-- Un seul job develop|review|qa actif (leased) par projet.
CREATE UNIQUE INDEX jobs_one_active_dev_per_project ON jobs(project_id)
    WHERE status = 'leased' AND kind IN ('develop','review','qa');

CREATE TABLE runs (
    id             BIGSERIAL PRIMARY KEY,
    job_id         BIGINT NOT NULL REFERENCES jobs(id),
    agent_role     TEXT NOT NULL,
    hermes_profile TEXT NOT NULL,
    hermes_run_id  TEXT UNIQUE,
    status         TEXT NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted','running','completed','failed','stopped','lost')),
    input_tokens   BIGINT NOT NULL DEFAULT 0,
    output_tokens  BIGINT NOT NULL DEFAULT 0,
    total_tokens   BIGINT NOT NULL DEFAULT 0,
    model          TEXT,
    provider       TEXT,
    output         TEXT CHECK (output IS NULL OR octet_length(output) <= 262144),
    artifact       JSONB,
    error          TEXT,
    started_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX runs_job_idx ON runs(job_id);
CREATE INDEX runs_status_idx ON runs(status);

CREATE TABLE run_events (
    id         BIGSERIAL PRIMARY KEY,
    run_id     BIGINT NOT NULL REFERENCES runs(id),
    seq        INTEGER NOT NULL,
    event      TEXT NOT NULL,
    data       JSONB CHECK (data IS NULL OR octet_length(data::text) <= 65536),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, seq)
);

CREATE TABLE approvals (
    id               BIGSERIAL PRIMARY KEY,
    kind             TEXT NOT NULL CHECK (kind IN ('spec_review','design_review','dev_gate','rework','publish_pr')),
    subject_type     TEXT NOT NULL CHECK (subject_type IN ('feature','ticket')),
    subject_id       BIGINT NOT NULL,
    title            TEXT NOT NULL,
    effect           TEXT NOT NULL,
    details          JSONB NOT NULL DEFAULT '{}',
    status           TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected','executed','execution_failed','expired')),
    decision         TEXT CHECK (decision IN ('approved','rejected')),
    decided_by       TEXT,
    decided_at       TIMESTAMPTZ,
    decision_comment TEXT,
    executed_at      TIMESTAMPTZ,
    execution_result JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX approvals_one_pending ON approvals(kind, subject_type, subject_id) WHERE status = 'pending';
CREATE INDEX approvals_status_idx ON approvals(status);

CREATE TABLE audit_log (
    id           BIGSERIAL PRIMARY KEY,
    at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor        TEXT NOT NULL,
    action       TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_id   BIGINT,
    before       JSONB,
    after        JSONB,
    run_id       BIGINT,
    sha          TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_subject_idx ON audit_log(subject_type, subject_id);

CREATE FUNCTION audit_log_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_log est en ajout seul (% interdit)', TG_OP;
END;
$$;
CREATE TRIGGER audit_log_no_update_delete BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
CREATE TRIGGER audit_log_no_truncate BEFORE TRUNCATE ON audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION audit_log_immutable();

CREATE TABLE github_deliveries (
    id          BIGSERIAL PRIMARY KEY,
    delivery_id TEXT NOT NULL UNIQUE,
    event       TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed   BOOLEAN NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE settings (
    id         BIGSERIAL PRIMARY KEY,
    key        TEXT NOT NULL UNIQUE,
    value      JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO settings (key, value) VALUES ('dispatch_paused', 'false');

CREATE TABLE sessions (
    id          BIGSERIAL PRIMARY KEY,
    token_hash  TEXT NOT NULL UNIQUE,
    login       TEXT NOT NULL,
    csrf_token  TEXT NOT NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
