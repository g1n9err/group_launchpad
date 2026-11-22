import { auditNotificationLines } from "../lib/auditDetail";
// App shell: sidebar + header + footer + routed content. Modular pieces —
// header and footer are isolated components, easy to modify or remove.

import { useEffect, useRef, useState, type ReactNode } from "react";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useSettings } from "../i18n";
import { useRoute } from "../lib/router";
import { api } from "../lib/api";
import { isUnlocked, onAuthChange, useAuthUser } from "../lib/auth";
import { HomeLogo } from "./HomeLogo";
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
  IconMenu,
  IconClose,
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
  const user = useAuthUser();
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
  const isStaff = user?.role === "admin" || user?.role === "coordinator";
  const visibleNav = isStaff ? NAV : user?.role === "lecturer" || user?.role === "ta"
    ? NAV.filter(({ page }) => page === "groups") : [];
  const matchingPages = visibleNav.filter(({ key }) => t(key).toLocaleLowerCase().includes(query));
  const sections = mobile
    ? [{ key: "nav.workspace" as const, pages: visibleNav.map(({ page }) => page) }]
    : NAV_SECTIONS;

  return (
    <nav aria-label={t("nav.primary")} className={mobile ? "mobile-nav" : "flex min-h-0 flex-1 flex-col overflow-y-auto px-4 py-5"}>
      {!mobile && (
        <>
          <HomeLogo size={38} wordmark className="mb-4 flex items-center gap-3 border-b border-border px-2 pb-5 pt-1 text-rail-text" />
          <label className="app-sidebar-search mb-5 flex min-h-11 items-center gap-2 border border-border bg-surface-muted px-3 text-rail-muted">
            <IconSearch className="h-4 w-4" />
            <input ref={searchRef} value={search} onChange={(event) => setSearch(event.target.value)}
              placeholder={t("nav.search")} aria-label={t("nav.search")} className="min-w-0 flex-1 bg-transparent text-sm text-text outline-none placeholder:text-muted" />
            <span className="app-search-shortcut" aria-hidden="true">⌘K</span>
          </label>
        </>
      )}
      {sections.map((section) => {
        const items = (section.pages.map((page) => visibleNav.find((item) => item.page === page)!).filter(Boolean))
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
  const user = useAuthUser();
  return (
    <a href="#/account" className="app-sidebar-account mx-4 mb-4 flex items-center gap-3 border-t border-border px-2 pt-4 transition-colors hover:text-primary" aria-label={t("nav.accountPage")}>
      <span aria-hidden="true" className="app-account-avatar">{(user?.email?.[0] ?? "G").toLocaleUpperCase()}</span>
      <span className="min-w-0">
        <span className="block truncate text-sm font-semibold text-rail-text">{user?.email ?? t("nav.coordinator")}</span>
        <span className="block text-xs text-rail-muted">{t("nav.account")}</span>
      </span>
    </a>
  );
}

// ── Header ─────────────────────────────────────────────────────────────────

function Header({ sidebarOpen, onToggleSidebar }: { sidebarOpen: boolean; onToggleSidebar: () => void }) {
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
        <button type="button" onClick={onToggleSidebar}
          aria-label={t(sidebarOpen ? "nav.sidebarHide" : "nav.sidebarShow")}
          aria-expanded={sidebarOpen} aria-controls="primary-sidebar"
          title={t(sidebarOpen ? "nav.sidebarHide" : "nav.sidebarShow")}
          className="app-header-control hidden h-10 w-10 shrink-0 items-center justify-center p-0 lg:inline-flex">
          {sidebarOpen ? <IconClose className="h-4 w-4" /> : <IconMenu className="h-4 w-4" />}
        </button>
        <HomeLogo size={33} className="shrink-0 lg:hidden" />

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
  const { t, lang } = useSettings();
  const user = useAuthUser();
  const canReadNotifications = user?.role === "admin" || user?.role === "coordinator";
  const [unlocked, setUnlocked] = useState(isUnlocked());
  const [open, setOpen] = useState(false);
  const [confirmDeleteAll, setConfirmDeleteAll] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const queryClient = useQueryClient();
  useEffect(() => onAuthChange(() => {
    const access = isUnlocked();
    setUnlocked(access);
    if (!access) setOpen(false);
  }), []);
  useEffect(() => {
    if (!open) return;
    const closeOutside = (event: PointerEvent) => {
      if (event.target instanceof Node && !menuRef.current?.contains(event.target)) setOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOutside);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);
  const feed = useInfiniteQuery({
    queryKey: ["coordinator-notifications"],
    enabled: unlocked && canReadNotifications,
    retry: false,
    refetchInterval: 60_000,
    initialPageParam: 0,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET("/api/notifications", { params: { query: { limit: 50, offset: pageParam } } });
      if (!data) throw error ?? new Error("Could not load notifications");
      return data;
    },
    getNextPageParam: (lastPage) => {
      const nextOffset = lastPage.page.offset + lastPage.data.length;
      return nextOffset < lastPage.page.total ? nextOffset : undefined;
    },
  });
  const markRead = useMutation({
    mutationFn: async (id: string) => {
      const { error, response } = await api.POST("/api/notifications/{notificationId}/read", {
        params: { path: { notificationId: id } },
      });
      if (!response.ok) throw error ?? new Error("Could not mark notification as read");
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["coordinator-notifications"] }),
  });
  const markAllRead = useMutation({
    mutationFn: async () => {
      const { error, response } = await api.POST("/api/notifications/read-all");
      if (!response.ok) throw error ?? new Error("Could not mark notifications as read");
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["coordinator-notifications"] }),
  });
  const deleteOne = useMutation({
    mutationFn: async (id: string) => {
      const { error, response } = await api.DELETE("/api/notifications/{notificationId}", {
        params: { path: { notificationId: id } },
      });
      if (!response.ok) throw error ?? new Error("Could not delete notification");
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["coordinator-notifications"] }),
  });
  const deleteAll = useMutation({
    mutationFn: async () => {
      const { error, response } = await api.DELETE("/api/notifications");
      if (!response.ok) throw error ?? new Error("Could not delete notifications");
    },
    onSuccess: () => {
      setConfirmDeleteAll(false);
      queryClient.invalidateQueries({ queryKey: ["coordinator-notifications"] });
    },
  });
  const notifications = feed.data?.pages.flatMap((page) => page.data) ?? [];
  const unreadCount = feed.data?.pages[0]?.unreadCount ?? 0;

  if (user?.role !== "admin" && user?.role !== "coordinator") return null;

  return (
    <div ref={menuRef} className="relative shrink-0">
      <button type="button" className="app-header-control app-notification-control"
        aria-expanded={open} aria-haspopup="dialog" onClick={() => setOpen((value) => !value)}>
        <span className="app-notification-icon">
          <IconBell className="h-4 w-4" />
          {unreadCount > 0 && <span className="app-notification-dot" aria-label={t("notifications.available")} title={t("notifications.available")} />}
        </span>
        <span>{t("nav.notifications")}</span>
      </button>
      {open && (
        <section role="dialog" aria-label={t("nav.notifications")}
          className="notification-popover absolute right-0 top-[calc(100%+0.5rem)] z-50 w-[min(25rem,calc(100vw-2rem))] overflow-hidden rounded-xl border border-border bg-surface shadow-card">
          <div className="flex shrink-0 items-center justify-between gap-3 border-b border-border px-4 py-3">
            <div className="min-w-0">
              <h2 className="text-sm font-semibold text-text">{t("nav.notifications")}</h2>
              <p className="mt-0.5 text-[0.68rem] text-muted">{t("notifications.count", { count: feed.data?.pages[0]?.page.total ?? 0 })}</p>
            </div>
            <div className="flex shrink-0 items-center gap-3">
              {unreadCount > 0 && <button type="button" disabled={markAllRead.isPending}
                className="text-xs font-semibold text-primary hover:underline disabled:opacity-50"
                onClick={() => markAllRead.mutate()}>{t("notifications.markAllRead")}</button>}
              {notifications.length > 0 && <button type="button" disabled={deleteAll.isPending}
                className="text-xs font-semibold text-danger-text hover:underline disabled:opacity-50"
                onClick={() => setConfirmDeleteAll((current) => !current)}>{t("notifications.deleteAll")}</button>}
            </div>
          </div>
          {confirmDeleteAll && <div className="notification-confirm border-b border-border px-4 py-3">
            <p className="text-xs text-text">{t("notifications.deleteAllPrompt")}</p>
            <div className="mt-2 flex justify-end gap-2">
              <button type="button" className="notification-confirm-cancel" onClick={() => setConfirmDeleteAll(false)}>{t("notifications.cancel")}</button>
              <button type="button" disabled={deleteAll.isPending} className="notification-confirm-delete"
                onClick={() => deleteAll.mutate()}>{t("notifications.deleteAllConfirm")}</button>
            </div>
          </div>}
          {!unlocked && <p className="px-4 py-5 text-sm text-muted">{t("notifications.unlockNeeded")}</p>}
          {unlocked && feed.isPending && <p className="px-4 py-5 text-sm text-muted">{t("notifications.loading")}</p>}
          {unlocked && feed.isError && <p role="alert" className="px-4 py-5 text-sm text-danger-text">{t("notifications.loadFailed")}</p>}
          {unlocked && notifications.length === 0 && !feed.isPending && <p className="px-4 py-5 text-sm text-muted">{t("notifications.empty")}</p>}
          {deleteOne.isError || deleteAll.isError ? <p role="alert" className="px-4 py-2 text-xs text-danger-text">{t("notifications.deleteFailed")}</p> : null}
          {notifications.length > 0 && <div className="notification-list min-h-0 flex-1 overflow-y-auto overscroll-contain">
            {notifications.map((notification) => {
              let title = notification.title;
              let detail = notification.detail;
              const auditLines = notification.kind === "audit_inconsistency" ? auditNotificationLines(detail, t) : [];
              switch (notification.kind) {
                case "audit_inconsistency":
                  title = t("notifications.auditTitle"); detail = auditLines.length ? undefined : t("notifications.auditDetail"); break;
              }
              return <article key={notification.id} className={`notification-row ${notification.readAt ? "is-read" : "is-unread"}`}>
                <button type="button" disabled={!!notification.readAt || markRead.isPending}
                  onClick={() => markRead.mutate(notification.id)}
                  className="notification-row-content min-w-0 flex-1 text-left disabled:cursor-default">
                  <span className="flex items-start justify-between gap-3">
                    <span className="text-sm font-semibold text-text">{title}</span>
                    {!notification.readAt && <span className="mt-1 h-2 w-2 shrink-0 rounded-full bg-primary" aria-label={t("notifications.unread")} />}
                  </span>
                  {auditLines.map((line, index) => <span key={index} className="mt-1 block break-words text-xs text-muted">{line}</span>)}
                  {detail && <span className="mt-1 block line-clamp-2 text-xs text-muted">{detail}</span>}
                  <span className="mt-1 block text-[0.7rem] text-muted">{new Date(notification.createdAt).toLocaleString(lang)}</span>
                </button>
                <button type="button" className="notification-delete-one" aria-label={t("notifications.deleteOne", { title })}
                  title={t("notifications.deleteOneTitle")} disabled={deleteOne.isPending}
                  onClick={() => deleteOne.mutate(notification.id)}>
                  <span aria-hidden="true">×</span>
                </button>
              </article>;
            })}
          </div>}
          {feed.hasNextPage && <div className="notification-load-more border-t border-border px-4 py-2">
            <button type="button" disabled={feed.isFetchingNextPage} onClick={() => void feed.fetchNextPage()}>
              {feed.isFetchingNextPage ? t("notifications.loadingMore") : t("notifications.loadMore")}
            </button>
          </div>}
        </section>
      )}
    </div>
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
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    try { return window.localStorage.getItem("launchpad.sidebar-hidden") !== "true"; }
    catch { return true; }
  });
  const toggleSidebar = () => setSidebarOpen((open) => {
    const next = !open;
    try { window.localStorage.setItem("launchpad.sidebar-hidden", String(!next)); } catch { /* storage is optional */ }
    return next;
  });
  return (
    <div className="app-shell flex min-h-screen">
      {/* Desktop sidebar */}
      <aside id="primary-sidebar" className={`app-sidebar fixed inset-y-0 left-0 z-30 hidden w-60 bg-rail transition-transform duration-200 lg:block ${sidebarOpen ? "translate-x-0" : "-translate-x-full"}`} aria-hidden={!sidebarOpen} inert={!sidebarOpen}>
        <div className="flex h-full flex-col">
          <Navigation />
          <SidebarAccount />
        </div>
      </aside>

      {/* Content column: full available width, sidebar offset on desktop */}
      <div className={`flex min-h-screen min-w-0 flex-1 flex-col transition-[padding] duration-200 ${sidebarOpen ? "lg:pl-60" : "lg:pl-0"}`}>
        <div className="sticky top-0 z-20">
          <Header sidebarOpen={sidebarOpen} onToggleSidebar={toggleSidebar} />
          <Navigation mobile />
        </div>
        <main id="main-content" className="mx-auto w-full max-w-[100rem] flex-1 px-4 py-6 sm:px-7 lg:px-10 lg:py-8">{children}</main>
        <Footer />
      </div>
    </div>
  );
}
