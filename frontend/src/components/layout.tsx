// App shell: sidebar + header + footer + routed content. Modular pieces —
// header and footer are isolated components, easy to modify or remove.

import { useEffect, useRef, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { useSettings } from "../i18n";
import { useRoute } from "../lib/router";
import { api } from "../lib/api";
import { isUnlocked, onAuthChange } from "../lib/auth";
import { CoordinatorSessionControl, useCoordinatorLock } from "./auth";
import {
  IconAnnouncements,
  IconAutomation,
  IconBack,
  IconBell,
  IconGroups,
  IconHome,
  IconIntegrations,
  IconIntakeForms,
  IconOverview,
  IconSettings,
  IconSearch,
  IconMoon,
  IconSun,
  IconStudents,
} from "./icons";

// ── Sidebar ────────────────────────────────────────────────────────────────

const NAV: { page: string; icon: (p: { className?: string }) => ReactNode; key: Parameters<ReturnType<typeof useSettings>["t"]>[0] }[] = [
  { page: "home", icon: IconHome, key: "nav.home" },
  { page: "overview", icon: IconOverview, key: "nav.overview" },
  { page: "groups", icon: IconGroups, key: "nav.groups" },
  { page: "students", icon: IconStudents, key: "nav.students" },
  { page: "intake-forms", icon: IconIntakeForms, key: "nav.intakeForms" },
  { page: "announcements", icon: IconAnnouncements, key: "nav.announcements" },
  { page: "integrations", icon: IconIntegrations, key: "nav.integrations" },
  { page: "automation", icon: IconAutomation, key: "nav.automation" },
  { page: "settings", icon: IconSettings, key: "nav.settings" },
];

const NAV_SECTIONS: { key: Parameters<ReturnType<typeof useSettings>["t"]>[0]; pages: string[] }[] = [
  { key: "nav.workspace", pages: ["home", "overview", "groups", "students"] },
  { key: "nav.communication", pages: ["intake-forms", "announcements"] },
  { key: "nav.setup", pages: ["integrations", "automation", "settings"] },
] as const;

function Navigation({ mobile = false }: { mobile?: boolean }) {
  const { t } = useSettings();
  const route = useRoute();
  const [search, setSearch] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (mobile) return;
    const onShortcut = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        searchRef.current?.focus();
      }
      if (event.key === "Escape" && document.activeElement === searchRef.current) {
        setSearch("");
        searchRef.current?.blur();
      }
    };
    window.addEventListener("keydown", onShortcut);
    return () => window.removeEventListener("keydown", onShortcut);
  }, [mobile]);

  const query = mobile ? "" : search.trim().toLocaleLowerCase();
  const matchingPages = NAV.filter(({ key }) => t(key).toLocaleLowerCase().includes(query));
  const sections = mobile
    ? [{ key: "nav.workspace" as const, pages: NAV.map(({ page }) => page) }]
    : NAV_SECTIONS;

  return (
    <nav aria-label={t("nav.primary")} className={mobile ? "mobile-nav" : "flex min-h-0 flex-1 flex-col overflow-y-auto px-4 py-5"}>
      {!mobile && (
        <>
          <a href="#/" aria-label="Group Launchpad" className="mb-4 flex items-center gap-3 border-b border-border px-2 pb-5 pt-1 text-rail-text">
            <img src="/favicon.png" width="38" height="38" alt="" />
            <span className="app-wordmark">Group <strong>Launchpad</strong></span>
          </a>
          <label className="app-sidebar-search mb-5 flex min-h-11 items-center gap-2 border border-border bg-surface-muted px-3 text-rail-muted">
            <IconSearch className="h-4 w-4" />
            <input ref={searchRef} value={search} onChange={(event) => setSearch(event.target.value)}
              placeholder={t("nav.search")} aria-label={t("nav.search")} className="min-w-0 flex-1 bg-transparent text-sm text-text outline-none placeholder:text-muted" />
            <span className="app-search-shortcut" aria-hidden="true">⌘K</span>
          </label>
        </>
      )}
      {sections.map((section) => {
        const items = (section.pages.map((page) => NAV.find((item) => item.page === page)!).filter(Boolean))
          .filter(({ key }) => !query || t(key).toLocaleLowerCase().includes(query));
        if (!items.length) return null;
        return <div key={section.key} className={mobile ? "contents" : "mb-5"}>
        {!mobile && <p className="app-sidebar-section mb-1 px-2 pt-1">{t(section.key)}</p>}
        {items.map(({ page, icon: Icon, key }) => {
        const active = route.page === page || (page === "groups" && route.page === "group-details");
        const desktopClass = `app-nav-link flex min-h-11 items-center gap-3 border-l-2 px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-rail-accent
          ${active ? "border-rail-accent bg-rail-active text-rail-text" : "border-transparent text-rail-muted hover:bg-rail-hover hover:text-rail-text"}`;
        return (
          <a key={page} href={page === "home" ? "#/" : `#/${page}`}
            aria-current={active ? "page" : undefined} className={mobile ? "mobile-nav-link" : desktopClass}>
            <Icon className={mobile ? "h-4 w-4" : active ? "h-[1.1rem] w-[1.1rem] text-rail-accent" : "h-[1.1rem] w-[1.1rem]"} />
            <span>{t(key)}</span>
          </a>
        );
        })}
        </div>;
      })}
      {!mobile && query && matchingPages.length === 0 && <p className="px-3 py-2 text-xs text-rail-muted">{t("nav.noResults")}</p>}
    </nav>
  );
}

function SidebarAccount() {
  const { t } = useSettings();
  return (
    <a href="#/account" className="app-sidebar-account mx-4 mb-4 flex items-center gap-3 border-t border-border px-2 pt-4 transition-colors hover:text-primary" aria-label={t("nav.accountPage")}>
      <span aria-hidden="true" className="app-account-avatar">C</span>
      <span className="min-w-0">
        <span className="block truncate text-sm font-semibold text-rail-text">{t("nav.coordinator")}</span>
        <span className="block text-xs text-rail-muted">{t("nav.account")}</span>
      </span>
    </a>
  );
}

// ── Header ─────────────────────────────────────────────────────────────────

function Header() {
  const { t, theme, toggleTheme } = useSettings();
  const route = useRoute();
  useCoordinatorLock(); // attaches the cache-clearing listener

  // Page title + breadcrumb for the current route.
  let title = t("nav.home");
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
  } else if (route.page === "account") {
    title = t("nav.accountPage");
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

        <NotificationControl />
        <button type="button" onClick={toggleTheme}
          aria-label={t(theme === "light" ? "nav.themeDark" : "nav.themeLight")}
          title={t(theme === "light" ? "nav.themeDark" : "nav.themeLight")}
          className="app-header-control app-theme-control shrink-0 gap-2">
          {theme === "light" ? <IconSun className="h-[1.1rem] w-[1.1rem]" /> : <IconMoon className="h-[1.1rem] w-[1.1rem]" />}
          <span>{t(theme === "light" ? "nav.themeLightShort" : "nav.themeDarkShort")}</span>
        </button>
        <div className="flex min-h-10 shrink-0 items-center">
          <CoordinatorSessionControl />
        </div>
      </div>
    </header>
  );
}

function NotificationControl() {
  const { t } = useSettings();
  const [unlocked, setUnlocked] = useState(isUnlocked());
  useEffect(() => onAuthChange(() => setUnlocked(isUnlocked())), []);
  const feed = useQuery({
    queryKey: ["header-audit-notifications"],
    enabled: unlocked,
    retry: false,
    refetchInterval: 60_000,
    queryFn: async () => {
      const { data, error } = await api.GET("/api/audit-notifications");
      if (!data) throw error ?? new Error("Could not load notifications");
      return data;
    },
  });
  const hasNotifications = !!feed.data && (feed.data.openIssues.length > 0 || feed.data.notifications.length > 0);

  return (
    <a href="#/automation" className="app-header-control app-notification-control shrink-0">
      <span className="app-notification-icon">
        <IconBell className="h-4 w-4" />
        {hasNotifications && <span className="app-notification-dot" aria-label={t("notifications.available")} title={t("notifications.available")} />}
      </span>
      <span>{t("nav.notifications")}</span>
    </a>
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
        <div className="flex h-full flex-col">
          <Navigation />
          <SidebarAccount />
        </div>
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
