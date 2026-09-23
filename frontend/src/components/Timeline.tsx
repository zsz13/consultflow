import type { Activity } from "../api";
import { formatDateTime, personName, roleLabels } from "../format";
import { StatusPill } from "./Status";

const detailLabels: Record<string, string> = {
  forwardedFrom: "Forwarded from",
  forwardedTo: "Forwarded to",
  previousAttention: "Previous attention",
  remoteSite: "Remote facility",
  noteTitle: "Note",
  ifcUpdate: "Inter-facility",
  duplicatesAcknowledged: "Ordered despite possible duplicates",
};

function actorRole(a: Activity): string {
  return a.actorRole === "SYSTEM" ? "System" : roleLabels[a.actorRole];
}

function DetailRows({ details }: { details: Record<string, unknown> }) {
  const rows: [string, string][] = [];
  for (const [key, value] of Object.entries(details)) {
    if (key === "noteId" || key === "ifcRole") continue;
    if (key === "previousValues") {
      const prev = value as Record<string, string>;
      for (const [field, old] of Object.entries(prev)) rows.push([`Previous ${field.toLowerCase()}`, old || "(empty)"]);
      continue;
    }
    const text = Array.isArray(value) ? value.map((v) => `#${String(v)}`).join(", ") : String(value);
    rows.push([detailLabels[key] ?? key, text]);
  }
  if (rows.length === 0) return null;
  return (
    <dl className="event-details">
      {rows.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Chronological audit trail: every action with its before/after status. */
export function Timeline({ activities }: { activities: Activity[] }) {
  return (
    <ol className="timeline">
      {activities.map((a) => {
        const changed = a.previousStatus !== a.newStatus;
        return (
          <li key={a.id} data-changed={changed || undefined}>
            <time dateTime={a.occurredAt}>{formatDateTime(a.occurredAt)}</time>
            <div className="event">
              <p className="event-summary">{a.summary}</p>
              <p className="event-transition">
                {a.previousStatus === null ? (
                  <>
                    Filed as <StatusPill status={a.newStatus} />
                  </>
                ) : changed ? (
                  <>
                    <StatusPill status={a.previousStatus} /> <span aria-label="changed to">to</span>{" "}
                    <StatusPill status={a.newStatus} />
                  </>
                ) : (
                  <>
                    Status unchanged <StatusPill status={a.newStatus} />
                  </>
                )}
              </p>
              {a.comment && <blockquote>{a.comment}</blockquote>}
              <DetailRows details={a.details} />
              <p className="event-meta">
                {a.actorRole === "SYSTEM" ? a.actor : personName(a.actor)}, {actorRole(a)}
                <code title="REQUEST ACTION TYPES (#123.1) entry recorded by legacy GMRC">
                  123.1 #{a.legacyActionIen} {a.legacyActionName}
                </code>
              </p>
            </div>
          </li>
        );
      })}
    </ol>
  );
}
