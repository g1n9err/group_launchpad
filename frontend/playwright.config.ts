import { defineConfig } from "playwright/test";

// Browser E2E for the auth/RBAC flow. The suite expects a disposable stack:
//   - API on E2E_API_URL (default http://127.0.0.1:18090) backed by a
//     throwaway PostgreSQL database, seeded with E2E_* test accounts;
//   - the built SPA served on E2E_BASE_URL (default http://127.0.0.1:4199).
// `make e2e` (repo root) starts all of it; see Makefile for the wiring.
// No browser or external-service binaries are downloaded by this config —
// run `npx playwright install chromium` once on a new machine.
export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  retries: 0,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: process.env.E2E_BASE_URL ?? "http://127.0.0.1:4199",
    trace: "off",
  },
});
