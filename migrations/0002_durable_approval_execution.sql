-- Rend l'exécution des décisions d'approbation durable via la file de jobs.
ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_check CHECK (
    kind IN ('po_spec','tech_design','open_issues','develop','review','qa','publish_pr','execute_approval','sync_agents')
);

-- Les versions antérieures exécutaient la décision dans une seconde transaction.
-- Une décision non exécutée ne possède pas de snapshot complet et ne peut donc
-- pas être rejouée silencieusement. On conserve une seule approbation actionnable
-- par kind/sujet, on expire les anciennes, puis on exige une nouvelle décision.
WITH ranked AS (
    SELECT id,
           row_number() OVER (
               PARTITION BY kind, subject_type, subject_id
               ORDER BY (status = 'pending') DESC, id DESC
           ) AS rn
    FROM approvals
    WHERE status IN ('pending','approved','rejected','execution_failed')
)
UPDATE approvals a
SET status = 'expired', updated_at = now()
FROM ranked r
WHERE a.id = r.id AND r.rn > 1;

UPDATE approvals
SET status = 'pending',
    decision = NULL,
    decided_by = NULL,
    decided_at = NULL,
    decision_comment = NULL,
    executed_at = NULL,
    execution_result = NULL,
    updated_at = now()
WHERE status IN ('approved','rejected','execution_failed');

-- Rafraîchit le texte présenté et le snapshot des approbations de feature qui
-- autorisent des effets liés au dépôt.
UPDATE approvals a
SET details = a.details || jsonb_build_object(
        'project_id', p.id,
        'repository', p.repository,
        'base_branch', p.base_branch,
        'required_checks', to_jsonb(p.required_checks)
    ),
    effect = CASE a.kind
        WHEN 'design_review' THEN format(
            'Créer %s issues sur %s (marqueur agentic-os, aucune autre écriture).',
            jsonb_array_length(COALESCE(f.design->'tickets', '[]'::jsonb)), p.repository
        )
        WHEN 'dev_gate' THEN format(
            'Autoriser le développement autonome de %s ticket(s) sur %s : branches aos/<ticket>, commits et push par Agentic OS, sans PR ni merge.',
            (SELECT count(*) FROM tickets t WHERE t.feature_id = f.id), p.repository
        )
        ELSE a.effect
    END,
    updated_at = now()
FROM features f
JOIN projects p ON p.id = f.project_id
WHERE a.status = 'pending'
  AND a.subject_type = 'feature'
  AND a.subject_id = f.id
  AND a.kind IN ('design_review','dev_gate');

-- Même remise à niveau pour la publication de PR d'un ticket.
UPDATE approvals a
SET details = a.details || jsonb_build_object(
        'project_id', p.id,
        'repository', p.repository,
        'base_branch', p.base_branch,
        'required_checks', to_jsonb(p.required_checks),
        'head', t.branch,
        'sha', t.head_sha
    ),
    effect = format(
        'Créer une PR draft sur %s de %s vers %s au SHA %s (aucun merge, aucun déploiement).',
        p.repository, COALESCE(t.branch, ''), p.base_branch, left(COALESCE(t.head_sha, ''), 8)
    ),
    updated_at = now()
FROM tickets t
JOIN projects p ON p.id = t.project_id
WHERE a.status = 'pending'
  AND a.subject_type = 'ticket'
  AND a.subject_id = t.id
  AND a.kind = 'publish_pr';
