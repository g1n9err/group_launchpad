// App shell: sidebar + header + footer + routed content. Modular pieces —
// header and footer are isolated components, easy to modify or remove.

import { useEffect, useState, type ReactNode } from "react";
import { useSettings, LANGUAGE_LABELS, LANGUAGES, type Lang } from "../i18n";
import { navigate, useRoute } from "../lib/router";
import {
  IconAnnouncements,
  IconAutomation,
  IconBack,
  IconClose,
  IconGroups,
  IconIntegrations,
  IconMenu,
  IconOverview,
  IconSettings,
  IconStudents,
} from "./icons";

// ── Sidebar ────────────────────────────────────────────────────────────────

const NAV: { page: string; icon: (p: { className?: string }) => ReactNode; key: Parameters<ReturnType<typeof useSettings>["t"]>[0] }[] = [
  { page: "overview", icon: IconOverview, key: "nav.overview" },
  { page: "groups", icon: IconGroups, key: "nav.groups" },
  { page: "students", icon: IconStudents, key: "nav.students" },
  { page: "integrations", icon: IconIntegrations, key: "nav.integrations" },
  { page: "automation", icon: IconAutomation, key: "nav.automation" },
  { page: "announcements", icon: IconAnnouncements, key: "nav.announcements" },
  { page: "settings", icon: IconSettings, key: "nav.settings" },
];

function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  const { t } = useSettings();
  const route = useRoute();
  return (
    <nav className="flex h-full flex-col gap-1 p-3">
      <div className="px-3 pb-4 pt-2">
        <span className="text-base font-bold tracking-tight text-text">Group Launchpad</span>
      </div>
      {NAV.map(({ page, icon: Icon, key }) => {
        const active = route.page === page || (page === "groups" && route.page === "group-details");
        return (
          <button
            key={page}
            onClick={() => {
              navigate(page === "overview" ? "/" : `/${page}`);
              onNavigate?.();
            }}
            aria-current={active ? "page" : undefined}
            className={`flex items-center gap-3 rounded-lg px-3 py-2 text-sm font-medium transition-colors ${
              active
                ? "bg-surface-muted text-primary"
                : "text-muted hover:bg-surface-muted/60 hover:text-text"
            }`}
          >
            {Icon({ className: "h-5 w-5" })}
            {t(key)}
          </button>
        );
      })}
    </nav>
  );
}

// ── Header ─────────────────────────────────────────────────────────────────

function Header({ onMenu }: { onMenu: () => void }) {
  const { t, lang, setLang, theme, toggleTheme } = useSettings();
  const route = useRoute();

  // Page title + breadcrumb for the current route.
  let title = t("nav.overview");
  let crumbs: string[] = [];
  if (route.page === "groups") {
    title = t("nav.groups");
  } else if (route.page === "group-details") {
    title = t("groups.details");
    crumbs = [t("nav.groups")];
  } else if (NAV.some((n) => n.page === route.page)) {
    const item = NAV.find((n) => n.page === route.page)!;
    title = t(item.key);
  }

  return (
    <header className="sticky top-0 z-20 border-b border-border bg-surface">
      <div className="flex items-center gap-3 px-4 py-3 lg:px-6">
        <button
          type="button"
          onClick={onMenu}
          aria-label={t("header.menu")}
          className="rounded-md border border-border p-2 text-text transition-colors hover:bg-surface-muted lg:hidden"
        >
          <IconMenu />
        </button>

        <div className="min-w-0 flex-1">
          <h2 className="truncate text-sm font-semibold text-text">{title}</h2>
          {crumbs.length > 0 && (
            <div className="flex items-center gap-1 text-xs text-muted">
              {crumbs.map((c) => (
                <span key={c} className="flex items-center gap-1">
                  {c}
                  <IconBack className="h-3 w-3 rotate-180" />
                </span>
              ))}
              <span className="truncate">{title}</span>
            </div>
          )}
        </div>

        <label className="sr-only" htmlFor="header-lang">{t("nav.language")}</label>
        <select
          id="header-lang"
          value={lang}
          onChange={(e) => setLang(e.target.value as Lang)}
          className="rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-text outline-none focus:ring-2 focus:ring-primary/40"
        >
          {LANGUAGES.map((l) => (
            <option key={l} value={l}>{LANGUAGE_LABELS[l]}</option>
          ))}
        </select>

        <button
          type="button"
          onClick={toggleTheme}
          aria-label={theme === "light" ? t("nav.themeDark") : t("nav.themeLight")}
          title={theme === "light" ? t("nav.themeDark") : t("nav.themeLight")}
          className="rounded-md border border-border bg-surface p-2 text-text transition-colors hover:bg-surface-muted"
        >
          {theme === "light" ? (
            <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
            </svg>
          ) : (
            <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" aria-hidden="true">
              <circle cx="12" cy="12" r="4" />
              <path d="M12 2v2m0 16v2M4.93 4.93l1.41 1.41m11.32 11.32 1.41 1.41M2 12h2m16 0h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
            </svg>
          )}
        </button>
      </div>
    </header>
  );
}

// ── Footer ─────────────────────────────────────────────────────────────────

function Footer() {
  const { t } = useSettings();
  return (
    <footer className="border-t border-border px-6 py-3">
      <p className="text-xs text-muted">{t("footer.copyright")}</p>
    </footer>
  );
}

// ── Shell ──────────────────────────────────────────────────────────────────

export function AppShell({ children }: { children: ReactNode }) {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const route = useRoute();

  // Close the mobile drawer on navigation.
  useEffect(() => {
    setDrawerOpen(false);
  }, [route.page, route.id]);

  return (
    <div className="flex min-h-screen">
      {/* Desktop sidebar */}
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-60 border-r border-border bg-surface lg:block">
        <SidebarNav />
      </aside>

      {/* Mobile drawer */}
      {drawerOpen && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div
            className="absolute inset-0 bg-black/40"
            onClick={() => setDrawerOpen(false)}
            aria-hidden="true"
          />
          <aside className="absolute inset-y-0 left-0 w-64 border-r border-border bg-surface shadow-xl">
            <button
              type="button"
              onClick={() => setDrawerOpen(false)}
              aria-label="Close menu"
              className="absolute right-3 top-3 rounded-md p-1.5 text-muted hover:bg-surface-muted"
            >
              <IconClose className="h-4 w-4" />
            </button>
            <SidebarNav onNavigate={() => setDrawerOpen(false)} />
          </aside>
        </div>
      )}

      {/* Content column: full available width, sidebar offset on desktop */}
      <div className="flex min-h-screen min-w-0 flex-1 flex-col lg:pl-60">
        <Header onMenu={() => setDrawerOpen((v) => !v)} />
        <main className="flex-1 px-4 py-6 sm:px-6 lg:px-8">{children}</main>
        <Footer />
      </div>
    </div>
  );
}
