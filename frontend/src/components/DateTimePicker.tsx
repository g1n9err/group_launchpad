import { useEffect, useMemo, useRef, useState } from "react";
import { AppSelect } from "./AppSelect";
import { useSettings } from "../i18n";
import { intakeInputClass } from "../lib/intakeForm";

type Props = {
  value: string;
  onChange: (value: string) => void;
  required?: boolean;
};

const locales = { hy: "hy-AM", en: "en-US", ru: "ru-RU" } as const;
const pad = (value: number) => String(value).padStart(2, "0");

function parseLocal(value: string): Date | undefined {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value);
  if (!match) return undefined;
  const [, year, month, day, hour, minute] = match;
  const date = new Date(Number(year), Number(month) - 1, Number(day), Number(hour), Number(minute));
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function localValue(date: Date) {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function dateKey(date: Date) {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

export function DateTimePicker({ value, onChange, required }: Props) {
  const { lang, t } = useSettings();
  const locale = locales[lang];
  const selected = parseLocal(value);
  const [open, setOpen] = useState(false);
  const [visibleMonth, setVisibleMonth] = useState(() => {
    const initial = selected ?? new Date();
    return new Date(initial.getFullYear(), initial.getMonth(), 1);
  });
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") setOpen(false);
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const calendar = useMemo(() => {
    const first = new Date(visibleMonth.getFullYear(), visibleMonth.getMonth(), 1);
    // Armenian and Russian calendars start on Monday; English uses Sunday.
    const weekStart = lang === "en" ? 0 : 1;
    const offset = (first.getDay() - weekStart + 7) % 7;
    const start = new Date(first.getFullYear(), first.getMonth(), 1 - offset);
    return Array.from({ length: 42 }, (_, index) => new Date(start.getFullYear(), start.getMonth(), start.getDate() + index));
  }, [visibleMonth, lang]);

  const weekdays = useMemo(() => {
    const weekStart = lang === "en" ? 0 : 1;
    return Array.from({ length: 7 }, (_, index) => {
      const day = new Date(2024, 0, 7 + ((weekStart + index) % 7));
      return new Intl.DateTimeFormat(locale, { weekday: "short" }).format(day);
    });
  }, [lang, locale]);

  const displayed = selected
    ? new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(selected)
    : "";
  const monthTitle = new Intl.DateTimeFormat(locale, { month: "long", year: "numeric" }).format(visibleMonth);
  const hour = selected?.getHours() ?? 12;
  const minute = selected?.getMinutes() ?? 0;

  function chooseDate(date: Date) {
    const next = selected ? new Date(selected) : new Date();
    next.setFullYear(date.getFullYear(), date.getMonth(), date.getDate());
    onChange(localValue(next));
  }

  function setTime(nextHour: number, nextMinute: number) {
    const next = selected ? new Date(selected) : new Date();
    next.setHours(nextHour, nextMinute, 0, 0);
    onChange(localValue(next));
  }

  return (
    <div className="datetime-picker" ref={root}>
      <button type="button" className={`${intakeInputClass} datetime-picker-trigger`} aria-haspopup="dialog"
        aria-expanded={open} aria-required={required || undefined} onClick={() => {
          if (!open) {
            const initial = selected ?? new Date();
            setVisibleMonth(new Date(initial.getFullYear(), initial.getMonth(), 1));
          }
          setOpen((current) => !current);
        }}>
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" className="datetime-picker-icon">
          <rect x="3.5" y="5" width="17" height="16" rx="2.5" />
          <path d="M7.5 3v4M16.5 3v4M4 9.5h16" />
          <path d="M8 13h.01M12 13h.01M16 13h.01M8 17h.01M12 17h.01" />
        </svg>
        <span className={displayed ? "" : "datetime-picker-placeholder"}>{displayed || t("datetime.choose")}</span>
        <span className="datetime-picker-caret" aria-hidden="true">⌄</span>
      </button>

      {open && (
        <div className="datetime-popover" role="dialog" aria-label={t("datetime.dialog")}>
          <div className="datetime-popover-header">
            <div>
              <p className="datetime-overline">{t("datetime.date")}</p>
              <p className="datetime-month">{monthTitle}</p>
            </div>
            <div className="datetime-month-nav">
              <button type="button" aria-label={t("datetime.previousMonth")}
                onClick={() => setVisibleMonth((date) => new Date(date.getFullYear(), date.getMonth() - 1, 1))}>‹</button>
              <button type="button" aria-label={t("datetime.nextMonth")}
                onClick={() => setVisibleMonth((date) => new Date(date.getFullYear(), date.getMonth() + 1, 1))}>›</button>
            </div>
          </div>

          <div className="datetime-calendar" aria-label={monthTitle}>
            {weekdays.map((day, index) => <span className="datetime-weekday" key={`${day}-${index}`}>{day}</span>)}
            {calendar.map((date) => {
              const outside = date.getMonth() !== visibleMonth.getMonth();
              const active = selected && dateKey(date) === dateKey(selected);
              const today = dateKey(date) === dateKey(new Date());
              return (
                <button type="button" key={dateKey(date)}
                  aria-label={new Intl.DateTimeFormat(locale, { dateStyle: "full" }).format(date)}
                  aria-pressed={Boolean(active)}
                  className={`datetime-day${outside ? " is-outside" : ""}${active ? " is-selected" : ""}${today ? " is-today" : ""}`}
                  onClick={() => chooseDate(date)}>
                  {new Intl.NumberFormat(locale, { useGrouping: false }).format(date.getDate())}
                </button>
              );
            })}
          </div>

          <div className="datetime-time-section">
            <div>
              <p className="datetime-overline">{t("datetime.time")}</p>
              {selected && <p className="datetime-time-preview">{new Intl.DateTimeFormat(locale, { timeStyle: "short" }).format(selected)}</p>}
            </div>
            <div className="datetime-time-controls">
              <label>
                <span className="sr-only">{t("datetime.hour")}</span>
                <AppSelect ariaLabel={t("datetime.hour")} className="datetime-time-select" menuClassName="datetime-time-menu"
                  value={String(hour)} options={Array.from({ length: 24 }, (_, value) => ({ value: String(value), label: pad(value) }))}
                  onChange={(value) => setTime(Number(value), minute)} />
              </label>
              <span aria-hidden="true">:</span>
              <label>
                <span className="sr-only">{t("datetime.minute")}</span>
                <AppSelect ariaLabel={t("datetime.minute")} className="datetime-time-select" menuClassName="datetime-time-menu"
                  value={String(minute)} options={Array.from({ length: 60 }, (_, value) => ({ value: String(value), label: pad(value) }))}
                  onChange={(value) => setTime(hour, Number(value))} />
              </label>
            </div>
          </div>

          <div className="datetime-popover-footer">
            <button type="button" className="datetime-today" onClick={() => chooseDate(new Date())}>{t("datetime.today")}</button>
            <button type="button" className="datetime-done" onClick={() => setOpen(false)}>{t("datetime.done")}</button>
          </div>
        </div>
      )}
    </div>
  );
}
