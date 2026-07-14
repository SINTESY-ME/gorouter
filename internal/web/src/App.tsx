import { useEffect, useState } from "react";
import { Routes, Route, NavLink, useLocation } from "react-router-dom";
import { api, clearDashboardToken } from "./api";
import Dashboard from "./pages/Dashboard";
import Providers from "./pages/Providers";
import Combos from "./pages/Combos";
import Keys from "./pages/Keys";
import Logs from "./pages/Logs";
import Models from "./pages/Models";
import Performance from "./pages/Performance";
import Setup from "./pages/Setup";
import Login from "./pages/Login";

const nav = [
  { to: "/", label: "Dashboard", icon: IconHome, end: true },
  { to: "/providers", label: "Providers", icon: IconServer },
  { to: "/combos", label: "Combos", icon: IconLayers },
  { to: "/models", label: "Models", icon: IconBox },
  { to: "/keys", label: "API Keys", icon: IconKey },
  { to: "/logs", label: "Logs", icon: IconActivity },
  { to: "/performance", label: "Performance", icon: IconGauge },
];

const titleMap: Record<string, string> = {
  "/": "Dashboard",
  "/providers": "Providers",
  "/combos": "Combos",
  "/models": "Models",
  "/keys": "API Keys",
  "/logs": "Logs",
  "/performance": "Performance",
};

type AuthState = "loading" | "setup" | "login" | "dashboard";

export default function App() {
  const [authState, setAuthState] = useState<AuthState>("loading");

  async function checkAuth() {
    try {
      const s = await api.auth.status();
      if (!s.configured) setAuthState("setup");
      else if (!s.authenticated) setAuthState("login");
      else setAuthState("dashboard");
    } catch {
      setAuthState("dashboard");
    }
  }

  useEffect(() => { checkAuth(); }, []);

  function logout() {
    clearDashboardToken();
    setAuthState("login");
  }

  if (authState === "loading") {
    return (
      <div className="min-h-screen bg-default-50 flex items-center justify-center">
        <p className="text-sm text-default-400">Carregando...</p>
      </div>
    );
  }
  if (authState === "setup") return <Setup onDone={checkAuth} />;
  if (authState === "login") return <Login onDone={checkAuth} />;

  return <DashboardLayout onLogout={logout} />;
}

function DashboardLayout({ onLogout }: { onLogout: () => void }) {
  const loc = useLocation();
  const title = titleMap[loc.pathname] ?? "gorouter";
  return (
    <div className="min-h-screen bg-default-50 text-foreground flex">
      <aside className="w-60 bg-content1 border-r border-default-100 flex flex-col">
        <div className="px-5 py-5 flex items-center gap-3 border-b border-default-100">
          <IconRoute className="w-5 h-5 text-white" />
          <div>
            <p className="font-semibold text-base leading-tight">gorouter</p>
            <p className="text-xs text-default-500 leading-tight">LLM router</p>
          </div>
        </div>
        <nav className="p-3 space-y-1 flex-1">
          {nav.map((it) => (
            <NavLink
              key={it.to}
              to={it.to}
              end={it.end}
              className={({ isActive }) =>
                `flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors ${
                  isActive
                    ? "bg-primary/15 text-primary font-medium"
                    : "text-default-600 hover:bg-default-100"
                }`
              }
            >
              <it.icon className="w-4 h-4" />
              {it.label}
            </NavLink>
          ))}
        </nav>
        <div className="p-3 border-t border-default-100 space-y-2">
          <button
            onClick={onLogout}
            className="w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm text-default-600 hover:bg-default-100 transition-colors"
          >
            <IconLogout className="w-4 h-4" />
            Sair
          </button>
          <p className="text-xs text-default-400 px-3">v0.1 · port :20128</p>
        </div>
      </aside>
      <div className="flex-1 flex flex-col min-w-0">
        <header className="h-14 px-6 flex items-center border-b border-default-100 bg-content1/60 backdrop-blur">
          <h2 className="text-lg font-semibold">{title}</h2>
        </header>
        <main className="flex-1 p-6 overflow-auto">
          <div className="max-w-6xl mx-auto">
            <Routes>
              <Route path="/" element={<Dashboard />} />
              <Route path="/providers" element={<Providers />} />
              <Route path="/combos" element={<Combos />} />
              <Route path="/models" element={<Models />} />
              <Route path="/keys" element={<Keys />} />
              <Route path="/logs" element={<Logs />} />
              <Route path="/performance" element={<Performance />} />
            </Routes>
          </div>
        </main>
      </div>
    </div>
  );
}

function IconRoute({ className }: { className?: string }) {
  return (
    <svg className={className} viewBox="0 0 14 20" fill="currentColor" xmlns="http://www.w3.org/2000/svg" fillRule="evenodd">
      <path d="M10.008,12.17 C10.008,13.275 9.108,14.17 8,14.17 C6.89,14.17 5.992,13.275 5.992,12.17 C5.992,11.065 6.89,10.17 8,10.17 C9.108,10.17 10.008,11.065 10.008,12.17 M7.973,18.005 C5.39,18.005 3.035,16.295 2.239,13.848 C1.446,11.41 2.358,8.739 4.344,7.227 C4.894,6.808 5.095,6.113 5.005,5.428 C4.781,3.732 6.099,2 7.973,2 C9.846,2 11.164,3.732 10.94,5.428 C10.85,6.112 11.051,6.808 11.601,7.227 C13.586,8.739 14.499,11.41 13.705,13.848 C12.91,16.295 10.555,18.005 7.973,18.005 M13.316,6.039 C13.076,5.823 12.955,5.519 12.968,5.198 C13.075,2.432 10.833,0 7.973,0 C5.111,0 2.868,2.433 2.977,5.2 C2.989,5.52 2.869,5.824 2.629,6.038 C-1.615,9.817 -0.632,17.124 4.94,19.416 C7.89,20.629 11.377,19.909 13.631,17.658 C17.125,14.17 16.528,8.905 13.316,6.039" />
    </svg>
  );
}
function IconHome({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg>;
}
function IconServer({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg>;
}
function IconLayers({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>;
}
function IconBox({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/><polyline points="3.27 6.96 12 12.01 20.73 6.96"/><line x1="12" y1="22.08" x2="12" y2="12"/></svg>;
}
function IconKey({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="7.5" cy="15.5" r="5.5"/><path d="m21 2-9.6 9.6"/><path d="m15.5 7.5 3 3L22 7l-3-3"/></svg>;
}
function IconActivity({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>;
}
function IconGauge({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m12 14 4-4"/><path d="M3.34 19a10 10 0 1 1 17.32 0"/></svg>;
}
function IconLogout({ className }: { className?: string }) {
  return <svg className={className} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/></svg>;
}