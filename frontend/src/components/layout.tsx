// App shell: sidebar + header + footer + routed content. Modular pieces —
// header and footer are isolated components, easy to modify or remove.

import type { ReactNode } from "react";
import { useSettings } from "../i18n";
import { useRoute } from "../lib/router";
import {
  IconAnnouncements,
  IconAutomation,
  IconBack,
  IconGroups,
  IconIntegrations,
  IconIntakeForms,
  IconOverview,
  IconSettings,
  IconStudents,
} from "./icons";

// ── Sidebar ────────────────────────────────────────────────────────────────

const NAV: { page: string; icon: (p: { className?: string }) => ReactNode; key: Parameters<ReturnType<typeof useSettings>["t"]>[0] }[] = [
  { page: "overview", icon: IconOverview, key: "nav.overview" },
  { page: "groups", icon: IconGroups, key: "nav.groups" },
  { page: "students", icon: IconStudents, key: "nav.students" },
  { page: "intake-forms", icon: IconIntakeForms, key: "nav.intakeForms" },
  { page: "announcements", icon: IconAnnouncements, key: "nav.announcements" },
  { page: "integrations", icon: IconIntegrations, key: "nav.integrations" },
  { page: "automation", icon: IconAutomation, key: "nav.automation" },
  { page: "settings", icon: IconSettings, key: "nav.settings" },
];

function Navigation({ mobile = false }: { mobile?: boolean }) {
  const { t } = useSettings();
  const route = useRoute();

  return (
    <nav aria-label={t("nav.primary")} className={mobile ? "mobile-nav" : "flex h-full flex-col gap-1.5 overflow-y-auto px-4 py-5"}>
      {!mobile && (
        <a href="#/" aria-label="Group Launchpad" className="mb-4 flex items-center gap-3 border-b border-border px-2 pb-5 pt-1 text-rail-text">
          <img src="/favicon.png" width="38" height="38" alt="" />
          <span className="app-wordmark">Group <strong>Launchpad</strong></span>
        </a>
      )}
      {NAV.map(({ page, icon: Icon, key }) => {
        const active = route.page === page || (page === "groups" && route.page === "group-details");
        const desktopClass = `app-nav-link flex min-h-11 items-center gap-3 border-l-2 px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-rail-accent
          ${active ? "border-rail-accent bg-rail-active text-rail-text" : "border-transparent text-rail-muted hover:bg-rail-hover hover:text-rail-text"}`;
        return (
          <a key={page} href={page === "overview" ? "#/" : `#/${page}`}
            aria-current={active ? "page" : undefined} className={mobile ? "mobile-nav-link" : desktopClass}>
            <Icon className={mobile ? "h-4 w-4" : active ? "h-[1.1rem] w-[1.1rem] text-rail-accent" : "h-[1.1rem] w-[1.1rem]"} />
            <span>{t(key)}</span>
          </a>
        );
      })}
    </nav>
  );
}

// ── Header ─────────────────────────────────────────────────────────────────

function Header() {
  const { t, theme, toggleTheme } = useSettings();
  const route = useRoute();

  // Page title + breadcrumb for the current route.
  let title = t("nav.overview");
  let parent: string | undefined;
  if (route.page === "groups" && route.id === "new") {
    title = t("form.title");
    parent = t("nav.groups");
  } else if (route.page === "groups" && !route.id) {
    title = t("nav.groups");
  } else if (route.page === "group-details" || route.page === "groups") {
    title = t("groups.details");
    parent = t("nav.groups");
  } else if (NAV.some((n) => n.page === route.page)) {
    const item = NAV.find((n) => n.page === route.page)!;
    title = t(item.key);
  }

  return (
    <header className="border-b border-border bg-surface/90 backdrop-blur-md">
      <div className="mx-auto flex max-w-[100rem] items-center gap-3 px-4 py-3 sm:px-7 lg:px-10">
        <a href="#/" aria-label="Group Launchpad" className="shrink-0 lg:hidden">
          <img src="/favicon.png" width="33" height="33" alt="" />
        </a>

        <div className="min-w-0 flex-1">
          {!parent && <span className="block truncate text-sm font-semibold text-muted">{title}</span>}
          {parent && (
            <div className="flex items-center gap-1 text-xs text-muted">
              <a href="#/groups" className="font-semibold text-primary hover:underline">{parent}</a>
              <IconBack className="h-3 w-3 rotate-180" />
              <span className="truncate">{title}</span>
            </div>
          )}
        </div>

        <a href="#/automation"
          className="inline-flex min-h-10 shrink-0 items-center border border-border bg-surface px-2 text-xs font-semibold text-text transition-colors hover:border-primary hover:text-primary sm:px-3 sm:text-sm">
          {t("nav.notifications")}
        </a>
        <button type="button" onClick={toggleTheme}
          className="min-h-10 shrink-0 border border-border bg-surface px-2 text-xs font-semibold text-text transition-colors hover:border-primary hover:text-primary sm:px-3 sm:text-sm">
          {t(theme === "light" ? "nav.themeDark" : "nav.themeLight")}
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
  return (
    <div className="app-shell flex min-h-screen">
      {/* Desktop sidebar */}
      <aside className="app-sidebar fixed inset-y-0 left-0 z-30 hidden w-60 bg-rail lg:block">
        <Navigation />
      </aside>

      {/* Content column: full available width, sidebar offset on desktop */}
      <div className="flex min-h-screen min-w-0 flex-1 flex-col lg:pl-60">
        <div className="sticky top-0 z-20">
          <Header />
          <Navigation mobile />
        </div>
        <main id="main-content" className="mx-auto w-full max-w-[100rem] flex-1 px-4 py-6 sm:px-7 lg:px-10 lg:py-8">{children}</main>
        <Footer />
      </div>
    </div>
  );
}
