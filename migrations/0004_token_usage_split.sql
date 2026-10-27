-- 0004 : répartition des tokens d'un run (entrée nouvelle / relue du cache / sortie).
-- Hermes compte dans input_tokens les tokens relus du cache : input_tokens - cache_read_tokens est
-- l'entrée réellement nouvelle. Les colonnes existantes ne changent pas de sens.
ALTER TABLE runs
    ADD COLUMN cache_read_tokens  BIGINT NOT NULL DEFAULT 0 CHECK (cache_read_tokens >= 0),
    ADD COLUMN cache_write_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cache_write_tokens >= 0);

-- Reprise des runs terminés : l'usage complet est conservé dans l'événement run.completed.
UPDATE runs r SET
    cache_read_tokens  = GREATEST(0, LEAST(r.input_tokens, COALESCE((u.usage->>'cache_read_tokens')::bigint, 0))),
    cache_write_tokens = CASE WHEN COALESCE(u.usage->>'cache_write_tokens', '') ~ '^[0-9]{1,18}$'
                              THEN (u.usage->>'cache_write_tokens')::bigint ELSE 0 END
FROM (
    SELECT DISTINCT ON (run_id) run_id, data->'usage' AS usage
    FROM run_events
    WHERE event = 'run.completed' AND jsonb_typeof(data->'usage') = 'object'
      AND COALESCE(data->'usage'->>'cache_read_tokens', '') ~ '^[0-9]{1,18}$'
    ORDER BY run_id, seq DESC
) u
WHERE u.run_id = r.id;
