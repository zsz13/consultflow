import { useEffect, useState } from "react";
import type { Consult, Status } from "../api";
import { api } from "../api";
import { LoadError, useLoad } from "../components/common";
import { StatusFlow, StatusPill } from "../components/Status";
import { ago, personName, since } from "../format";

export function Dashboard() {
  const { data, error, reload } = useLoad(() => api.dashboard(), "dashboard");
  const [notice, setNotice] = useState<string | null>(null);

  // Keep the board current while it is open.
  useEffect(() => {
    const t = window.setInterval(reload, 15_000);
    return () => window.clearInterval(t);
  }, [reload]);

  if (error && !data) return <LoadError message={error} retry={reload} />;
  if (!data) return <p className="loading">Loading dashboard</p>;

  const counts: Partial<Record<Status, number>> = {};
  for (const s of data.statusCounts) counts[s.status] = s.count;
  const attentionByStatus: Partial<Record<Status, number>> = {};
  for (const c of data.attention) attentionByStatus[c.status] = (attentionByStatus[c.status] ?? 0) + 1;
  const t = data.totals;
  const need = data.attention.length;
  const critical = t.critical ?? 0;

  async function runJob() {
    try {
      const res = await api.runAutoDiscontinue();
      setNotice(
        res.discontinued.length === 0
          ? "No cancelled consult has reached 31 days. Nothing was discontinued."
          : `Discontinued ${res.discontinued.map((id) => `#${id}`).join(", ")} after 31 days in cancelled status.`,
      );
    } catch (e) {
      setNotice(`The job did not run: ${e instanceof Error ? e.message : String(e)}`);
    }
    reload();
  }

  async function resetDemo() {
    try {
      await api.resetDemo();
      setNotice("Demo data reset. Scenario timings are relative to now.");
    } catch (e) {
      setNotice(`Reset failed: ${e instanceof Error ? e.message : String(e)}`);
    }
    reload();
  }

  return (
    <div className="dashboard">
      <section className="board-head">
        <h1>
          {need === 0
            ? "No consults need attention"
            : `${need} ${need === 1 ? "consult needs" : "consults need"} attention`}
        </h1>
        <p>
          {critical > 0 && (
            <>
              <strong className="critical-text">{critical} critical.</strong>{" "}
            </>
          )}
          {t.active ?? 0} active consults: {t.awaiting_scheduling ?? 0} waiting to be received or scheduled,{" "}
          {t.awaiting_results ?? 0} waiting for results. {t.returned ?? 0} returned to requesters.
        </p>
      </section>

      <div className="board">
        <section className="ledger" aria-labelledby="ledger-title">
          <div className="section-head">
            <h2 id="ledger-title">Needs attention</h2>
            <a href="#/consults?bucket=attention">Open as list</a>
          </div>
          {need === 0 ? (
            <p className="empty-state">
              Every consult is moving. New exceptions appear here as soon as a rule is breached.
            </p>
          ) : (
            <ol className="exceptions">
              {data.attention.map((c) => (
                <ExceptionRow key={c.id} consult={c} />
              ))}
            </ol>
          )}
        </section>

        <aside className="board-side">
          <section aria-labelledby="flow-title">
            <h2 id="flow-title">Where consults are</h2>
            <StatusFlow counts={counts} attention={attentionByStatus} />
          </section>

          <section aria-labelledby="svc-title">
            <h2 id="svc-title">Open by service</h2>
            <table className="svc-table">
              <thead>
                <tr>
                  <th scope="col">Service</th>
                  <th scope="col" className="num">
                    Open
                  </th>
                  <th scope="col" className="num">
                    Attention
                  </th>
                </tr>
              </thead>
              <tbody>
                {data.byService.map((s) => (
                  <tr key={s.serviceId}>
                    <th scope="row">
                      <a href={`#/consults?bucket=active&serviceId=${s.serviceId}`}>{s.name}</a>
                    </th>
                    <td className="num">{s.open}</td>
                    <td className={s.attention > 0 ? "num has-attention" : "num"}>{s.attention}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>

          <section aria-labelledby="recent-title">
            <h2 id="recent-title">Latest activity</h2>
            <ol className="recent">
              {data.recentActivity.map((a) => (
                <li key={a.id}>
                  <a href={`#/consults/${a.consultId}`}>
                    <span className="recent-what">
                      #{a.consultId} {a.summary}
                    </span>
                    <span className="recent-when">
                      {a.actorRole === "SYSTEM" ? a.actor : personName(a.actor)}, {ago(a.occurredAt)}
                    </span>
                  </a>
                </li>
              ))}
            </ol>
          </section>

          <section className="demo-controls" aria-labelledby="demo-title">
            <h2 id="demo-title">Demo controls</h2>
            <p>Run the legacy overnight job now, or restore the synthetic scenarios.</p>
            <div className="form-buttons">
              <button type="button" onClick={() => void runJob()}>
                Run auto-discontinue job
              </button>
              <button type="button" onClick={() => void resetDemo()}>
                Reset demo data
              </button>
            </div>
            {notice && (
              <p className="notice" role="status">
                {notice}
              </p>
            )}
          </section>
        </aside>
      </div>
    </div>
  );
}

function ExceptionRow({ consult: c }: { consult: Consult }) {
  const top = c.flags[0];
  if (!top) return null;
  const more = c.flags.length - 1;
  return (
    <li data-severity={top.severity}>
      <a href={`#/consults/${c.id}`}>
        <span className="ex-title">{top.title}</span>
        <span className="ex-detail">{top.detail}</span>
        <span className="ex-meta">
          <span className="ex-id">#{c.id}</span> {personName(c.patientName)} to {c.toService.name}
          <StatusPill status={c.status} />
          <span className="ex-age">{since(c.statusChangedAt)} in status</span>
          {more > 0 && <span className="ex-more">+{more} more</span>}
        </span>
      </a>
    </li>
  );
}
