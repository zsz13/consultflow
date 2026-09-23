import type { Status } from "../api";

export const statusLabels: Record<Status, string> = {
  PENDING: "Pending",
  ACTIVE: "Active",
  SCHEDULED: "Scheduled",
  PARTIAL_RESULTS: "Partial results",
  COMPLETE: "Complete",
  CANCELLED: "Cancelled",
  DISCONTINUED: "Discontinued",
};

/** The forward path a consult normally travels, in order. */
const mainLine: Status[] = ["PENDING", "ACTIVE", "SCHEDULED", "PARTIAL_RESULTS", "COMPLETE"];
/** Ways off the line. */
const exits: Status[] = ["CANCELLED", "DISCONTINUED"];

export function StatusPill({ status }: { status: Status }) {
  return (
    <span className="status-pill" data-status={status}>
      {statusLabels[status]}
    </span>
  );
}

interface FlowProps {
  counts: Partial<Record<Status, number>>;
  attention: Partial<Record<Status, number>>;
}

/** Dashboard view of the state machine: how many consults sit at each status. */
export function StatusFlow({ counts, attention }: FlowProps) {
  const station = (s: Status) => {
    const n = counts[s] ?? 0;
    const a = attention[s] ?? 0;
    return (
      <li key={s} data-status={s} className={n === 0 ? "empty" : undefined}>
        <a href={`#/consults?status=${s}`}>
          <span className="count">{n}</span>
          <span className="name">{statusLabels[s]}</span>
          {a > 0 && <span className="needs">{a} need attention</span>}
        </a>
      </li>
    );
  };
  return (
    <div className="flow">
      <ol className="flow-line" aria-label="Consults by status along the workflow">
        {mainLine.map(station)}
      </ol>
      <ul className="flow-exits" aria-label="Consults that left the workflow">
        {exits.map(station)}
      </ul>
      <p className="flow-note">
        A cancelled consult goes back to the requester. It returns to pending when resubmitted, or is discontinued after
        31 days.
      </p>
    </div>
  );
}

/** Detail view: the statuses this consult has passed through, and where it is now. */
export function StatusTrack({ current, visited }: { current: Status; visited: Set<Status> }) {
  const line = exits.includes(current) ? [...mainLine.slice(0, 3), current] : mainLine;
  return (
    <ol className="track" aria-label="Status history">
      {line.map((s) => {
        const state = s === current ? "current" : visited.has(s) ? "visited" : "ahead";
        return (
          <li key={s} data-status={s} data-state={state} aria-current={s === current ? "step" : undefined}>
            <span className="dot" aria-hidden="true" />
            <span className="name">{statusLabels[s]}</span>
          </li>
        );
      })}
    </ol>
  );
}
