import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Card, Chip, PageTitle } from "../components/ui";
import { useSettings } from "../i18n";
import type { TranslationKey } from "../i18n/en";
import { api } from "../lib/api";
import { intakeActionClass, intakeButtonClass, intakeInputClass } from "../lib/intakeForm";
import { useIntakeGroups } from "../lib/queries";
import type { components } from "../lib/schema";

type Schedule = components["schemas"]["AnnouncementSchedule"];
type Announcement = components["schemas"]["ScheduledAnnouncement"];

const types = ["welcome", "staff_intro", "rules", "reminder", "general"] as const;

export function AnnouncementsPage() {
  const { t } = useSettings();
  const [password, setPassword] = useState("");
  const [authorization, setAuthorization] = useState("");
  const [groupId, setGroupId] = useState("");
  const [rejected, setRejected] = useState(false);
  const groups = useIntakeGroups(Boolean(authorization));

  const lock = useCallback((invalid: boolean) => {
    setAuthorization("");
    setGroupId("");
    setRejected(invalid);
  }, []);
  const onUnauthorized = useCallback(() => lock(true), [lock]);

  function unlock(event: FormEvent) {
    event.preventDefault();
    const bytes = new TextEncoder().encode("coordinator:" + password);
    setAuthorization("Basic " + btoa(String.fromCharCode(...bytes)));
    setPassword("");
    setRejected(false);
  }

  return (
    <div className="space-y-5">
      <PageTitle title={t("announcements.title")} />
      <Card className="p-5">
        {authorization ? (
          <div className="flex flex-wrap items-end gap-3">
            <label className="w-72 max-w-full text-sm font-medium text-text">
              {t("intake.group")}
              <select className={`mt-1 ${intakeInputClass}`} value={groupId} onChange={(event) => setGroupId(event.target.value)}>
                <option value="">{t("intake.choose")}</option>
                {groups.data?.map((group) => <option key={group.id} value={group.id}>{group.name}</option>)}
              </select>
            </label>
            <button type="button" className={intakeButtonClass} onClick={() => lock(false)}>{t("audit.lock")}</button>
            {groups.isPending && <p role="status">{t("list.loading")}</p>}
            {groups.isError && <p role="alert">{t("intake.loadFailed")}</p>}
            {groups.data?.length === 0 && <p role="status">{t("list.empty")}</p>}
          </div>
        ) : (
          <form className="flex flex-wrap items-end gap-3" onSubmit={unlock}>
            <label className="text-sm font-medium text-text">
              {t("audit.password")}
              <input type="password" autoComplete="current-password" required maxLength={256}
                className={`mt-1 block w-64 max-w-full ${intakeInputClass}`}
                value={password} onChange={(event) => setPassword(event.target.value)} />
            </label>
            <button type="submit" className={intakeActionClass}>{t("announcements.open")}</button>
            {rejected && <p role="alert" className="text-sm text-danger-text">{t("audit.invalidPassword")}</p>}
          </form>
        )}
      </Card>
      {authorization && groupId && (
        <GroupAnnouncements key={groupId} {...{ groupId, authorization, onUnauthorized }} />
      )}
    </div>
  );
}

function GroupAnnouncements({ groupId, authorization, onUnauthorized }: {
  groupId: string; authorization: string; onUnauthorized: () => void;
}) {
  const { t, lang } = useSettings();
  const [items, setItems] = useState<Announcement[]>();
  const [loadFailed, setLoadFailed] = useState(false);
  const [reload, setReload] = useState(0);
  const [channel, setChannel] = useState("");
  const [type, setType] = useState<Schedule["type"]>("general");
  const [message, setMessage] = useState("");
  const [localTime, setLocalTime] = useState("");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<TranslationKey>();
  const requests = useRef(new Map<string, string>());
  const submission = useRef<AbortController | null>(null);

  useEffect(() => () => submission.current?.abort(), []);

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

  async function schedule(event: FormEvent) {
    event.preventDefault();
    if (submission.current) return;
    setSaved(false);
    setError(undefined);
    const date = new Date(localTime);
    const channelName = channel.trim().replace(/^#/, "");
    if (!channelName || !message.trim() || !Number.isFinite(date.getTime())) {
      setError("announcements.invalid");
      return;
    }

    const draft = { channelName, type, message: message.trim(), scheduledAt: date.toISOString() };
    const key = JSON.stringify(draft);
    const requestId = requests.current.get(key) ?? crypto.randomUUID();
    requests.current.set(key, requestId);
    const controller = new AbortController();
    submission.current = controller;
    setBusy(true);
    try {
      const { data, response } = await api.POST("/api/groups/{groupId}/announcements", {
        headers: { Authorization: authorization }, params: { path: { groupId } },
        body: { ...draft, requestId }, signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      if (response.status === 401) { onUnauthorized(); return; }
      if (!data) {
        setError(response.status === 409 ? "announcements.conflict" : "announcements.saveFailed");
        return;
      }
      requests.current.delete(key);
      setMessage("");
      setLocalTime("");
      setSaved(true);
      setReload((value) => value + 1);
    } catch {
      if (!controller.signal.aborted) setError("announcements.saveFailed");
    } finally {
      submission.current = null;
      if (!controller.signal.aborted) setBusy(false);
    }
  }

  const dateTime = new Intl.DateTimeFormat(lang, { dateStyle: "medium", timeStyle: "short" });

  return (
    <div className="grid gap-5 lg:grid-cols-2">
      <Card className="p-5">
        <h2 className="mb-4 font-semibold text-text">{t("announcements.create")}</h2>
        <form onSubmit={schedule}>
          <fieldset disabled={busy || !items || loadFailed} className="space-y-3 text-sm text-text">
            <label className="block">{t("announcements.channel")}
              <input className={`mt-1 ${intakeInputClass}`} required maxLength={81} value={channel}
                onChange={(event) => setChannel(event.target.value)} />
            </label>
            <label className="block">{t("announcements.type")}
              <select className={`mt-1 ${intakeInputClass}`} value={type}
                onChange={(event) => setType(event.target.value as Schedule["type"])}>
                {types.map((value) => <option key={value} value={value}>{t(`announcements.type.${value}`)}</option>)}
              </select>
            </label>
            <label className="block">{t("announcements.message")}
              <textarea className={`mt-1 ${intakeInputClass}`} rows={5} required maxLength={4000} value={message}
                onChange={(event) => setMessage(event.target.value)} />
            </label>
            <label className="block">{t("announcements.localTime")}
              <input type="datetime-local" className={`mt-1 ${intakeInputClass}`} required value={localTime}
                onChange={(event) => setLocalTime(event.target.value)} />
            </label>
            <button type="submit" className={intakeActionClass}>{t("announcements.submit")}</button>
          </fieldset>
          {error && <p role="alert" className="mt-3 text-sm text-danger-text">{t(error)}</p>}
          {saved && <p role="status" className="mt-3 text-sm text-text">{t("announcements.saved")}</p>}
        </form>
      </Card>
      <Card className="p-5">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
          <h2 className="font-semibold text-text">{t("announcements.history")}</h2>
          <button type="button" className={intakeButtonClass} onClick={() => { setLoadFailed(false); setReload((value) => value + 1); }}>{t("audit.refresh")}</button>
        </div>
        {loadFailed && <p role="alert" className="text-sm text-danger-text">{t("announcements.loadFailed")}</p>}
        {!loadFailed && !items && <p role="status" className="text-sm text-muted">{t("announcements.loading")}</p>}
        {!loadFailed && items?.length === 0 && <p className="text-sm text-muted">{t("announcements.empty")}</p>}
        <ul className="divide-y divide-border">
          {items?.map((item) => (
            <li key={item.id} className="space-y-2 py-3 text-sm text-text">
              <div className="flex flex-wrap items-center gap-2">
                <strong>#{item.schedule.channelName}</strong>
                <Chip>{t(`announcements.status.${item.status}`)}</Chip>
              </div>
              <p className="text-muted">{t(`announcements.type.${item.schedule.type}`)} · {dateTime.format(new Date(item.schedule.scheduledAt))}</p>
              <p className="whitespace-pre-wrap break-words">{item.schedule.message}</p>
              {item.publishedAt && <p className="text-muted">{t("announcements.status.published")} · {dateTime.format(new Date(item.publishedAt))}</p>}
              {item.error && <p className="break-words text-danger-text">{item.error}</p>}
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
