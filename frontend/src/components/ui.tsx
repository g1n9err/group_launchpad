// Shared UI primitives: tone classes, badges, cards, stat tiles, placeholders.

import { useSettings } from "../i18n";
import type { ReactNode } from "react";

// Tone → token classes. Literal strings so the Tailwind scanner sees them;
// colors live only in the design tokens (index.css).
const toneClasses = {
  muted: "bg-muted-bg text-muted-text ring-muted-ring",
  info: "bg-info-bg text-info-text ring-info-ring",
  success: "bg-success-bg text-success-text ring-success-ring",
  warn: "bg-warn-bg text-warn-text ring-warn-ring",
  danger: "bg-danger-bg text-danger-text ring-danger-ring",
} as const;

export type Tone = keyof typeof toneClasses;

const statusTone = {
  draft: "muted",
  launching: "info",
  launched: "success",
  partially_failed: "warn",
  failed: "danger",
  archived: "muted",
  connected: "success",
  disconnected: "muted",
  error: "danger",
} as const satisfies Record<string, Tone>;

type TFunc = ReturnType<typeof useSettings>["t"];

export function StatusBadge({ status, t }: { status: string; t: TFunc }) {
  const knownStatus = Object.hasOwn(statusTone, status) ? status as keyof typeof statusTone : undefined;
  const tone = knownStatus ? statusTone[knownStatus] : "muted";
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ring-1 ring-inset ${toneClasses[tone]}`}>
      {knownStatus ? t(`status.${knownStatus}`) : status}
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
    <section className={`app-card rounded-xl border border-border bg-surface shadow-card ${className}`}>
      {children}
    </section>
  );
}

export function PageTitle({ title, sub, actions }: { title: string; sub?: string; actions?: ReactNode }) {
  return (
    <div className="app-page-title mb-4 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-2xl font-bold tracking-[-0.035em] text-text md:text-3xl">{title}</h1>
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
  icon,
  visual,
}: {
  label: string;
  value: ReactNode;
  sub?: string;
  icon?: ReactNode;
  visual?: ReactNode;
}) {
  return (
    <Card className="metric-card p-5">
      <div className="flex items-start justify-between gap-2">
        <div className="metric-label text-sm font-medium text-muted">{label}</div>
        {icon && <span className="hidden rounded-lg bg-soft p-2 text-info-text sm:inline-flex">{icon}</span>}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1">
        <div className="metric-value text-3xl font-bold tracking-tight text-text tabular-nums">{value}</div>
        {visual}
      </div>
      {sub && <div className="mt-1 text-xs text-muted">{sub}</div>}
    </Card>
  );
}
