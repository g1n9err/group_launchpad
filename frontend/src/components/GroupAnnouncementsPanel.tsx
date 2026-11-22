import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useSettings } from "../i18n";
import { api } from "../lib/api";
import { fireLock, getAuthHeader, useAuthUser } from "../lib/auth";
import type { components } from "../lib/schema";
import { Card, Chip } from "./ui";

type Filter = "upcoming" | "published" | "failed";
type Announcement = components["schemas"]["ScheduledAnnouncement"];

export function GroupAnnouncementsPanel({ groupId }: { groupId: string }) {
  const { t, lang } = useSettings();
  const user = useAuthUser();
  const authorization = user ? getAuthHeader().Authorization ?? "" : "";
  const [filter, setFilter] = useState<Filter>("upcoming");
  const query = useQuery({
    queryKey: ["group-announcements", groupId],
    enabled: Boolean(authorization),
    refetchInterval: 60_000,
    queryFn: async ({ signal }) => {
      const { data, response, error } = await api.GET("/api/groups/{groupId}/announcements", {
        headers: { Authorization: authorization }, params: { path: { groupId } }, signal,
      });
      if (response.status === 401) { fireLock(); throw new Error("Unauthorized"); }
      if (!data) throw error ?? new Error("Could not load announcements");
      return data;
    },
  });
  const items = query.data?.filter((item) => filter === "upcoming"
    ? item.status === "scheduled" || item.status === "publishing"
    : filter === "published" ? item.status === "published"
      : item.status === "failed" || item.status === "unknown").sort((a, b) => filter === "upcoming"
        ? Date.parse(a.schedule.scheduledAt) - Date.parse(b.schedule.scheduledAt)
        : Date.parse(b.schedule.scheduledAt) - Date.parse(a.schedule.scheduledAt));
  const dateTime = new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" });

  return (
    <Card className="space-y-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-base font-semibold text-text">{t("announcements.title")}</h2>
        <div className="flex flex-wrap gap-2">
          <button type="button" className="rounded-md border border-border px-3 py-1.5 text-xs font-semibold text-text hover:bg-surface-muted"
            disabled={query.isFetching} onClick={() => void query.refetch()}>{t("audit.refresh")}</button>
          {user?.role === "admin" || user?.role === "coordinator" ? (
            <a className="app-action min-h-8 px-3 py-1 text-xs" href={`#/announcements/${groupId}`}>{t("announcements.create")}</a>
          ) : null}
        </div>
      </div>
      <div className="flex flex-wrap gap-2 border-b border-border" role="tablist" aria-label={t("announcements.filter")}>
        {(["upcoming", "published", "failed"] as const).map((key) => (
          <button key={key} type="button" role="tab" aria-selected={filter === key}
            className={`min-h-10 border-b-2 px-3 text-sm font-semibold transition ${filter === key ? "border-primary text-primary" : "border-transparent text-muted hover:text-text"}`}
            onClick={() => setFilter(key)}>{t(`announcements.filter.${key}`)}</button>
        ))}
      </div>
      {!authorization && <a href="#/login" className="app-action">{t("auth.signIn")}</a>}
      {authorization && query.isPending && <p role="status" className="text-sm text-muted">{t("announcements.loading")}</p>}
      {authorization && query.isError && <p role="alert" className="text-sm text-danger-text">{t("announcements.loadFailed")}</p>}
      {authorization && query.data && items?.length === 0 && <p className="text-sm text-muted">{t(`announcements.filter.empty.${filter}`)}</p>}
      {!!items?.length && <ul className="divide-y divide-border">
        {items.map((item: Announcement) => (
          <li key={item.id} className="space-y-1 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <strong className="text-text">{item.schedule.channelName === "all" ? t("announcements.allChannelsShort") : `#${item.schedule.channelName}`}</strong>
              <Chip tone={filter === "failed" ? "danger" : filter === "published" ? "success" : "info"}>{t(`announcements.status.${item.status}`)}</Chip>
            </div>
            <p className="text-muted">{dateTime.format(new Date(item.publishedAt ?? item.schedule.scheduledAt))}</p>
            <p className="whitespace-pre-wrap break-words text-text">{item.schedule.message}</p>
            {item.error && <p className="break-words text-danger-text">{item.error}</p>}
          </li>
        ))}
      </ul>}
    </Card>
  );
}
