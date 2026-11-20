// Browser E2E: real login, logout, role-based navigation, scoped groups and
// denied access — against the real SPA and the real API (disposable stack).
// Credentials arrive via environment variables set by `make e2e`; they are
// throwaway values for an isolated database, never production secrets.

import { expect, test, request, type Page } from "playwright/test";

const apiURL = process.env.E2E_API_URL ?? "http://127.0.0.1:18090";
const baseURL = process.env.E2E_BASE_URL ?? "http://127.0.0.1:4199";
const accounts = {
  admin: {
    email: process.env.E2E_ADMIN_EMAIL ?? "e2e-admin@launchpad.test",
    password: process.env.E2E_ADMIN_PASSWORD ?? "e2e-admin-password-123",
  },
  coordinator: {
    email: process.env.E2E_COORDINATOR_EMAIL ?? "e2e-coordinator@launchpad.test",
    password: process.env.E2E_COORDINATOR_PASSWORD ?? "e2e-coord-password-123",
  },
  lecturer: {
    email: process.env.E2E_LECTURER_EMAIL ?? "e2e-lecturer@launchpad.test",
    password: process.env.E2E_LECTURER_PASSWORD ?? "e2e-lecturer-password-1",
  },
  student: {
    email: process.env.E2E_STUDENT_EMAIL ?? "e2e-student@launchpad.test",
    password: process.env.E2E_STUDENT_PASSWORD ?? "e2e-student-password-1",
  },
};

const alphaGroup = "E2E Alpha Group";
const betaGroup = "E2E Beta Group";
const rosterStudent = "e2e-roster-student@launchpad.test";

let alphaId = "";
let betaId = "";

async function apiLogin(email: string, password: string): Promise<string> {
  const ctx = await request.newContext({ baseURL: apiURL });
  const response = await ctx.post("/api/auth/login", { data: { email, password } });
  expect(response.status(), `login as ${email}`).toBe(200);
  const body = await response.json();
  await ctx.dispose();
  return body.token as string;
}

async function createUser(adminToken: string, email: string, password: string, role: string) {
  const ctx = await request.newContext({ baseURL: apiURL });
  const response = await ctx.post("/api/admin/users", {
    headers: { Authorization: `Bearer ${adminToken}` },
    data: { email, fullName: `E2E ${role}`, role, password },
  });
  expect([201, 409], `create ${email}`).toContain(response.status());
  await ctx.dispose();
}

test.beforeAll(async () => {
  const adminToken = await apiLogin(accounts.admin.email, accounts.admin.password);
  await createUser(adminToken, accounts.coordinator.email, accounts.coordinator.password, "coordinator");
  await createUser(adminToken, accounts.lecturer.email, accounts.lecturer.password, "lecturer");
  await createUser(adminToken, accounts.student.email, accounts.student.password, "student");

  const coordinatorToken = await apiLogin(accounts.coordinator.email, accounts.coordinator.password);
  const ctx = await request.newContext({ baseURL: apiURL });
  const groups: [string, string][] = [
    [alphaGroup, accounts.lecturer.email],
    [betaGroup, "other-lecturer@launchpad.test"],
  ];
  for (const [name, lecturerEmail] of groups) {
    const response = await ctx.post("/api/groups", {
      headers: { Authorization: `Bearer ${coordinatorToken}` },
      data: {
        name,
        courseName: "E2E Course",
        startDate: "2026-12-01",
        staffEmails: ["e2e-staff@launchpad.test"],
        lecturerEmails: [lecturerEmail],
        studentEmails: [rosterStudent],
      },
    });
    // 201 on a fresh database; 409 when a partial earlier run already
    // seeded the group (the disposable DB persists for the whole run).
    expect([201, 409], `create ${name}`).toContain(response.status());
    const group = await response.json();
    if (group?.id) {
      if (name === alphaGroup) alphaId = group.id;
      else betaId = group.id;
    }
  }
  if (!alphaId || !betaId) {
    const listed = await ctx.get("/api/groups", {
      headers: { Authorization: `Bearer ${coordinatorToken}` },
      params: { limit: 50, offset: 0 },
    });
    for (const group of (await listed.json()).data ?? []) {
      if (group.name === alphaGroup) alphaId = group.id;
      if (group.name === betaGroup) betaId = group.id;
    }
  }
  expect(alphaId, "alpha group id").not.toBe("");
  expect(betaId, "beta group id").not.toBe("");
  await ctx.dispose();
});

async function openApp(page: Page, hash = "#/") {
  await page.addInitScript(() => localStorage.setItem("launchpad.lang", "en"));
  await page.goto(`${baseURL}/${hash}`);
}

async function signIn(page: Page, email: string, password: string) {
  await openApp(page, "#/login");
  await page.locator("#login-email").fill(email);
  await page.locator("#login-password").fill(password);
  await page.locator(".login-submit").click();
  await page.waitForURL((url) => !url.hash.includes("login"), { timeout: 10_000 });
}

// storageSnapshot returns everything a page could persist tokens in.
async function storageSnapshot(page: Page): Promise<string> {
  return page.evaluate(() => {
    const local: string[] = [];
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i)!;
      if (key !== "launchpad.lang" && key !== "launchpad.theme") local.push(`${key}=${localStorage.getItem(key)}`);
    }
    const session: string[] = [];
    for (let i = 0; i < sessionStorage.length; i++) {
      const key = sessionStorage.key(i)!;
      session.push(`${key}=${sessionStorage.getItem(key)}`);
    }
    return [...local, ...session, document.cookie].join("|");
  });
}

test("unauthenticated users cannot trigger protected queries", async ({ page }) => {
  const protectedCalls: string[] = [];
  page.on("request", (req) => {
    if (req.url().includes("/api/") && !req.url().includes("/api/auth/login")) protectedCalls.push(req.url());
  });
  await openApp(page, "#/overview");
  await expect(page.getByText("Coordinator access required").first()).toBeVisible({ timeout: 10_000 });
  await page.waitForTimeout(700); // deferred queries would fire by now
  expect(protectedCalls).toEqual([]);
});

test("invalid login shows an error and stores nothing", async ({ page }) => {
  await openApp(page, "#/login");
  await page.locator("#login-email").fill(accounts.coordinator.email);
  await page.locator("#login-password").fill("definitely-wrong-password");
  await page.locator(".login-submit").click();
  await expect(page.locator("[role=alert]")).toBeVisible({ timeout: 10_000 });
  await expect(page).toHaveURL(/#\/login/);
  expect(await storageSnapshot(page)).toBe("");
});

test("coordinator login: full navigation, Bearer header, token only in memory", async ({ page }) => {
  const authHeaders: string[] = [];
  page.on("request", (req) => {
    const header = req.headers()["authorization"];
    if (header && req.url().includes("/api/")) authHeaders.push(header);
  });
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  await expect(page).toHaveURL(/#\/overview/);

  await page.goto(`${baseURL}/#/groups`);
  await expect(page.getByText(alphaGroup)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(betaGroup)).toBeVisible();

  // Protected API calls carried a Bearer token…
  expect(authHeaders.length).toBeGreaterThan(0);
  expect(authHeaders.every((header) => header.startsWith("Bearer "))).toBe(true);
  // …but the token never persisted anywhere in browser storage.
  const persisted = await storageSnapshot(page);
  expect(persisted).toBe("");
});

test("lecturer login lands on scoped groups; only the assigned group is visible", async ({ page }) => {
  await signIn(page, accounts.lecturer.email, accounts.lecturer.password);
  await expect(page).toHaveURL(/#\/groups/);

  await expect(page.getByText(alphaGroup)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(betaGroup)).toHaveCount(0);

  // Visible navigation is groups-only (matches backend authorization).
  const navTexts = (await page.locator("nav a").allTextContents()).map((text) => text.trim()).filter(Boolean);
  expect(navTexts.length).toBeGreaterThan(0);
  for (const forbidden of ["Students", "Integrations", "Automation", "Announcements", "Intake", "Overview", "Settings"]) {
    expect(navTexts.some((text) => text.includes(forbidden)), `nav shows ${forbidden}`).toBe(false);
  }
});

test("lecturer cannot reach another group by URL; no roster or student PII leaks", async ({ page }) => {
  await signIn(page, accounts.lecturer.email, accounts.lecturer.password);

  // IDOR attempt: navigate straight to the foreign group's details.
  await page.goto(`${baseURL}/#/group-details/${betaId}`);
  await page.waitForTimeout(1_000);
  await expect(page.getByText(betaGroup)).toHaveCount(0);
  await expect(page.getByText(rosterStudent)).toHaveCount(0);

  // Own group: name visible, no roster table, no launch button.
  await page.goto(`${baseURL}/#/group-details/${alphaId}`);
  await expect(page.getByText(alphaGroup).first()).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText(rosterStudent)).toHaveCount(0);
  await expect(page.getByRole("table")).toHaveCount(0);
  await expect(page.locator("button", { hasText: "Launch" })).toHaveCount(0);
});

test("student login: account only, no workspace surface", async ({ page }) => {
  const protectedCalls: string[] = [];
  page.on("request", (req) => {
    if (req.url().includes("/api/") && !req.url().includes("/api/auth/")) protectedCalls.push(req.url());
  });
  await signIn(page, accounts.student.email, accounts.student.password);
  await expect(page).toHaveURL(/#\/account/);
  await expect(page.getByText(accounts.student.email).first()).toBeVisible();

  await page.goto(`${baseURL}/#/students`);
  await expect(page.getByText("does not have access").first()).toBeVisible({ timeout: 10_000 });
  await page.waitForTimeout(700);
  expect(protectedCalls).toEqual([]);
});

test("logout clears access and protected queries stop", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  await expect(page).toHaveURL(/#\/overview/);
  // Let the overview's initial queries finish BEFORE measuring.
  await page.waitForLoadState("networkidle");

  // What matters after logout: NO request may carry credentials. (Cleared
  // TanStack queries can still finish an in-flight refetch; it is rejected
  // with 401 because the Authorization header is gone.)
  const authedAfterLogout: string[] = [];
  page.on("request", (req) => {
    if (req.url().includes("/api/") && !req.url().includes("/api/auth/") && req.headers()["authorization"]) {
      authedAfterLogout.push(req.url());
    }
  });

  await page.locator("header .app-header-danger").first().click();
  await page.waitForURL((url) => url.hash.includes("login"), { timeout: 10_000 });

  await page.goto(`${baseURL}/#/overview`);
  await expect(page.getByText("Coordinator access required").first()).toBeVisible({ timeout: 10_000 });
  await page.waitForTimeout(700);
  expect(authedAfterLogout).toEqual([]);
  expect(await storageSnapshot(page)).toBe("");
});
