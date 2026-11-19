// Shared UI primitives: tone classes, badges, cards, stat tiles, placeholders.

import { useSettings } from "../i18n";
import type { ReactNode } from "react";

// Tone → token classes. Literal strings so the Tailwind scanner sees them;
// colors live only in the design tokens (index.css).
export const toneClasses = {
  muted: "bg-muted-bg text-muted-text ring-muted-ring",
  info: "bg-info-bg text-info-text ring-info-ring",
  success: "bg-success-bg text-success-text ring-success-ring",
  warn: "bg-warn-bg text-warn-text ring-warn-ring",
  danger: "bg-danger-bg text-danger-text ring-danger-ring",
} as const;

export type Tone = keyof typeof toneClasses;

const groupStatusTone: Record<string, Tone> = {
  draft: "muted",
  launching: "info",
  launched: "success",
  partially_failed: "warn",
  failed: "danger",
  archived: "muted",
};

type TFunc = ReturnType<typeof useSettings>["t"];

export function StatusBadge({ status, t }: { status: string; t: TFunc }) {
  const tone = groupStatusTone[status] ?? "muted";
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset ${toneClasses[tone]}`}>
      {t(`status.${status}` as never as Parameters<TFunc>[0])}
    </span>
  );
}

export function OnboardingBadge({ status, t }: { status: string; t: TFunc }) {
  const joined = status === "joined_slack" || status === "complete";
  const tone: Tone = joined ? "success" : "warn";
  return (
    <span className={`inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${toneClasses[tone]}`}>
      {t(`onboard.${status}` as never as Parameters<TFunc>[0])}
    </span>
  );
}

export function Chip({ tone = "info", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset ${toneClasses[tone]}`}>
      {children}
    </span>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <section className={`rounded-xl border border-border bg-surface shadow-sm ${className}`}>
      {children}
    </section>
  );
}

export function PageTitle({ title, sub, actions }: { title: string; sub?: string; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-xl font-bold text-text">{title}</h1>
        {sub && <p className="mt-1 text-sm text-muted">{sub}</p>}
      </div>
      {actions && <div className="flex gap-2">{actions}</div>}
    </div>
  );
}

export function StatCard({
  label,
  value,
  sub,
  children,
}: {
  label: string;
  value: ReactNode;
  sub?: string;
  children?: ReactNode;
}) {
  return (
    <Card className="p-5">
      <div className="text-xs font-medium uppercase tracking-wide text-muted">{label}</div>
      <div className="mt-2 text-2xl font-bold text-text">{value}</div>
      {sub && <div className="mt-1 text-xs text-muted">{sub}</div>}
      {children}
    </Card>
  );
}

/** Honest placeholder for backend functionality that does not exist yet. */
export function ComingSoon({ title, description }: { title: string; description?: string }) {
  const { t } = useSettings();
  return (
    <Card className="border-dashed p-6">
      <div className="flex items-center gap-2">
        <Chip tone="info">{t("common.comingSoon")}</Chip>
        <span className="text-sm font-semibold text-text">{title}</span>
      </div>
      <p className="mt-2 text-sm text-muted">{description ?? t("common.comingSoonDesc")}</p>
    </Card>
  );
}
