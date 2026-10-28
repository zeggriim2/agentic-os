/**
 * Répartition des tokens d'un run. Hermes compte dans input_tokens les tokens relus du cache du
 * fournisseur : seule l'entrée « nouvelle » et la sortie sont réellement produites par le run ;
 * la relecture du cache est bien moins coûteuse. Le total brut surestime donc la consommation.
 */
export interface TokenSplit {
  fresh: number;
  cache: number;
  output: number;
  total: number;
}

type RunTokens = {
  input_tokens?: number | null;
  output_tokens?: number | null;
  total_tokens?: number | null;
  fresh_input_tokens?: number | null;
  cache_read_tokens?: number | null;
};

export function runTokenSplit(r: RunTokens): TokenSplit {
  const input = r.input_tokens ?? 0;
  const cache = Math.min(Math.max(r.cache_read_tokens ?? 0, 0), input);
  const fresh = r.fresh_input_tokens ?? Math.max(input - cache, 0);
  const output = r.output_tokens ?? 0;
  return { fresh, cache, output, total: r.total_tokens ?? input + output };
}

/** Tokens « nouveaux » : entrée nouvelle + sortie (hors relecture du cache). */
export const newTokens = (s: { fresh: number; output: number }) => s.fresh + s.output;
