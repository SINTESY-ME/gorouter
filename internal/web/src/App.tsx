import { useEffect, useState } from "react";
import { Routes, Route, NavLink } from "react-router-dom";
import { Button, Toast, Select, ListBox } from "@heroui/react";
import { useTranslation } from "react-i18next";
import { setLocale, LOCALES, LANGUAGE_NAMES } from "./i18n";
import { api, clearDashboardToken, User } from "./api";
import Dashboard from "./pages/Dashboard";
import Providers from "./pages/Providers";
import Combos from "./pages/Combos";
import Keys from "./pages/Keys";
import Logs from "./pages/Logs";
import Models from "./pages/Models";
import Performance from "./pages/Performance";
import Settings from "./pages/Settings";
import Users from "./pages/Users";
import Playground from "./pages/Playground";
import MCP from "./pages/MCP";
import Setup from "./pages/Setup";
import Login from "./pages/Login";
import { IconRoute, IconHome, IconServer, IconLayers, IconBox, IconKey, IconActivity, IconGauge, IconChat, IconLogout, IconSettings, IconUsers, IconPlug } from "./icons";

interface NavItem {
  to: string;
  labelKey: string;
  icon: (props: { className?: string }) => React.JSX.Element;
  end?: boolean;
  adminOnly?: boolean;
  permKey?: "can_manage_cache" | "can_access_settings";
}

const nav: NavItem[] = [
  { to: "/", labelKey: "app.nav.dashboard", icon: IconHome, end: true },
  { to: "/providers", labelKey: "app.nav.providers", icon: IconServer },
  { to: "/combos", labelKey: "app.nav.combos", icon: IconLayers },
  { to: "/models", labelKey: "app.nav.models", icon: IconBox },
  { to: "/mcp", labelKey: "app.nav.mcp", icon: IconPlug },
  { to: "/keys", labelKey: "app.nav.keys", icon: IconKey },
  { to: "/playground", labelKey: "app.nav.playground", icon: IconChat },
  { to: "/logs", labelKey: "app.nav.logs", icon: IconActivity },
  { to: "/performance", labelKey: "app.nav.performance", icon: IconGauge, permKey: "can_manage_cache" },
  { to: "/settings", labelKey: "app.nav.settings", icon: IconSettings, permKey: "can_access_settings" },
  { to: "/users", labelKey: "app.nav.users", icon: IconUsers, adminOnly: true },
];

type AuthState = "loading" | "setup" | "login" | "dashboard";

export default function App() {
  const { t } = useTranslation();
  const [authState, setAuthState] = useState<AuthState>("loading");
  const [me, setMe] = useState<User | null>(null);

  async function checkAuth() {
    try {
      const s = await api.auth.status();
      if (!s.configured) setAuthState("setup");
      else if (!s.authenticated) setAuthState("login");
      else {
        try { setMe(await api.auth.me()); } catch { setMe(null); }
        setAuthState("dashboard");
      }
    } catch {
      setAuthState("dashboard");
    }
  }

  useEffect(() => { checkAuth(); }, []);

  async function logout() {
    try { await api.auth.logout(); } catch {}
    clearDashboardToken();
    setMe(null);
    setAuthState("login");
  }

  if (authState === "loading") {
    return (
      <div className="min-h-screen bg-background flex items-center justify-center">
        <p className="text-sm text-muted">{t("app.loading")}</p>
      </div>
    );
  }
  if (authState === "setup") return <Setup onDone={checkAuth} />;
  if (authState === "login") return <Login onDone={checkAuth} />;

  return <DashboardLayout onLogout={logout} me={me} />;
}

function DashboardLayout({ onLogout, me }: { onLogout: () => void; me: User | null }) {
  const { t } = useTranslation();
  const isAdmin = me?.role === "admin";
  const perms = me?.permissions;
  const visibleNav = nav.filter((it) => {
    if (it.adminOnly && !isAdmin) return false;
    if (it.permKey && !isAdmin && !perms?.[it.permKey]) return false;
    return true;
  });
  return (
    <>
    <Toast.Provider />
    <div className="h-screen bg-background text-foreground flex overflow-hidden">
      <aside className="w-60 bg-surface border-r border-border flex flex-col h-screen overflow-y-auto shrink-0">
        <div className="px-5 py-5 flex items-center gap-3 border-b border-border">
          <IconRoute className="w-5 h-5 text-accent" />
          <div>
            <p className="font-semibold text-base leading-tight">{t("app.brand")}</p>
            <p className="text-xs text-muted leading-tight">{t("app.tagline")}</p>
          </div>
        </div>
        <nav className="p-3 space-y-1 flex-1">
          {visibleNav.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.end}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors ${
                  isActive
                    ? "bg-accent/15 text-accent font-medium"
                    : "text-foreground/80 hover:bg-default-soft"
                }`
              }
            >
              <it.icon className="w-4 h-4" />
              {t(it.labelKey)}
            </NavLink>
          ))}
        </nav>
        <div className="p-3 border-t border-border space-y-2">
          <ReadinessPill />
          <LanguageSwitcher />
          <div className="px-3 py-1 flex items-center justify-between gap-2">
            <div className="min-w-0">
              <p className="text-sm font-medium truncate">{me?.name ?? "Admin"}</p>
              <p className="text-xs text-muted">{isAdmin ? t("app.roleAdmin") : t("app.roleMember")}</p>
            </div>
            <Button variant="tertiary" isIconOnly onPress={onLogout} aria-label={t("app.logout")}>
              <IconLogout className="w-4 h-4" />
            </Button>
          </div>
          <p className="text-xs text-muted px-3">{t("app.version")}</p>
        </div>
      </aside>
       <div className="flex-1 flex flex-col min-w-0 overflow-hidden">
         <main className="flex-1 overflow-auto">
            <Routes>
              <Route path="/" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Dashboard /></div></div>} />
              <Route path="/providers" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Providers /></div></div>} />
              <Route path="/combos" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Combos /></div></div>} />
              <Route path="/models" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Models /></div></div>} />
              <Route path="/mcp" element={<div className="p-6"><div className="max-w-6xl mx-auto"><MCP /></div></div>} />
              <Route path="/playground" element={<Playground />} />
              <Route path="/keys" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Keys /></div></div>} />
              <Route path="/logs" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Logs /></div></div>} />
              <Route path="/performance" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Performance /></div></div>} />
              <Route path="/settings" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Settings /></div></div>} />
              <Route path="/users" element={<div className="p-6"><div className="max-w-6xl mx-auto"><Users /></div></div>} />
            </Routes>
        </main>
      </div>
    </div>
    </>
  );
}

// ReadinessPill shows a live ready/not-ready indicator from /health/readiness.
function ReadinessPill() {
  const { t } = useTranslation();
  const [ready, setReady] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    const check = () => api.health.ready().then((ok) => { if (!cancelled) setReady(ok); }).catch(() => { if (!cancelled) setReady(false); });
    check();
    const t = setInterval(check, 15000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  return (
    <div className="flex items-center gap-2 px-3 py-1.5">
      <span className={`w-2 h-2 rounded-full ${ready === null ? "bg-warning" : ready ? "bg-success" : "bg-danger"}`} />
      <span className="text-xs text-muted">
        {ready === null ? t("app.checking") : ready ? t("app.ready") : t("app.notReady")}
      </span>
    </div>
  );
}

// LanguageSwitcher lets the user pick the UI language; persisted in
// localStorage and reflected live (including RTL direction).
function LanguageSwitcher() {
  const { i18n } = useTranslation();
  const current = i18n.resolvedLanguage ?? "en";

  return (
    <div className="px-1.5 pb-1">
      <Select
        aria-label="Language"
        selectedKey={current}
        onSelectionChange={(k) => setLocale(String(k))}
        className="w-full"
      >
        <Select.Trigger>
          <Select.Value>{LANGUAGE_NAMES[current] ?? current}</Select.Value>
          <Select.Indicator />
        </Select.Trigger>
        <Select.Popover>
          <ListBox>
            {LOCALES.map((l) => (
              <ListBox.Item key={l} id={l}>{LANGUAGE_NAMES[l] ?? l}</ListBox.Item>
            ))}
          </ListBox>
        </Select.Popover>
      </Select>
    </div>
  );
}
