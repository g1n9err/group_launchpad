import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useSettings } from "../i18n";
import { api } from "../lib/api";
import { intakeActionClass, intakeButtonClass, intakeInputClass } from "../lib/intakeForm";
import type { components } from "../lib/schema";
import { Card, Chip } from "./ui";

type SlackLaunch = components["schemas"]["SlackLaunch"];
type Classroom = components["schemas"]["ClassroomCourse"];
type Drive = components["schemas"]["DriveFolders"];
type Service = "slack" | "classroom" | "drive";
type Failure = "requestFailed" | "unavailable" | "originDenied";

function inProgress(run: SlackLaunch | null | undefined) {
  return run?.status === "pending" || run?.status === "running";
}

export function GroupLaunch({ groupId }: { groupId: string }) {
  const { t } = useSettings();
  const [password, setPassword] = useState("");
  const [authorization, setAuthorization] = useState("");
  const [rejected, setRejected] = useState(false);

  const onUnauthorized = useCallback(() => {
    setAuthorization("");
    setRejected(true);
  }, []);

  function unlock(event: FormEvent) {
    event.preventDefault();
    const bytes = new TextEncoder().encode("coordinator:" + password);
    setAuthorization("Basic " + btoa(String.fromCharCode(...bytes)));
    setPassword("");
    setRejected(false);
  }

  return (
    <Card className="space-y-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-base font-semibold text-text">{t("launch.title")}</h2>
        {authorization && <button type="button" className={intakeButtonClass}
          onClick={() => setAuthorization("")}>{t("audit.lock")}</button>}
      </div>
      {authorization ? (
        <LaunchChecklist groupId={groupId} authorization={authorization} onUnauthorized={onUnauthorized} />
      ) : (
        <form className="flex flex-wrap items-end gap-3" onSubmit={unlock}>
          <label className="text-sm font-medium text-text">
            {t("audit.password")}
            <input type="password" autoComplete="current-password" required maxLength={256}
              className={`mt-1 block w-64 max-w-full ${intakeInputClass}`}
              value={password} onChange={(event) => setPassword(event.target.value)} />
          </label>
          <button type="submit" className={intakeActionClass}>{t("launch.open")}</button>
          {rejected && <p role="alert" className="text-sm text-danger-text">{t("audit.invalidPassword")}</p>}
        </form>
      )}
    </Card>
  );
}

function LaunchChecklist({ groupId, authorization, onUnauthorized }: {
  groupId: string; authorization: string; onUnauthorized: () => void;
}) {
  const { t } = useSettings();
  const queryClient = useQueryClient();
  const [slack, setSlack] = useState<SlackLaunch | null>();
  const [classroom, setClassroom] = useState<Classroom>();
  const [drive, setDrive] = useState<Drive>();
  const [statusFailed, setStatusFailed] = useState(false);
  const [reload, setReload] = useState(0);
  const [busy, setBusy] = useState<Service>();
  const [confirm, setConfirm] = useState<Service>();
  const [failures, setFailures] = useState<Partial<Record<Service, Failure>>>({});
  const request = useRef<AbortController | null>(null);

  useEffect(() => () => request.current?.abort(), []);

  useEffect(() => {
    if (busy === "slack") return;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let wasRunning = false;

    async function loadStatus() {
      try {
        const { data, response } = await api.GET("/api/groups/{groupId}/status", {
          headers: { Authorization: authorization }, params: { path: { groupId } }, signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        if (response.status === 401) { onUnauthorized(); return; }
        if (response.status === 404) {
          setSlack(null);
          setStatusFailed(false);
          return;
        }
        setStatusFailed(!data);
        if (!data) return;
        setSlack(data);
        if (inProgress(data)) {
          wasRunning = true;
          timer = setTimeout(() => void loadStatus(), 2000);
        } else if (wasRunning || reload > 0) {
          void queryClient.invalidateQueries({ queryKey: ["groups"] });
          void queryClient.invalidateQueries({ queryKey: ["onboarding", groupId] });
        }
      } catch {
        if (!controller.signal.aborted) setStatusFailed(true);
      }
    }

    void loadStatus();
    return () => { clearTimeout(timer); controller.abort(); };
  }, [authorization, busy, groupId, onUnauthorized, queryClient, reload]);

  async function provision(service: Service) {
    if (request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setConfirm(undefined);
    setBusy(service);
    setFailures((previous) => ({ ...previous, [service]: undefined }));
    const options = {
      headers: { Authorization: authorization }, params: { path: { groupId } }, signal: controller.signal,
    };
    try {
      let response: Response;
      if (service === "slack") {
        const result = await api.POST("/api/groups/{groupId}/launch", options);
        response = result.response;
        if (!controller.signal.aborted && result.data) {
          setSlack(result.data);
          setReload((value) => value + 1);
        }
      } else if (service === "classroom") {
        const result = await api.POST("/api/groups/{groupId}/classroom/provision", options);
        response = result.response;
        if (!controller.signal.aborted && result.data) setClassroom(result.data);
      } else {
        const result = await api.POST("/api/groups/{groupId}/drive/provision", options);
        response = result.response;
        if (!controller.signal.aborted && result.data) setDrive(result.data);
      }
      if (controller.signal.aborted) return;
      if (response.status === 401) { onUnauthorized(); return; }
      if (!response.ok) {
        const failure = response.status === 403 ? "originDenied" : response.status === 503 ? "unavailable" : "requestFailed";
        setFailures((previous) => ({ ...previous, [service]: failure }));
      }
    } catch {
      if (!controller.signal.aborted) setFailures((previous) => ({ ...previous, [service]: "requestFailed" }));
    } finally {
      request.current = null;
      if (!controller.signal.aborted) setBusy(undefined);
    }
  }

  if (slack === undefined && !statusFailed) return <p role="status" className="text-sm text-muted">{t("launch.loading")}</p>;

  const slackFailed = slack?.status === "failed" || slack?.status === "partially_failed";
  const services = ["slack", "classroom", "drive"] as const;

  return (
    <div className="space-y-4">
      {statusFailed && (
        <div className="flex flex-wrap items-center gap-3">
          <p role="alert" className="text-sm text-danger-text">{t("launch.statusFailed")}</p>
          <button type="button" className={intakeButtonClass}
            onClick={() => setReload((value) => value + 1)}>{t("audit.refresh")}</button>
        </div>
      )}
      <ul className="grid gap-3 lg:grid-cols-3">
        {services.map((service) => {
          const failure = failures[service];
          const completed = service === "slack" ? slack?.status === "succeeded" : service === "classroom" ? classroom : drive;
          const slackActionBlocked = service === "slack" && (statusFailed || inProgress(slack));
          return (
            <li key={service} className="space-y-3 rounded-md border border-border p-4 text-sm text-text">
              <h3 className="font-semibold">{t(`launch.${service}`)}</h3>
              {service === "slack" && slack && (
                <div role="status" className="space-y-2">
                  <Chip tone={slackFailed ? "danger" : completed ? "success" : "info"}>{t(`launch.status.${slack.status}`)}</Chip>
                  {slack.error && <p className="break-words text-danger-text">{slack.error}</p>}
                </div>
              )}
              {service === "classroom" && classroom && (
                <p className="break-words">{t("launch.courseId")} <code>{classroom.courseId}</code></p>
              )}
              {service === "drive" && drive && (
                <div className="space-y-2">
                  <a className="block break-words text-primary underline" target="_blank" rel="noreferrer"
                    href={`https://drive.google.com/drive/folders/${encodeURIComponent(drive.root.id)}`}>{drive.root.name}</a>
                  <ul className="list-inside list-disc text-muted">
                    {drive.subfolders.map((folder) => <li key={folder.id}>{folder.name}</li>)}
                  </ul>
                </div>
              )}
              {failure && <p role="alert" className="text-danger-text">{t(`launch.${failure}`)}</p>}
              {!completed && (
                <button type="button" className={intakeButtonClass} disabled={Boolean(busy || confirm || slackActionBlocked)}
                  onClick={() => setConfirm(service)}>
                  {busy === service ? t("launch.working") : failure || (service === "slack" && slackFailed) ? t("onboarding.retry") : t(`launch.action.${service}`)}
                </button>
              )}
            </li>
          );
        })}
      </ul>
      {confirm && (
        <div className="space-y-3 rounded-md border border-border p-4">
          <p className="text-sm text-text">{t(`launch.confirm.${confirm}`)}</p>
          <div className="flex flex-wrap gap-2">
            <button type="button" className={intakeActionClass} onClick={() => void provision(confirm)}>{t("launch.confirm")}</button>
            <button type="button" className={intakeButtonClass} onClick={() => setConfirm(undefined)}>{t("intake.cancel")}</button>
          </div>
        </div>
      )}
    </div>
  );
}
