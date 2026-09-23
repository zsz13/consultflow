import { useState } from "react";
import type { Status } from "../api";
import { api } from "../api";
import { useSession } from "../App";
import { LoadError, useLoad } from "../components/common";
import { StatusPill, statusLabels } from "../components/Status";
import { personName, since } from "../format";

const bucketTabs: [string, string][] = [
  ["attention", "Needs attention"],
  ["active", "Active"],
  ["awaiting_scheduling", "Waiting to be received or scheduled"],
  ["awaiting_results", "Waiting for results"],
  ["returned", "Returned to requester"],
  ["closed", "Closed"],
  ["all", "All"],
];

function navigate(params: URLSearchParams) {
  window.location.hash = "/consults?" + params.toString();
}

export function ConsultList({ params }: { params: URLSearchParams }) {
  const { services } = useSession();
  const status = params.get("status") ?? "";
  const bucket = status ? "all" : (params.get("bucket") ?? "active");
  const serviceId = params.get("serviceId") ?? "";
  const q = params.get("q") ?? "";
  const [text, setText] = useState(q);

  const query: Record<string, string> = { bucket };
  if (status) query.status = status;
  if (serviceId) query.serviceId = serviceId;
  if (q) query.q = q;
  const { data, error, reload } = useLoad(() => api.consults(query), JSON.stringify(query));

  const set = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    if (key === "bucket") next.delete("status");
    navigate(next);
  };

  return (
    <div className="list-page">
      <h1>Consults</h1>
      <nav className="tabs" aria-label="Consult groups">
        {bucketTabs.map(([key, label]) => (
          <a
            key={key}
            href={`#/consults?bucket=${key}${serviceId ? `&serviceId=${serviceId}` : ""}`}
            aria-current={!status && bucket === key ? "page" : undefined}
          >
            {label}
          </a>
        ))}
      </nav>

      <form
        className="filters"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          set("q", text.trim());
        }}
      >
        <label>
          Search
          <input
            type="search"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Consult #, patient, service, reason"
          />
        </label>
        <label>
          Service
          <select value={serviceId} onChange={(e) => set("serviceId", e.target.value)}>
            <option value="">All services</option>
            {services
              .filter((s) => s.usage !== "GROUPER")
              .map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
          </select>
        </label>
        {status && (
          <p className="filter-chip">
            Status: {statusLabels[status as Status]}{" "}
            <button type="button" onClick={() => set("status", "")}>
              Clear status
            </button>
          </p>
        )}
        <button type="submit">Search</button>
      </form>

      {error && !data ? (
        <LoadError message={error} retry={reload} />
      ) : !data ? (
        <p className="loading">Loading consults</p>
      ) : data.length === 0 ? (
        <p className="empty-state">
          No consults match. <a href="#/consults?bucket=all">Show all consults</a> or{" "}
          <a href="#/new">place a new consult</a>.
        </p>
      ) : (
        <div className="table-wrap">
          <table className="consult-table">
            <caption className="visually-hidden">Consults, most urgent first</caption>
            <thead>
              <tr>
                <th scope="col">Consult</th>
                <th scope="col">Patient</th>
                <th scope="col">To service</th>
                <th scope="col">Status</th>
                <th scope="col">Urgency</th>
                <th scope="col">Waiting on</th>
              </tr>
            </thead>
            <tbody>
              {data.map((c) => (
                <tr key={c.id} data-severity={c.flags[0]?.severity}>
                  <th scope="row">
                    <a href={`#/consults/${c.id}`}>#{c.id}</a>
                  </th>
                  <td>{personName(c.patientName)}</td>
                  <td>
                    {c.toService.name}
                    {c.ifcRole && <span className="ifc-tag">{c.ifcRole === "P" ? "IFC placed" : "IFC received"}</span>}
                  </td>
                  <td>
                    <StatusPill status={c.status} />
                    <span className="cell-sub">{since(c.statusChangedAt)}</span>
                  </td>
                  <td>{c.urgency.toLowerCase()}</td>
                  <td>
                    {c.flags[0] ? <strong className="cell-flag">{c.flags[0].title}</strong> : null}
                    <span className="cell-sub">{c.explanation.headline}</span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
