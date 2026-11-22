import { TranslateDraft } from "../components/TranslateDraft";
import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Card, Chip, PageTitle } from "../components/ui";
import { AppSelect } from "../components/AppSelect";
import { DateTimePicker } from "../components/DateTimePicker";
import { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";
import { api } from "../lib/api";
import { intakeActionClass, intakeButtonClass, intakeInputClass } from "../lib/intakeForm";
import { useIntakeGroups } from "../lib/queries";
import { useRoute } from "../lib/router";
import type { components } from "../lib/schema";
import { fireLock, getAuthHeader, useAuthUser } from "../lib/auth";
import { useWorkspaceScope } from "../lib/workspaceScope";

type Schedule = components["schemas"]["AnnouncementSchedule"];
type Announcement = components["schemas"]["ScheduledAnnouncement"];

function isHistoryEntry(item: Announcement) {
  return item.status !== "scheduled" && item.status !== "publishing";
}

const types = ["welcome", "staff_intro", "rules", "reminder", "general"] as const;

export function AnnouncementsPage() {
  const { t } = useSettings();
  const route = useRoute();
  const user = useAuthUser();
  const authorization = user ? getAuthHeader().Authorization ?? "" : "";
  const [groupId, setGroupId] = useState(() => route.id ?? "");
  const [panel, setPanel] = useState<"create" | "history">("history");
  const [historyFilter, setHistoryFilter] = useState<"upcoming" | "published" | "failed">("upcoming");
  const [groupSearch, setGroupSearch] = useState("");
  const [showGroupSearch, setShowGroupSearch] = useState(false);
  const groups = useIntakeGroups(Boolean(authorization));
  const workspaceScope = useWorkspaceScope();
  const visibleGroups = workspaceScope.filterGroups(groups.data);
  const search = groupSearch.trim().toLocaleLowerCase();
  const nameMatches = visibleGroups?.filter((group) => group.name.toLocaleLowerCase().includes(search)) ?? [];
  const exactMatches = search ? nameMatches.filter((group) => group.name.trim().toLocaleLowerCase() === search) : [];
  const matches = exactMatches.length ? exactMatches : nameMatches;
  const selected = visibleGroups?.find((group) => group.id === groupId);
  useEffect(() => {
    if (visibleGroups && !visibleGroups.some((group) => group.id === groupId)) setGroupId(visibleGroups[0]?.id ?? "");
  }, [visibleGroups, groupId]);
  const options = selected && !matches.some((group) => group.id === groupId) ? [selected, ...matches] : matches;
  const onUnauthorized = useCallback(() => fireLock(), []);

  return (
    <div className="space-y-5">
      <PageTitle title={t("announcements.title")} />
      <Card className="p-5">
        {authorization ? (
          <div className="flex flex-wrap items-end gap-3">
            <label className="w-72 max-w-full text-sm font-medium text-text">
              {t("intake.group")}
              <AppSelect className={`mt-1 ${intakeInputClass}`} ariaLabel={t("intake.group")} value={groupId}
                placeholder={t("intake.choose")} options={options.map((group) => ({ value: group.id, label: group.name }))}
                onChange={setGroupId} />
            </label>
            {groups.isPending && <p role="status">{t("list.loading")}</p>}
            {groups.isError && <p role="alert">{t("intake.loadFailed")}</p>}
            {visibleGroups?.length === 0 && <p role="status">{t("list.empty")}</p>}
            {search && !!groups.data?.length && matches.length === 0 && (
              <p role="status" className="text-sm text-muted">{t("announcements.noGroupMatches")}</p>
            )}
          </div>
        ) : <a href="#/login" className="app-action">{t("auth.signIn")}</a>}
      </Card>
      {authorization && selected && <div className="flex flex-wrap gap-2" role="tablist" aria-label={t("announcements.title")}>
        {(["create", "history"] as const).map((key) => (
          <button key={key} type="button" role="tab" aria-selected={panel === key}
            className={`min-h-10 rounded-lg border px-4 text-sm font-semibold transition ${panel === key ? "border-primary bg-primary text-on-primary" : "border-border bg-surface text-text hover:border-primary"}`}
            onClick={() => setPanel(key)}>{t(key === "create" ? "announcements.create" : "announcements.history")}</button>
        ))}
      </div>}
      {authorization && selected && panel === "history" && <div>
        <button type="button" className={intakeButtonClass} aria-expanded={showGroupSearch}
          onClick={() => setShowGroupSearch((show) => !show)}>{t("announcements.groupSearch")}</button>
        {showGroupSearch && <label className="mt-3 block max-w-sm text-sm font-medium text-text">
          {t("audit.groupSearch")}
          <input type="search" className={`mt-1 ${intakeInputClass}`} value={groupSearch}
            onChange={(event) => setGroupSearch(event.target.value)} />
          {search && matches.length === 0 && <span className="mt-1 block text-xs text-muted">{t("announcements.noGroupMatches")}</span>}
        </label>}
      </div>}
      {authorization && selected && (
        <GroupAnnouncements key={groupId} groupName={selected.name} filter={historyFilter} panel={panel}
          onFilterChange={setHistoryFilter}
          {...{ groupId, authorization, onUnauthorized }} />
      )}
    </div>
  );
}

function GroupAnnouncements({ groupId, groupName, authorization, onUnauthorized, filter, panel, onFilterChange }: {
  groupId: string; groupName: string; authorization: string; onUnauthorized: () => void;
  filter: "upcoming" | "published" | "failed";
  panel: "create" | "history";
  onFilterChange: (filter: "upcoming" | "published" | "failed") => void;
}) {
  const { t, lang } = useSettings();
  const [items, setItems] = useState<Announcement[]>();
  const [loadFailed, setLoadFailed] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const [clearing, setClearing] = useState(false);
  const [clearOutcome, setClearOutcome] = useState<"cleared" | "clearFailed">();
  const [reload, setReload] = useState(0);
  const [channel, setChannel] = useState("");
  const [type, setType] = useState<Schedule["type"]>("general");
  const [message, setMessage] = useState("");
  const [localTime, setLocalTime] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<TranslationKey>();
  const [serverError, setServerError] = useState("");
  const [channels, setChannels] = useState<string[]>([]);
  const requests = useRef(new Map<string, string>());
  const submission = useRef<AbortController | null>(null);

  useEffect(() => () => submission.current?.abort(), []);

  // Prefill the send time to "now + 15 minutes" so the field never starts
  // empty and the exact date AND time are one click away.
  useEffect(() => {
    if (localTime) return;
    const at = new Date(Date.now() + 15 * 60_000);
    const pad = (value: number) => String(value).padStart(2, "0");
    setLocalTime(`${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())}T${pad(at.getHours())}:${pad(at.getMinutes())}`);
  }, [localTime]);

  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      try {
        const { data, response } = await api.GET("/api/groups/{groupId}/announcements", {
          headers: { Authorization: authorization }, params: { path: { groupId } }, signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        if (response.status === 401) { onUnauthorized(); return; }
        if (data) setItems(data);
        setLoadFailed(!data);
      } catch {
        if (!controller.signal.aborted) setLoadFailed(true);
      }
    }
    void load();
    return () => controller.abort();
  }, [authorization, groupId, onUnauthorized, reload]);

  // Load the group's ACTIVE Slack channels so the coordinator picks one from
  // a list instead of typing an exact, easy-to-mistype name (dots→dashes!).
  useEffect(() => {
    const controller = new AbortController();
    async function loadChannels() {
      setChannels([]);
      try {
        const { data, response } = await api.GET("/api/groups/{groupId}/resources", {
          headers: { Authorization: authorization }, params: { path: { groupId } }, signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        if (response.status === 401) { onUnauthorized(); return; }
        const names = (data ?? [])
          .filter((resource) => resource.provider === "slack" && resource.kind === "channel")
          .map((resource) => resource.name)
          .sort();
        setChannels(names);
        setChannel((current) => (current && names.includes(current) ? current : names[0] ?? ""));
      } catch {
        /* The channel list is a convenience; submission still validates server-side. */
      }
    }
    void loadChannels();
    return () => controller.abort();
  }, [authorization, groupId, onUnauthorized]);

  async function schedule(event: FormEvent) {
    event.preventDefault();
    if (submission.current) return;
    setSaved(false);
    setError(undefined);
    setServerError("");
    const channelName = channel.trim().replace(/^#/, "");
    if ((type !== "general" && !channelName) || !message.trim() || !Number.isFinite(new Date(localTime).getTime())) {
      setError("announcements.invalid");
      return;
    }
    // Եթե ընտրած ժամանակն արդեն անցել է (օրինակ՝ էջը երկար բաց է մնացել
    // և ավտոմատ լցված «հիմա+15ր»-ը հնացել է)՝ ավտոմատ թարմացնում ենք և
    // տեղեկացնում, որ ուղարկվելու է նոր ժամանակով։
    let date = new Date(localTime);
    if (date.getTime() <= Date.now()) {
      const bumped = new Date(Date.now() + 15 * 60_000);
      const pad = (value: number) => String(value).padStart(2, "0");
      setLocalTime(`${bumped.getFullYear()}-${pad(bumped.getMonth() + 1)}-${pad(bumped.getDate())}T${pad(bumped.getHours())}:${pad(bumped.getMinutes())}`);
      date = bumped;
      setError("announcements.timeInPast");
      return;
    }

    const draft = { channelName: type === "general" ? "all" : channelName, type, message: message.trim(), scheduledAt: date.toISOString() };
    const key = JSON.stringify(draft);
    const requestId = requests.current.get(key) ?? crypto.randomUUID();
    requests.current.set(key, requestId);
    const controller = new AbortController();
    submission.current = controller;
    setBusy(true);
    try {
      const { data, error, response } = await api.POST("/api/groups/{groupId}/announcements", {
        headers: { Authorization: authorization }, params: { path: { groupId } },
        body: { ...draft, requestId }, signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      if (response.status === 401) { onUnauthorized(); return; }
      if (!data) {
        setError(response.status === 409 ? "announcements.conflict" : "announcements.saveFailed");
        // Surface the server's own reason (e.g. "the channel is not active
        // for this group") so the coordinator knows exactly what to fix.
        const detail = (error as { error?: { message?: string } } | undefined)?.error?.message;
        if (detail) setServerError(detail);
        return;
      }
      requests.current.delete(key);
      setMessage("");
      setSaved(true);
      setReload((value) => value + 1);
    } catch {
      if (!controller.signal.aborted) setError("announcements.saveFailed");
    } finally {
      submission.current = null;
      if (!controller.signal.aborted) setBusy(false);
    }
  }

  async function clearHistory() {
    if (clearing) return;
    setClearing(true);
    setClearOutcome(undefined);
    try {
      const { response } = await api.DELETE("/api/groups/{groupId}/announcements/history", {
        headers: { Authorization: authorization },
        params: { path: { groupId } },
      });
      if (response.status === 401) { onUnauthorized(); return; }
      if (!response.ok) {
        setClearOutcome("clearFailed");
        return;
      }
      setConfirmClear(false);
      setClearOutcome("cleared");
      setReload((value) => value + 1);
    } catch {
      setClearOutcome("clearFailed");
    } finally {
      setClearing(false);
    }
  }

  const dateTime = new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" });
  const visibleItems = items?.filter((item) => filter === "upcoming"
    ? item.status === "scheduled" || item.status === "publishing"
    : filter === "published" ? item.status === "published"
      : item.status === "failed" || item.status === "unknown");

  return (
    <>
      {panel === "create" && <Card className="p-5">
        <h2 className="mb-4 font-semibold text-text">{t("announcements.create")}</h2>
        <form onSubmit={schedule}>
          <fieldset disabled={busy || !items || loadFailed} className="space-y-3 text-sm text-text">
            {type !== "general" ? <label className="block">{t("announcements.channel")}
              <AppSelect className={`mt-1 ${intakeInputClass}`} ariaLabel={t("announcements.channel")} value={channel}
                placeholder={t("announcements.noChannels")}
                options={channels.length === 0 ? [{ value: "", label: t("list.loading"), disabled: true }] : channels.map((name) => ({ value: name, label: `#${name}` }))}
                onChange={setChannel} />
              {channels.length === 0 && !loadFailed && items && (
                <span className="mt-1 block text-xs text-muted">{t("announcements.noChannels")}</span>
              )}
            </label> : <p className="rounded-md bg-surface-muted px-3 py-2 text-sm">{t("announcements.allChannels")}</p>}
            <label className="block">{t("announcements.type")}
              <AppSelect className={`mt-1 ${intakeInputClass}`} ariaLabel={t("announcements.type")} value={type}
                options={types.map((value) => ({ value, label: t(`announcements.type.${value}`) }))}
                onChange={(value) => setType(value as Schedule["type"])} />
            </label>
            <label className="block">{t("announcements.message")}
              <textarea className={`mt-1 ${intakeInputClass}`} rows={5} required maxLength={4000} value={message}
                onChange={(event) => setMessage(event.target.value)} />
            </label>
            <TranslateDraft value={message} onApply={setMessage} />
            <label className="block">{t("announcements.localTime")}
              <DateTimePicker value={localTime} onChange={setLocalTime} required />
            </label>
            <button type="submit" className={intakeActionClass}>{t("announcements.submit")}</button>
          </fieldset>
          {error && (
            <p role="alert" className="mt-3 text-sm text-danger-text">
              {t(error)}
              {serverError && <span className="mt-0.5 block text-xs opacity-80">{serverError}</span>}
            </p>
          )}
          {saved && <p role="status" className="mt-3 text-sm text-text">{t("announcements.saved")}</p>}
        </form>
      </Card>}
      {panel === "history" && <Card className="p-5">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
          <h2 className="font-semibold text-text">{t("announcements.history")}</h2>
          <div className="flex flex-wrap gap-2">
            <button type="button" className={intakeButtonClass} onClick={() => { setLoadFailed(false); setReload((value) => value + 1); }}>{t("audit.refresh")}</button>
            <button type="button" className={`${intakeButtonClass} text-danger-text`}
              disabled={clearing || confirmClear || loadFailed || !items?.some(isHistoryEntry)}
              onClick={() => { setClearOutcome(undefined); setConfirmClear(true); }}>{t("announcements.clearHistory")}</button>
          </div>
        </div>
        <div className="mb-3 flex flex-wrap gap-2 border-b border-border" role="tablist" aria-label={t("announcements.filter")}>
          {(["upcoming", "published", "failed"] as const).map((key) => (
            <button key={key} type="button" role="tab" aria-selected={filter === key}
              className={`min-h-10 border-b-2 px-3 text-sm font-semibold transition ${filter === key ? "border-primary text-primary" : "border-transparent text-muted hover:text-text"}`}
              onClick={() => onFilterChange(key)}>{t(`announcements.filter.${key}`)}</button>
          ))}
        </div>
        {confirmClear && (
          <div className="mb-3 space-y-3 rounded-md border border-border p-3">
            <p className="text-sm text-text">{t("announcements.confirmClearHistory", { group: groupName })}</p>
            <div className="flex flex-wrap gap-2">
              <button type="button" className={`${intakeButtonClass} bg-danger-bg text-danger-text`} disabled={clearing}
                onClick={() => void clearHistory()}>
                {t(clearing ? "announcements.clearingHistory" : "announcements.confirmClear")}
              </button>
              <button type="button" className={intakeButtonClass} disabled={clearing}
                onClick={() => { setConfirmClear(false); setClearOutcome(undefined); }}>{t("intake.cancel")}</button>
            </div>
          </div>
        )}
        {clearOutcome && (
          <p role={clearOutcome === "clearFailed" ? "alert" : "status"}
            className={`mb-3 text-sm ${clearOutcome === "clearFailed" ? "text-danger-text" : "text-success-text"}`}>
            {t(`announcements.${clearOutcome}`)}
          </p>
        )}
        {loadFailed && <p role="alert" className="text-sm text-danger-text">{t("announcements.loadFailed")}</p>}
        {!loadFailed && !items && <p role="status" className="text-sm text-muted">{t("announcements.loading")}</p>}
        {!loadFailed && items && visibleItems?.length === 0 && <p className="text-sm text-muted">{t(`announcements.filter.empty.${filter}`)}</p>}
        <ul className="divide-y divide-border">
          {visibleItems?.map((item) => (
            <li key={item.id} className="space-y-2 py-3 text-sm text-text">
              <div className="flex flex-wrap items-center gap-2">
                <strong>{item.schedule.channelName === "all" ? t("announcements.allChannelsShort") : `#${item.schedule.channelName}`}</strong>
                <Chip>{t(`announcements.status.${item.status}`)}</Chip>
              </div>
              <p className="text-muted">{t(`announcements.type.${item.schedule.type}`)} · {dateTime.format(new Date(item.schedule.scheduledAt))}</p>
              <p className="whitespace-pre-wrap break-words">{item.schedule.message}</p>
              {item.publishedAt && <p className="text-muted">{t("announcements.status.published")} · {dateTime.format(new Date(item.publishedAt))}</p>}
              {item.error && <p className="break-words text-danger-text">{item.error}</p>}
            </li>
          ))}
        </ul>
      </Card>}
    </>
  );
}
