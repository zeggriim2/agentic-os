-- 0006 : canal Telegram par projet. Les notifications d'un projet partent dans ce groupe (et ce
-- sujet de forum) ; sans canal, elles partent dans le chat par défaut (secret telegram_chat_id),
-- préfixées du nom du projet.
ALTER TABLE projects
    ADD COLUMN telegram_chat_id   TEXT   NOT NULL DEFAULT '' CHECK (telegram_chat_id ~ '^(-?[0-9]{1,20})?$'),
    ADD COLUMN telegram_thread_id BIGINT NOT NULL DEFAULT 0  CHECK (telegram_thread_id >= 0);
