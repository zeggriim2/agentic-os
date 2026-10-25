import { defineConfig, devices } from "@playwright/test";
import { AUTH_FILE } from "./support/paths";

/**
 * Tests E2E navigateur d'Agentic OS.
 *
 * La configuration globale (support/global-setup.ts) construit l'UI et les binaires, démarre
 * PostgreSQL (conteneur jetable), les faux services (Hermes/GitHub/Telegram) et `agentic-os serve`
 * sur des ports libres en loopback. Les tests partagent ce serveur : ils s'exécutent donc en série
 * (un seul worker) et chaque fichier crée ses propres projets/features.
 */
export default defineConfig({
  testDir: "./tests",
  globalSetup: "./support/global-setup.ts",
  fullyParallel: false,
  workers: 1,
  // Pas de nouvel essai automatique : un test instable doit être corrigé, et la connexion est
  // limitée à 5 tentatives par minute et par IP côté serveur.
  retries: 0,
  forbidOnly: !!process.env.CI,
  timeout: 120_000,
  expect: { timeout: 20_000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "playwright-report" }]],
  outputDir: "test-results",
  use: {
    ...devices["Desktop Chrome"],
    viewport: { width: 1440, height: 900 },
    locale: "fr-FR",
    timezoneId: "Europe/Paris",
    storageState: AUTH_FILE,
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
});
