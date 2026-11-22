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
    // /api/auth/* (login, resume) is public by design; everything else is protected.
    if (req.url().includes("/api/") && !req.url().includes("/api/auth/")) protectedCalls.push(req.url());
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

test("coordinator can register from the sign-in screen and starts a private session", async ({ page }) => {
  await openApp(page, "#/login");
  await page.getByRole("link", { name: "Create account" }).click();
  await expect(page).toHaveURL(/#\/signup/);
  await page.locator("#signup-name").fill("Workspace Coordinator");
  await page.locator("#signup-email").fill(`workspace-${Date.now()}@launchpad.test`);
  await page.locator("#signup-password").fill("private-workspace-password-123");
  await page.locator("#signup-confirmation").fill("private-workspace-password-123");
  await page.locator(".login-submit").click();
  await expect(page).toHaveURL(/#\/overview/, { timeout: 10_000 });
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

test("launch details follow the locale and the logo opens Home on desktop and mobile", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  const details = `${baseURL}/#/group-details/${alphaId}`;
  await page.route(`${apiURL}/api/groups/${alphaId}/status`, (route) => route.fulfill({
    json: {
      groupId: alphaId,
      status: "partially_failed",
      steps: [
        { platform: "slack", status: "partially_failed", detail: "0 channel, 4 invite, 0 welcome failures" },
        { platform: "classroom", status: "partially_failed", detail: "course created, 3 enrollment and 2 teacher assignment failures" },
        { platform: "drive", status: "partially_failed", detail: "folders created, permissions partial: 6 Drive permission grants failed (first error: drive: grant writer to yoh***: Google Drive returned HTTP 403)" },
      ],
    },
  }));

  await page.goto(`${baseURL}/#/settings`);
  await page.getByRole("button", { name: "Հայերեն" }).click();
  await page.goto(details);
  await expect(page.getByText("Դասընթացը ստեղծված է։ Չհաջողված միացումներ՝ 3 ուսանող, 2 դասավանդող։")).toBeVisible();
  await expect(page.getByText(/Չհաջողված թույլտվությունների քանակը՝ 6։.*HTTP 403/)).toBeVisible();
  await expect(page.getByText(/Չհաջողված գործողություններ՝ 0 ալիք, 4 հրավեր/)).toBeVisible();
  await page.getByRole("link", { name: "Group Launchpad — Գլխավոր էջ" }).click();
  await expect(page).toHaveURL(/#\/$/);
  await expect(page.getByRole("heading", { name: "Խմբի գործարկման բոլոր քայլերը՝ մեկ տեղում։" })).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${baseURL}/#/settings`);
  await page.getByRole("button", { name: "Русский" }).click();
  await page.goto(details);
  await expect(page.getByText("Курс создан. Не удалось добавить: студентов — 3, преподавателей — 2.")).toBeVisible();
  await expect(page.getByText(/Не удалось выдать разрешения: 6\..*HTTP 403/)).toBeVisible();
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await page.getByRole("link", { name: "Group Launchpad — Главная" }).click();
  await expect(page).toHaveURL(/#\/$/);
  await expect(page.getByRole("heading", { name: "Все этапы запуска группы в одном месте." })).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
});

test("coordinator creates a group through the form (regression: request headers)", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  await page.goto(`${baseURL}/#/groups/new`);

  // Fill the creation form exactly like a user would.
  await page.locator("#name").fill("E2E Form Created Group");
  await page.locator("#courseName").fill("E2E Course");
  await page.locator("#startDate").fill("2026-12-01");
  await page.locator("#staffEmails").fill("e2e-staff@launchpad.test");
  await page.locator("#lecturerEmails").fill("e2e-lecturer@launchpad.test");
  await page.locator("#studentEmails").fill("e2e-form-student@launchpad.test");
  // Regression for the authFetch wrapper bug: openapi-fetch passes a Request
  // object; the wrapper must PRESERVE its Content-Type when overriding
  // Authorization, or this submit fails with 415 Unsupported Media Type.
  await page.locator("form button[type=submit]").click();

  await page.waitForURL(/#\/group-details\//, { timeout: 10_000 });
  await expect(page.getByText("E2E Form Created Group").first()).toBeVisible({ timeout: 10_000 });
});

test("coordinator can switch between students and teaching staff in a group", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  await page.goto(`${baseURL}/#/group-details/${alphaId}`);
  await page.getByRole("tab", { name: "Group members" }).click();

  const students = page.getByRole("button", { name: "Students", exact: true });
  const staff = page.getByRole("button", { name: "Teaching Staff", exact: true });
  const studentRow = page.getByRole("row").filter({ hasText: rosterStudent });
  await expect(students).toHaveAttribute("aria-pressed", "true");
  await expect(studentRow).toBeVisible();

  await staff.click();
  await expect(staff).toHaveAttribute("aria-pressed", "true");
  const ta = page.locator("section").filter({ has: page.getByRole("heading", { name: "TA", exact: true }) });
  await expect(ta.getByText("e2e-staff@launchpad.test", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Teacher", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Lecturer", exact: true })).toBeVisible();
  await expect(page.getByText(accounts.lecturer.email, { exact: true })).toBeVisible();
  await expect(studentRow).toHaveCount(0);

  await students.click();
  await expect(studentRow).toBeVisible();
  await expect(ta).toHaveCount(0);
});

test("coordinator can edit student and teaching staff rosters in an existing group", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  await page.goto(`${baseURL}/#/group-details/${betaId}`);
  await page.getByRole("tab", { name: "Group members" }).click();

  await page.getByRole("button", { name: "Edit students" }).click();
  await page.getByLabel("Student emails").fill(`${rosterStudent}\ne2e-new-student@launchpad.test`);
  await page.getByRole("button", { name: "Save and sync access" }).click();
  await expect(page.getByRole("row").filter({ hasText: "e2e-new-student@launchpad.test" })).toBeVisible();
  await expect(page.getByText("Roster saved. Some access updates need attention.")).toBeVisible();

  await page.getByRole("button", { name: "Teaching Staff", exact: true }).click();
  await page.getByRole("button", { name: "Edit teaching staff" }).click();
  const lecturers = page.getByLabel("Lecturer emails");
  await expect(lecturers).toHaveValue("other-lecturer@launchpad.test");
  await lecturers.fill("other-lecturer@launchpad.test\nreplacement-lecturer@launchpad.test");
  await page.getByRole("button", { name: "Save and sync access" }).click();
  await expect(page.getByText("replacement-lecturer@launchpad.test", { exact: true })).toBeVisible();
  await expect(page.getByText("other-lecturer@launchpad.test", { exact: true })).toBeVisible();
});

test("coordinator can add a custom message when sending an intake form", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  const formURL = `${apiURL}/api/groups/${alphaId}/forms`;
  await page.route(formURL, (route) => route.fulfill({
    json: {
      status: "created",
      questions: [],
      responderUrl: "https://docs.google.com/forms/d/e/e2e/viewform",
      verifiedEmail: true,
    },
  }));
  await page.route(`${formURL}/deliveries`, (route) => route.fulfill({ json: { deliveries: [] } }));
  await page.route(`${formURL}/distribute`, (route) => route.fulfill({
    status: 202,
    json: { deliveries: [] },
  }));

  await page.goto(`${baseURL}/#/intake-forms`);
  await page.getByRole("combobox", { name: "Group" }).click();
  await page.getByRole("option", { name: alphaGroup, exact: true }).click();
  await page.getByRole("button", { name: "Send to students" }).click();
  const message = "Բարև։\nԽնդրում եմ լրացնել ձևաթուղթը։";
  await page.getByRole("textbox", { name: "Email message (optional)" }).fill(message);
  const submitted = page.waitForRequest((request) => request.method() === "POST" && request.url() === `${formURL}/distribute`);
  await page.getByRole("button", { name: "Confirm sending" }).click();

  expect((await submitted).postDataJSON()).toEqual({ message });
  await expect(page.getByText("Email delivery queued.")).toBeVisible();
});

test("coordinator can search for a group and clear finished announcement history", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  const announcementsURL = `${apiURL}/api/groups/${alphaId}/announcements`;
  const schedule = { channelName: "e2e-general", type: "general", scheduledAt: "2026-12-01T12:00:00Z" };
  const scheduled = { id: "scheduled", status: "scheduled", schedule: { ...schedule, message: "Keep scheduled" } };
  const published = { id: "published", status: "published", schedule: { ...schedule, message: "Clear published" } };
  let cleared = false;
  await page.route(announcementsURL, (route) => route.fulfill({ json: cleared ? [scheduled] : [scheduled, published] }));
  await page.route(`${announcementsURL}/history`, (route) => {
    expect(route.request().method()).toBe("DELETE");
    cleared = true;
    return route.fulfill({ status: 204 });
  });

  await page.goto(`${baseURL}/#/announcements`);
  await page.getByRole("button", { name: "Search groups" }).click();
  await page.getByRole("searchbox", { name: "Search by group name" }).fill("aLpHa");
  const group = page.getByRole("combobox", { name: "Group", exact: true });
  await group.click();
  await expect(page.getByRole("option", { name: betaGroup })).toHaveCount(0);
  await page.getByRole("option", { name: alphaGroup, exact: true }).click();

  await page.getByRole("tab", { name: "Sent", exact: true }).click();
  await expect(page.getByText("Clear published")).toBeVisible();
  await page.getByRole("button", { name: "Clear history", exact: true }).click();
  await expect(page.getByText(`Clear finished entries for ${alphaGroup}?`, { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Confirm clear history" }).click();
  await expect(page.getByText("Announcement history cleared.")).toBeVisible();
  await expect(page.getByText("Clear published")).toHaveCount(0);
  await page.getByRole("tab", { name: "Upcoming", exact: true }).click();
  await expect(page.getByText("Keep scheduled")).toBeVisible();
});

test("general announcements target all channels in the selected group", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  const announcementsURL = `${apiURL}/api/groups/${alphaId}/announcements`;
  await page.route(`${apiURL}/api/groups/${alphaId}/resources`, (route) => route.fulfill({ json: [] }));
  await page.route(announcementsURL, async (route) => {
    if (route.request().method() === "GET") return route.fulfill({ json: [] });
    const body = route.request().postDataJSON();
    expect(body).toMatchObject({ channelName: "all", type: "general", message: "Group wide update" });
    return route.fulfill({ status: 202, json: { id: crypto.randomUUID(), groupId: alphaId, schedule: body, status: "scheduled" } });
  });

  await page.goto(`${baseURL}/#/announcements`);
  await page.getByRole("combobox", { name: "Group", exact: true }).click();
  await page.getByRole("option", { name: alphaGroup, exact: true }).click();
  await page.getByRole("tab", { name: "Create announcement" }).click();
  await expect(page.getByText("This general announcement will be sent to every available Slack channel in this group.")).toBeVisible();
  await expect(page.getByLabel("Slack channel")).toHaveCount(0);
  await page.getByLabel("Message").fill("Group wide update");
  await page.getByRole("button", { name: "Schedule announcement" }).click();
  await expect(page.getByText("Announcement scheduled.")).toBeVisible();
});

test("workspace selection on Overview filters the group list and persists across pages", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  await page.goto(`${baseURL}/#/overview`);
  const workspace = page.getByRole("combobox", { name: "Active workspace" });
  await workspace.selectOption("__default__");
  await expect(workspace).toHaveValue("__default__");

  await page.goto(`${baseURL}/#/groups`);
  await expect(page.getByRole("link", { name: alphaGroup, exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: betaGroup, exact: true })).toBeVisible();
});

test("page refresh keeps the session (HttpOnly cookie resume) until logout", async ({ page }) => {
  await signIn(page, accounts.coordinator.email, accounts.coordinator.password);
  await expect(page).toHaveURL(/#\/overview/);
  // Wait for the workspace to be fully interactive (initial queries done).
  await page.goto(`${baseURL}/#/groups`);
  await expect(page.getByText(alphaGroup).first()).toBeVisible({ timeout: 10_000 });

  // The session token stays out of JS-readable storage — persistence rides
  // an HttpOnly cookie (invisible to document.cookie).
  expect(await storageSnapshot(page)).toBe("");

  // F5: the app resumes via /api/auth/resume — no sign-in prompt, the
  // workspace is functional immediately.
  await page.reload({ waitUntil: "load" });
  await expect(page.getByText(alphaGroup).first()).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("Coordinator access required")).toHaveCount(0);

  // Logout kills persistence: refresh lands on the sign-in prompt again.
  await page.locator("header .app-header-danger").first().click();
  await page.waitForURL((url) => url.hash.includes("login"), { timeout: 10_000 });
  await page.goto(`${baseURL}/#/overview`);
  await expect(page.getByText("Coordinator access required").first()).toBeVisible({ timeout: 10_000 });
  await page.waitForTimeout(500);
  expect(await storageSnapshot(page)).toBe("");
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
  // Wait until the initial data queries have finished BEFORE measuring.
  await page.goto(`${baseURL}/#/groups`);
  await expect(page.getByText(alphaGroup).first()).toBeVisible({ timeout: 10_000 });

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
