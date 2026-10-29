-- 0008 : mode d'intégration des tickets d'une feature.
--   direct         : chaque ticket ouvre sa PR vers la branche de base du projet (comportement historique) ;
--   feature_branch : les tickets visent une branche de feature (feature/F<id>-<titre>), puis une PR
--                    finale branche de feature → base est ouverte après approbation (recette humaine).
-- Le projet porte le mode par défaut (pré-remplissage) ; chaque feature porte son mode, modifiable tant
-- qu'aucun de ses tickets n'est mergé.
ALTER TABLE projects
    ADD COLUMN integration_mode TEXT NOT NULL DEFAULT 'direct'
        CHECK (integration_mode IN ('direct','feature_branch'));
ALTER TABLE features
    ADD COLUMN integration_mode  TEXT NOT NULL DEFAULT 'direct'
        CHECK (integration_mode IN ('direct','feature_branch')),
    ADD COLUMN feature_branch    TEXT,
    ADD COLUMN feature_pr_number INTEGER CHECK (feature_pr_number IS NULL OR feature_pr_number > 0),
    ADD COLUMN feature_pr_url    TEXT;

ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_check CHECK (
    kind IN ('po_spec','tech_design','open_issues','develop','review','qa','publish_pr','execute_approval','sync_agents',
             'publish_feature_pr','switch_integration')
);
ALTER TABLE approvals DROP CONSTRAINT approvals_kind_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_kind_check CHECK (
    kind IN ('spec_review','design_review','dev_gate','rework','publish_pr','publish_feature_pr')
);
