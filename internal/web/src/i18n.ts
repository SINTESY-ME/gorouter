// i18n setup: i18next + react-i18next, all locales bundled (embedded in the
// binary). Locale resolution: localStorage override → browser language → en.
import i18n from "i18next";
import { initReactI18next } from "react-i18next";

// Bundle every src/locales/<locale>/common.json at build time.
const resources = import.meta.glob("./locales/*/common.json", { eager: true });

type Resources = Record<string, { translation: Record<string, unknown> }>;

const loaded: Resources = {};
for (const path in resources) {
  const locale = path.split("/")[2];
  const mod = resources[path] as { default?: Record<string, unknown> };
  loaded[locale] = { translation: (mod.default ?? {}) as Record<string, unknown> };
}

export const DEFAULT_LOCALE = "en";
export const LOCALES = Object.keys(loaded);
// RTL locales (mirror OmniRoute).
export const RTL_LOCALES = new Set(["ar", "fa", "he", "ur"]);

const storedLocale = (() => {
  try {
    const v = localStorage.getItem("gorouter_locale");
    return v && loaded[v] ? v : null;
  } catch {
    return null;
  }
})();

const browserLocale = (() => {
  try {
    const l = navigator.language;
    if (loaded[l]) return l;
    const base = l.split("-")[0];
    if (loaded[base]) return base;
  } catch {}
  return null;
})();

export function applyLocaleDir(locale: string) {
  document.documentElement.dir = RTL_LOCALES.has(locale) ? "rtl" : "ltr";
}

i18n.use(initReactI18next).init({
  resources: loaded,
  lng: storedLocale ?? browserLocale ?? DEFAULT_LOCALE,
  fallbackLng: DEFAULT_LOCALE,
  interpolation: { escapeValue: false },
  returnEmptyString: false,
});

applyLocaleDir(i18n.language);

// setLocale switches the active language and persists the user's choice.
export function setLocale(locale: string) {
  if (!loaded[locale]) return;
  i18n.changeLanguage(locale);
  try {
    localStorage.setItem("gorouter_locale", locale);
  } catch {}
  applyLocaleDir(locale);
}

export default i18n;
