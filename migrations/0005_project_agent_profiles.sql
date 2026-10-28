-- 0005 : profils Hermes par projet. Un projet peut confier un rôle à un profil dédié (par exemple
-- aos-fraiszen-developer, avec l'image Docker outillée du projet et ses seuls worktrees montés).
-- Rôle absent de l'objet = profil par défaut de la table agents. Les runs gardent le profil utilisé.
ALTER TABLE projects
    ADD COLUMN agent_profiles JSONB NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(agent_profiles) = 'object');
