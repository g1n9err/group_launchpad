import { useEffect, useRef, useState } from "react";
import { LANGUAGES, LANGUAGE_LABELS, useSettings } from "../i18n";
import { apiUrl, authFetch } from "../lib/api";
import { intakeButtonClass, intakeInputClass } from "../lib/intakeForm";

export function TranslateDraft({ value, onApply, maxLength = 4000 }: {
  value: string;
  onApply: (text: string) => void;
  maxLength?: number;
}) {
  const { t } = useSettings();
  const [target, setTarget] = useState("");
  const [result, setResult] = useState<{ source: string; text: string }>();
  const [pending, setPending] = useState(false);
  const [failed, setFailed] = useState(false);
  const request = useRef<AbortController | null>(null);
  useEffect(() => () => request.current?.abort(), []);

  async function translate() {
    if (!target || !value.trim() || request.current) return;
    const controller = new AbortController();
    request.current = controller;
    setPending(true);
    setFailed(false);
    setResult(undefined);
    try {
      const response = await authFetch(apiUrl("/api/translation"), {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ text: value, target }),
        signal: controller.signal,
      });
      if (!response.ok) throw new Error("translation failed");
      const data: unknown = await response.json();
      if (!data || typeof data !== "object" || !("text" in data) ||
          typeof data.text !== "string" || !data.text.trim() || data.text.length > maxLength) {
        throw new Error("invalid translation");
      }
      if (!controller.signal.aborted) setResult({ source: value, text: data.text });
    } catch {
      if (!controller.signal.aborted) setFailed(true);
    } finally {
      request.current = null;
      if (!controller.signal.aborted) setPending(false);
    }
  }

  return (
    <div className="mt-2 space-y-2 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <select className={`${intakeInputClass} max-w-56`} aria-label={t("translation.language")} value={target}
          disabled={pending} onChange={(event) => { setTarget(event.target.value); setResult(undefined); setFailed(false); }}>
          <option value="">{t("translation.language")}</option>
          {LANGUAGES.map((language) => <option key={language} value={language}>{LANGUAGE_LABELS[language]}</option>)}
        </select>
        <button type="button" className={intakeButtonClass} disabled={pending || !target || !value.trim() || value.length > 4000}
          onClick={() => void translate()}>{t(pending ? "translation.working" : "translation.translate")}</button>
      </div>
      {failed && <p role="alert" className="text-danger-text">{t("translation.failed")}</p>}
      {result?.source === value && (
        <div className="space-y-2">
          <p className="whitespace-pre-wrap break-words text-text">{result.text}</p>
          <button type="button" className={intakeButtonClass}
            onClick={() => { onApply(result.text); setResult(undefined); }}>{t("translation.apply")}</button>
        </div>
      )}
    </div>
  );
}
