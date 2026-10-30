-- 1.10.0 : retour de recette humaine. Une feature en recette (mode branche de feature) peut recevoir
-- un ticket de correction : son issue est créée par le job open_correction, puis la PR finale déjà
-- ouverte est mise à jour par refresh_feature_pr quand la feature revient en recette.
ALTER TABLE jobs DROP CONSTRAINT jobs_kind_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_kind_check CHECK (
    kind IN ('po_spec','tech_design','open_issues','develop','review','qa','publish_pr','execute_approval','sync_agents',
             'publish_feature_pr','switch_integration','open_correction','refresh_feature_pr')
);

-- Origine du ticket : conception (tech lead) ou correction demandée en recette humaine.
ALTER TABLE tickets ADD COLUMN origin text NOT NULL DEFAULT 'design' CHECK (origin IN ('design','correction'));
