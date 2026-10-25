import path from "node:path";
import { fileURLToPath } from "node:url";

/** Dossier e2e/ (racine du paquet Playwright). */
export const E2E_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
/** Racine du dépôt Agentic OS. */
export const REPO_ROOT = path.resolve(E2E_DIR, "..");
/** Session administrateur partagée par les tests (écrite par la configuration globale). */
export const AUTH_FILE = path.join(E2E_DIR, ".auth", "admin.json");
/** État de l'environnement démarré (URL, mot de passe de test), fichier 0600. */
export const STATE_FILE = path.join(E2E_DIR, ".state", "env.json");
/** Journaux des processus démarrés (app, faux services), conservés pour le diagnostic. */
export const LOG_DIR = path.join(E2E_DIR, "logs");
