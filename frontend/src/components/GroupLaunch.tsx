import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useSettings } from "../i18n";
import { api } from "../lib/api";
import { formatLaunchDetail } from "../lib/launchDetail";
import { intakeActionClass, intakeButtonClass } from "../lib/intakeForm";
import type { components } from "../lib/schema";
import { Card, Chip } from "./ui";
import { fireLock, getAuthHeader, useAuthUser } from "../lib/auth";

type LaunchResult = components["schemas"]["GroupLaunchResult"];
type LaunchStep = components["schemas"]["LaunchStep"];
type Classroom = components["schemas"]["ClassroomCourse"];
type Drive = components["schemas"]["DriveFolders"];
type Service = "slack" | "classroom" | "drive" | "forms";
type Failure = "requestFailed" | "unavailable" | "originDenied";

const ALL_PLATFORMS: Service[] = ["slack", "classroom", "drive", "forms"];

function inProgress(result: LaunchResult | null | undefined) {
  return result?.status === "running";
}

export function GroupLaunch({ groupId }: { groupId: string }) {
  const { t } = useSettings();
  const user = useAuthUser();
  const authorization = user ? getAuthHeader().Authorization ?? "" : "";
  const onUnauthorized = useCallback(() => fireLock(), []);

  return (
    <Card className="space-y-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-base font-semibold text-text">{t("launch.title")}</h2>
      </div>
      {authorization ? (
        <LaunchChecklist groupId={groupId} authorization={authorization} onUnauthorized={onUnauthorized} />
      ) : (
        <a href="#/login" className="app-action">{t("auth.signIn")}</a>
      )}
    </Card>
  );
}

function LaunchChecklist({ groupId, authorization, onUnauthorized }: {
  groupId: string; authorization: string; onUnauthorized: () => void;
}) {
  const { t, lang } = useSettings();
  const queryClient = useQueryClient();
  const [result, setResult] = useState<LaunchResult | null>();
  const [classroom, setClassroom] = useState<Classroom>();
  const [drive, setDrive] = useState<Drive>();
  const [statusFailed, setStatusFailed] = useState(false);
  const [reload, setReload] = useState(0);
  const [busy, setBusy] = useState<Service>();
  const [confirm, setConfirm] = useState<Service>();
  const [failures, setFailures] = useState<Partial<Record<Service, Failure>>>({});
  const [queued, setQueued] = useState(false); // true after POST /launch until terminal status
  const request = useRef<AbortController | null>(null);

  useEffect(() => () => request.current?.abort(), []);

  // Poll unified launch status. Keeps polling while: (a) the result is
  // "running", (b) we've recently queued a launch and the worker hasn't
  // persisted the result yet (404 or fallback), or (c) we haven't reached
  // a terminal state. Gives up after 60 seconds of no transition.
  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let wasRunning = false;
    const deadline = Date.now() + 60_000;

    async function loadStatus() {
      try {
        const { data, response } = await api.GET("/api/groups/{groupId}/status", {
          headers: { Authorization: authorization }, params: { path: { groupId } }, signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        if (response.status === 401) { onUnauthorized(); return; }

        // 404: no unified result yet. If we just queued a launch, the
        // worker is processing — keep polling until the deadline.
        if (response.status === 404) {
          setResult(null);
          setStatusFailed(false);
          if (queued && Date.now() < deadline) {
            timer = setTimeout(() => void loadStatus(), 2000);
          } else {
            setQueued(false);
          }
          return;
        }

        setStatusFailed(!data);
        if (!data) return;
        setResult(data);

        if (inProgress(data)) {
          wasRunning = true;
          timer = setTimeout(() => void loadStatus(), 2000);
        } else if (queued && data.status === "running") {
          // Worker saved "running" — keep polling for the terminal status.
          wasRunning = true;
          timer = setTimeout(() => void loadStatus(), 2000);
        } else {
          // Terminal state — stop polling, clear the queued flag.
          setQueued(false);
          if (wasRunning || reload > 0) {
            void queryClient.invalidateQueries({ queryKey: ["groups"] });
            void queryClient.invalidateQueries({ queryKey: ["onboarding", groupId] });
          }
        }
      } catch {
        if (!controller.signal.aborted) setStatusFailed(true);
      }
    }

    void loadStatus();
    return () => { clearTimeout(timer); controller.abort(); };
  }, [authorization, groupId, onUnauthorized, queryClient, reload, queued]);

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
        // Unified launch: queues all platforms.
        const res = await api.POST("/api/groups/{groupId}/launch", options);
        response = res.response;
        if (!controller.signal.aborted && res.response.ok) {
          setQueued(true); // triggers status polling until terminal state
          setReload((value) => value + 1);
        }
      } else if (service === "classroom") {
        const res = await api.POST("/api/groups/{groupId}/classroom/provision", options);
        response = res.response;
        if (!controller.signal.aborted && res.data) setClassroom(res.data);
      } else if (service === "drive") {
        const res = await api.POST("/api/groups/{groupId}/drive/provision", options);
        response = res.response;
        if (!controller.signal.aborted && res.data) setDrive(res.data);
      } else {
        const res = await api.POST("/api/groups/{groupId}/forms/provision", options);
        response = res.response;
      }
      if (controller.signal.aborted) return;
      if (response.status === 401) { onUnauthorized(); return; }
      if (!response.ok) {
        const failure = response.status === 403 ? "originDenied" : response.status === 503 ? "unavailable" : "requestFailed";
        setFailures((previous) => ({ ...previous, [service]: failure }));
      } else if (service === "classroom" || service === "drive") {
        setReload((value) => value + 1);
      }
    } catch {
      if (!controller.signal.aborted) setFailures((previous) => ({ ...previous, [service]: "requestFailed" }));
    } finally {
      request.current = null;
      if (!controller.signal.aborted) setBusy(undefined);
    }
  }

  if (result === undefined && !statusFailed) return <p role="status" className="text-sm text-muted">{t("launch.loading")}</p>;

  const stepFor = (platform: string): LaunchStep | undefined =>
    result?.steps?.find((s) => s.platform === platform);

  const overallTone = result?.status === "completed" ? "success"
    : result?.status === "failed" ? "danger"
    : result?.status === "partially_failed" ? "warn" : "info";

  return (
    <div className="space-y-4">
      {statusFailed && (
        <div className="flex flex-wrap items-center gap-3">
          <p role="alert" className="text-sm text-danger-text">{t("launch.statusFailed")}</p>
          <button type="button" className={intakeButtonClass}
            onClick={() => setReload((value) => value + 1)}>{t("audit.refresh")}</button>
        </div>
      )}

      {result && (
        <div className="flex flex-wrap items-center gap-3">
          <Chip tone={overallTone}>{t(`launch.status.${result.status}`)}</Chip>
          {result.steps && result.steps.length > 0 && (
            <span className="text-xs text-muted">
              {result.steps.filter((s) => s.status === "succeeded").length}/{result.steps.length} {t("launch.platformsReady")}
            </span>
          )}
        </div>
      )}

      <ul className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {ALL_PLATFORMS.map((service) => {
          const step = stepFor(service);
          const failure = failures[service];
          const stepTone = step?.status === "succeeded" ? "success"
            : step?.status === "failed" ? "danger"
            : step?.status === "partially_failed" ? "warn"
            : step?.status === "skipped" ? "muted" : "info";
          return (
            <li key={service} className="space-y-2 rounded-md border border-border p-4 text-sm text-text">
              <h3 className="font-semibold">{t(`launch.${service}`)}</h3>
              {step && (
                <div role="status" className="space-y-1">
                  <Chip tone={stepTone}>{t(`launch.step.${step.status}`)}</Chip>
                  {step.detail && <p className="break-words text-xs text-muted">{formatLaunchDetail(service, step.detail, lang, t)}</p>}
                  {service === "slack" && step.status === "partially_failed" && /\b[1-9]\d* invite,/.test(step.detail ?? "") && (
                    <p className="text-xs text-muted">{t("roster.slackCounts")}</p>
                  )}
                </div>
              )}
              {service === "classroom" && classroom && (
                <p className="break-words">{t("launch.courseId")} <code>{classroom.courseId}</code></p>
              )}
              {service === "drive" && drive && (
                <div className="space-y-1">
                  <a className="block break-words text-primary underline" target="_blank" rel="noreferrer"
                    href={`https://drive.google.com/drive/folders/${encodeURIComponent(drive.root.id)}`}>{drive.root.name}</a>
                  <ul className="list-inside list-disc text-muted">
                    {drive.subfolders.map((folder) => <li key={folder.id}>{folder.name}</li>)}
                  </ul>
                </div>
              )}
              {failure && <p role="alert" className="text-danger-text">{t(`launch.${failure}`)}</p>}

              {/* Show action/retry button for platforms that need it */}
              {(service === "slack" && (!step || step.status === "failed" || step.status === "partially_failed")) ||
               (service !== "slack" && !step?.status?.match(/succeeded|skipped/)) ? (
                <button type="button" className={intakeButtonClass} disabled={Boolean(busy || confirm)}
                  onClick={() => setConfirm(service)}>
                  {busy === service ? t("launch.working")
                    : (failure || step?.status === "failed" || step?.status === "partially_failed") ? t("onboarding.retry")
                    : t(`launch.action.${service}`)}
                </button>
              ) : null}
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
