import { createContext, useContext, useEffect, useState } from "react";
import type { Meta, Role, Service } from "./api";
import { api } from "./api";
import { defaultActors, roleLabels } from "./format";
import { Dashboard } from "./pages/Dashboard";
import { ConsultList } from "./pages/ConsultList";
import { ConsultDetail } from "./pages/ConsultDetail";
import { NewConsult } from "./pages/NewConsult";
import { LegacyRules } from "./pages/LegacyRules";

export interface Session {
  role: Role;
  actor: string;
  setRole: (r: Role) => void;
  setActor: (a: string) => void;
  services: Service[];
  meta: Meta | null;
}

const SessionContext = createContext<Session | null>(null);

export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error("useSession outside provider");
  return s;
}

function read(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function write(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // Storage unavailable (private mode); the setting just won't persist.
  }
}

function useHashRoute(): string {
  const [hash, setHash] = useState(() => window.location.hash.slice(1) || "/");
  useEffect(() => {
    const onChange = () => {
      setHash(window.location.hash.slice(1) || "/");
      window.scrollTo(0, 0);
    };
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return hash;
}

export function App() {
  const route = useHashRoute();
  const [role, setRoleState] = useState<Role>(() => (read("cf.role") as Role | null) ?? "SERVICE_USER");
  const [actor, setActorState] = useState(() => read("cf.actor") ?? defaultActors[role]);
  const [services, setServices] = useState<Service[]>([]);
  const [meta, setMeta] = useState<Meta | null>(null);

  useEffect(() => {
    api
      .services()
      .then(setServices)
      .catch(() => setServices([]));
    api
      .meta()
      .then(setMeta)
      .catch(() => setMeta(null));
  }, []);

  const setRole = (r: Role) => {
    setRoleState(r);
    write("cf.role", r);
    const a = defaultActors[r];
    setActorState(a);
    write("cf.actor", a);
  };
  const setActor = (a: string) => {
    setActorState(a);
    write("cf.actor", a);
  };

  const [path = "/", query = ""] = route.split("?");
  const params = new URLSearchParams(query);
  const detail = path.match(/^\/consults\/(\d+)$/);

  let page: React.ReactNode;
  let section = "dashboard";
  if (detail) {
    page = <ConsultDetail key={detail[1]} id={Number(detail[1])} />;
    section = "consults";
  } else if (path === "/consults") {
    page = <ConsultList params={params} />;
    section = "consults";
  } else if (path === "/new") {
    page = <NewConsult />;
    section = "new";
  } else if (path === "/rules") {
    page = <LegacyRules focus={params.get("rule")} />;
    section = "rules";
  } else {
    page = <Dashboard />;
  }

  return (
    <SessionContext.Provider value={{ role, actor, setRole, setActor, services, meta }}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="topbar">
        <div className="topbar-inner">
          <a className="brand" href="#/">
            <svg viewBox="0 0 32 32" aria-hidden="true">
              <rect width="32" height="32" rx="7" />
              <path d="M8 16h6l3-6 3 12 2-6h2" />
            </svg>
            ConsultFlow
          </a>
          <nav aria-label="Main">
            <a href="#/" aria-current={section === "dashboard" ? "page" : undefined}>
              Dashboard
            </a>
            <a href="#/consults" aria-current={section === "consults" ? "page" : undefined}>
              Consults
            </a>
            <a href="#/new" aria-current={section === "new" ? "page" : undefined}>
              New consult
            </a>
            <a href="#/rules" aria-current={section === "rules" ? "page" : undefined}>
              Legacy rules
            </a>
          </nav>
          <div className="acting-as">
            <label htmlFor="role">Acting as</label>
            <select id="role" value={role} onChange={(e) => setRole(e.target.value as Role)}>
              {(Object.keys(roleLabels) as Role[]).map((r) => (
                <option key={r} value={r}>
                  {roleLabels[r]}
                </option>
              ))}
            </select>
            <input
              aria-label="Your name (VistA format LAST,FIRST)"
              value={actor}
              onChange={(e) => setActor(e.target.value)}
              spellCheck={false}
            />
          </div>
        </div>
      </header>
      <main id="main" tabIndex={-1}>
        {page}
      </main>
      <footer className="site-footer">
        Synthetic demo data only. Workflow rules recovered from VistA Consult/Request Tracking (GMRC 3.0);{" "}
        <a href="#/rules">see how each rule traces to the legacy source</a>.
      </footer>
    </SessionContext.Provider>
  );
}
