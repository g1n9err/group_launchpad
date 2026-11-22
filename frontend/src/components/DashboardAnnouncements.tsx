import { useEffect, useState } from "react";
import { useSettings } from "../i18n";
import { api, type Group } from "../lib/api";
import type { components } from "../lib/schema";
import { Card, Chip } from "./ui";
import { intakeInputClass } from "../lib/intakeForm";

type Entry = { group: Group; announcement: components["schemas"]["ScheduledAnnouncement"] };

export function DashboardAnnouncements({ groups, authorization, onUnauthorized, reload, filter }: {
  groups: Group[]; authorization: string; onUnauthorized: () => void; reload: number;
  filter: "upcoming" | "published" | "failed";
}) {
  const { t, lang } = useSettings();
  const [result, setResult] = useState<{ entries: Entry[]; incomplete: boolean }>();
  const [groupSearch, setGroupSearch] = useState("");

  useEffect(() => {
    const controller = new AbortController();

    let loading = false;
    async function load() {
      if (loading) return;
      loading = true;
      const entries: Entry[] = [];
      let incomplete = false;
      for (let offset = 0; offset < groups.length; offset += 6) {
        if (controller.signal.aborted) return;
        const batch = await Promise.allSettled(groups.slice(offset, offset + 6).map(async (group) => {
          const { data, response } = await api.GET("/api/groups/{groupId}/announcements", {
            headers: { Authorization: authorization }, params: { path: { groupId: group.id } }, signal: controller.signal,
          });
          if (response.status === 401 && !controller.signal.aborted) {
            controller.abort();
            onUnauthorized();
          }
          if (!data) throw new Error("Could not load group announcements");
          return data.map((announcement) => ({ group, announcement }));
        }));
        for (const item of batch) {
          if (item.status === "fulfilled") entries.push(...item.value);
          else incomplete = true;
        }
      }
      loading = false;
      if (!controller.signal.aborted) setResult({ entries, incomplete });
    }

    void load();
    const timer = window.setInterval(() => void load(), 60_000);
    return () => { controller.abort(); window.clearInterval(timer); };
  }, [groups, authorization, onUnauthorized, reload]);

  const time = new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" });
  const visible = result?.entries.filter(({ announcement, group }) => {
    const statusMatches = filter === "upcoming"
      ? announcement.status === "scheduled" || announcement.status === "publishing"
      : filter === "published" ? announcement.status === "published"
        : announcement.status === "failed" || announcement.status === "unknown";
    return statusMatches && group.name.toLocaleLowerCase().includes(groupSearch.trim().toLocaleLowerCase());
  }).sort((a, b) => filter === "upcoming"
    ? Date.parse(a.announcement.schedule.scheduledAt) - Date.parse(b.announcement.schedule.scheduledAt)
    : Date.parse(b.announcement.schedule.scheduledAt) - Date.parse(a.announcement.schedule.scheduledAt));

  return (
    <Card className="space-y-4 p-5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="font-semibold text-text">{t("announcements.title")}</h2>
        <a href="#/announcements" className="text-sm font-semibold text-primary hover:underline">{t("announcements.open")}</a>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <label className="ms-auto block w-64 max-w-full text-sm">
          <span className="sr-only">{t("announcements.groupSearch")}</span>
          <input type="search" className={intakeInputClass} placeholder={t("announcements.groupSearch")}
            value={groupSearch} onChange={(event) => setGroupSearch(event.target.value)} />
        </label>
      </div>
      {!result && <p role="status" className="text-sm text-muted">{t("announcements.loading")}</p>}
      {result?.incomplete && <p role="alert" className="text-sm text-danger-text">{t("overview.incompleteAnnouncements")}</p>}
      {visible?.length === 0 && !result?.incomplete && <p className="text-sm text-muted">{t(`announcements.filter.empty.${filter}`)}</p>}
      {!!visible?.length && <ul className="divide-y divide-border">
        {visible.map(({ group, announcement }) => (
          <li key={announcement.id} className="space-y-1 py-3 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <a className="font-medium text-primary underline" href="#/announcements">{group.name}</a>
              <Chip tone={filter === "failed" ? "danger" : filter === "published" ? "success" : "info"}>
                {t(`announcements.status.${announcement.status}`)}
              </Chip>
            </div>
            <p className="text-muted">#{announcement.schedule.channelName} · {time.format(new Date(announcement.schedule.scheduledAt))}</p>
            <p className="whitespace-pre-wrap break-words text-text">{announcement.schedule.message}</p>
            {announcement.error && <p className="break-words text-danger-text">{announcement.error}</p>}
          </li>
        ))}
      </ul>}
    </Card>
  );
}
