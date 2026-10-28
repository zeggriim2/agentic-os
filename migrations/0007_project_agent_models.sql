-- 0007 : modèle LLM par agent et par projet. Un projet peut imposer, par rôle, le fournisseur et le
-- modèle envoyés à Hermes dans la requête de run ({"developer": {"provider": "anthropic", "model": "..."}}).
-- Rôle absent de l'objet = modèle souhaité de la table agents (desired_model, sans fournisseur), sinon
-- défaut du profil Hermes. Les runs gardent la surcharge demandée (requested_*) et signalent un repli
-- (model_fallback) : re-soumission unique sans surcharge quand Hermes refuse le fournisseur demandé.
ALTER TABLE projects
    ADD COLUMN agent_models JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(agent_models) = 'object');
ALTER TABLE runs
    ADD COLUMN requested_model    TEXT,
    ADD COLUMN requested_provider TEXT,
    ADD COLUMN model_fallback     BOOLEAN NOT NULL DEFAULT false;
