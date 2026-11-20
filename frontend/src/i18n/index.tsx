import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import en, { type Dictionary, type TranslationKey } from "./en";
import hy from "./hy";
import ru from "./ru";

// ── types ───────────────────────────────────────────────────────────────────

export const LANGUAGES = ["hy", "en", "ru"] as const;
export type Lang = (typeof LANGUAGES)[number];
export type Theme = "light" | "dark";

export const LANGUAGE_LABELS: Record<Lang, string> = {
  hy: "Հայերեն",
  en: "English",
  ru: "Русский",
};

const DICTS: Record<Lang, Dictionary> = { hy, en, ru };

// BCP-47 tags for Intl formatting per language.
const INTL_LOCALES: Record<Lang, string> = {
  hy: "hy-AM",
  en: "en-US",
  ru: "ru-RU",
};

const STORAGE = { lang: "launchpad.lang", theme: "launchpad.theme" } as const;

function readLang(): Lang {
  try {
    const v = localStorage.getItem(STORAGE.lang);
    if (v === "hy" || v === "en" || v === "ru") return v;
  } catch {
    /* storage unavailable */
  }
  return "hy"; // default language: Armenian
}

function readTheme(): Theme {
  try {
    const theme = localStorage.getItem(STORAGE.theme);
    if (theme === "light" || theme === "dark") return theme;
  } catch {
    // Fall through to the design's default theme.
  }
  return "dark";
}

// ── context ─────────────────────────────────────────────────────────────────

interface Settings {
  lang: Lang;
  theme: Theme;
  setLang: (l: Lang) => void;
  setTheme: (t: Theme) => void;
  toggleTheme: () => void;
  /** Translate a key with optional {param} interpolation. */
  t: (key: TranslationKey, params?: Record<string, string | number>) => string;
  /** Locale-aware date formatting for the active language. */
  formatDate: (iso: string | Date) => string;
  /** Locale-aware number formatting for the active language. */
  formatNumber: (n: number) => string;
  /** Locale-aware percentage for the active language. */
  formatPercent: (n: number) => string;
}

const SettingsContext = createContext<Settings | null>(null);

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(readLang);
  const [theme, setThemeState] = useState<Theme>(readTheme);

  // Apply + persist language (no reload — just <html lang> + re-render).
  useEffect(() => {
    document.documentElement.lang = lang;
    try {
      localStorage.setItem(STORAGE.lang, lang);
    } catch {
      /* ignore */
    }
  }, [lang]);

  // Apply + persist theme (data-theme flips CSS variables; no reload).
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    try {
      localStorage.setItem(STORAGE.theme, theme);
    } catch {
      /* ignore */
    }
  }, [theme]);

  const setLang = useCallback((l: Lang) => setLangState(l), []);
  const setTheme = useCallback((t: Theme) => setThemeState(t), []);
  const toggleTheme = useCallback(
    () => setThemeState((cur) => (cur === "light" ? "dark" : "light")),
    [],
  );

  const value = useMemo<Settings>(() => {
    const dict = DICTS[lang];
    const locale = INTL_LOCALES[lang];
    return {
      lang,
      theme,
      setLang,
      setTheme,
      toggleTheme,
      t: (key, params) => {
        let text: string = dict[key] ?? en[key];
        if (params) {
          for (const [k, v] of Object.entries(params)) {
            text = text.split(`{${k}}`).join(String(v));
          }
        }
        return text;
      },
      formatDate: (iso) =>
        new Intl.DateTimeFormat(locale, { dateStyle: "medium" }).format(
          typeof iso === "string" ? new Date(iso) : iso,
        ),
      formatNumber: (n) => new Intl.NumberFormat(locale).format(n),
      formatPercent: (n) => new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(n),
    };
  }, [lang, theme, setLang, setTheme, toggleTheme]);

  return <SettingsContext.Provider value={value}>{children}</SettingsContext.Provider>;
}

export function useSettings(): Settings {
  const ctx = useContext(SettingsContext);
  if (!ctx) throw new Error("useSettings must be used within SettingsProvider");
  return ctx;
}
