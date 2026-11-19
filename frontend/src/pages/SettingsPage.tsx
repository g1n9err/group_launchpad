// Settings: language + theme (both functional, persisted) and preferences
// scaffolding. No fake options.

import { useSettings, LANGUAGE_LABELS, LANGUAGES, type Lang, type Theme } from "../i18n";
import { Card, PageTitle } from "../components/ui";

export function SettingsPage() {
  const { t, lang, setLang, theme, setTheme } = useSettings();

  return (
    <div className="space-y-6">
      <PageTitle title={t("nav.settings")} />

      <Card className="p-6">
        <h3 className="text-sm font-semibold text-text">{t("settings.languageSection")}</h3>
        <div className="mt-3 flex flex-wrap gap-2">
          {LANGUAGES.map((l) => (
            <button
              key={l}
              onClick={() => setLang(l as Lang)}
              className={`rounded-md border px-4 py-2 text-sm font-medium transition-colors ${
                lang === l
                  ? "border-primary bg-surface-muted text-primary"
                  : "border-border bg-surface text-muted hover:bg-surface-muted"
              }`}
            >
              {LANGUAGE_LABELS[l]}
            </button>
          ))}
        </div>
      </Card>

      <Card className="p-6">
        <h3 className="text-sm font-semibold text-text">{t("settings.themeSection")}</h3>
        <div className="mt-3 flex gap-2">
          {(["light", "dark"] as Theme[]).map((th) => (
            <button
              key={th}
              onClick={() => setTheme(th)}
              className={`rounded-md border px-4 py-2 text-sm font-medium transition-colors ${
                theme === th
                  ? "border-primary bg-surface-muted text-primary"
                  : "border-border bg-surface text-muted hover:bg-surface-muted"
              }`}
            >
              {th === "light" ? t("nav.themeLight") : t("nav.themeDark")}
            </button>
          ))}
        </div>
      </Card>

      <Card className="p-6">
        <h3 className="text-sm font-semibold text-text">{t("settings.preferences")}</h3>
        <p className="mt-2 text-xs text-muted">{t("settings.prefHint")}</p>
      </Card>
    </div>
  );
}
