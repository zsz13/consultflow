import { useState } from "react";
import type { ConsultDetail as Detail, Status } from "../api";
import { api } from "../api";
import { useSession } from "../App";
import { ActionPanel } from "../components/ActionPanel";
import { FlagList, LoadError, useLoad } from "../components/common";
import { StatusPill, StatusTrack } from "../components/Status";
import { Timeline } from "../components/Timeline";
import { ago, formatDate, formatDateTime, personName, roleLabels, sigFindingsLabel, titleCase } from "../format";

export function ConsultDetail({ id }: { id: number }) {
  const { role } = useSession();
  const detail = useLoad(() => api.consult(id, role), `${id}:${role}`);
  const timeline = useLoad(() => api.timeline(id), String(id));
  const [notice, setNotice] = useState<string | null>(null);

  const error = detail.error ?? timeline.error;
  if (error && (!detail.data || !timeline.data)) {
    return (
      <LoadError
        message={error}
        retry={() => {
          detail.reload();
          timeline.reload();
        }}
      />
    );
  }
  const c = detail.data;
  if (!c || !timeline.data) return <p className="loading">Loading consult #{id}</p>;

  const visited = new Set<Status>(timeline.data.map((a) => a.newStatus));

  const onDone = (updated: Detail, summary: string) => {
    detail.setData(updated);
    timeline.reload();
    setNotice(`${summary}. Status is now ${updated.statusLabel.toLowerCase()}.`);
  };
  const onStale = () => {
    detail.reload();
    timeline.reload();
  };

  return (
    <article className="detail">
      <a className="back" href="#/consults">
        All consults
      </a>
      <header className="detail-head">
        <div>
          <h1>
            <span className="consult-no">Consult #{c.id}</span> {personName(c.patientName)}
          </h1>
          <p className="detail-sub">
            {titleCase(c.requestType)} to <strong>{c.toService.name}</strong>
            {c.procedure && <> for {c.procedure}</>} from {c.fromLocation}, requested {ago(c.dateOfRequest)} by{" "}
            {personName(c.requestingProvider)}
          </p>
        </div>
        <div className="detail-status">
          <StatusPill status={c.status} />
          <span className="urgency" data-urgent={["STAT", "EMERGENCY", "TODAY"].includes(c.urgency) || undefined}>
            {c.urgency.toLowerCase()}
          </span>
        </div>
      </header>

      <StatusTrack current={c.status} visited={visited} />

      {notice && (
        <p className="notice success" role="status">
          {notice}
        </p>
      )}

      <div className="detail-grid">
        <div className="detail-main">
          <section className={c.explanation.blocked ? "why blocked" : "why"} aria-labelledby="why-title">
            <h2 id="why-title">
              {c.explanation.blocked ? "Why this consult is blocked" : "What this consult is waiting for"}
            </h2>
            <p className="why-headline">{c.explanation.headline}</p>
            <p className="why-owner">
              Next move: <strong>{c.explanation.owner}</strong>
            </p>
            <ul className="why-reasons">
              {c.explanation.reasons.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
            <FlagList flags={c.flags} />
            {c.explanation.nextSteps.length > 0 && (
              <>
                <h3>Next steps</h3>
                <ol className="next-steps">
                  {c.explanation.nextSteps.map((s) => (
                    <li key={s.text}>
                      {s.text}
                      {s.role && s.role !== role && (
                        <span className="step-role"> ({roleLabels[s.role].toLowerCase()})</span>
                      )}
                    </li>
                  ))}
                </ol>
              </>
            )}
          </section>

          <section className="timeline-section" aria-labelledby="timeline-title">
            <h2 id="timeline-title">Timeline</h2>
            <p className="panel-lede">
              Every action, oldest first, with the status before and after. The code on each entry is the legacy REQUEST
              ACTION TYPES entry GMRC records.
            </p>
            <Timeline activities={timeline.data} />
          </section>
        </div>

        <div className="detail-side">
          <ActionPanel consult={c} onDone={onDone} onStale={onStale} />

          <section className="panel order-panel" aria-labelledby="order-title">
            <h2 id="order-title">Order information</h2>
            <dl className="order-info">
              <div>
                <dt>Patient</dt>
                <dd>
                  {personName(c.patientName)} <span className="muted">({c.patientRef}, synthetic)</span>
                </dd>
              </div>
              <div>
                <dt>To service</dt>
                <dd>{c.toService.name}</dd>
              </div>
              <div>
                <dt>From service</dt>
                <dd>{c.fromLocation}</dd>
              </div>
              <div>
                <dt>Requesting provider</dt>
                <dd>{personName(c.requestingProvider)}</dd>
              </div>
              <div>
                <dt>Attention</dt>
                <dd>{c.attention ? personName(c.attention) : <span className="muted">None</span>}</dd>
              </div>
              <div>
                <dt>Urgency</dt>
                <dd>{c.urgency.toLowerCase()}</dd>
              </div>
              <div>
                <dt>Place</dt>
                <dd>{c.place ? c.place.toLowerCase() : <span className="muted">Not set</span>}</dd>
              </div>
              <div>
                <dt>Setting</dt>
                <dd>{c.inpatientOutpatient === "I" ? "Inpatient" : "Outpatient"}</dd>
              </div>
              <div>
                <dt>Date of request</dt>
                <dd>{formatDateTime(c.dateOfRequest)}</dd>
              </div>
              <div>
                <dt>Clinically indicated date</dt>
                <dd>
                  {c.clinicallyIndicatedDate ? (
                    formatDate(c.clinicallyIndicatedDate)
                  ) : (
                    <span className="muted">None</span>
                  )}
                </dd>
              </div>
              <div>
                <dt>Provisional diagnosis</dt>
                <dd>{c.provisionalDiagnosis || <span className="muted">None</span>}</dd>
              </div>
              <div>
                <dt>Significant findings</dt>
                <dd>{sigFindingsLabel[c.significantFindings]}</dd>
              </div>
              {c.ifcRole && (
                <div>
                  <dt>Inter-facility</dt>
                  <dd>
                    {c.ifcRole === "P"
                      ? `Placed here, filled by ${c.ifcRemoteSite}`
                      : `Placed by ${c.ifcRemoteSite}, filled here`}
                    {c.ifcRemoteConsultId && <span className="muted"> (remote #{c.ifcRemoteConsultId})</span>}
                  </dd>
                </div>
              )}
              <div>
                <dt>Last action</dt>
                <dd>
                  <code>{c.lastAction}</code>
                </dd>
              </div>
            </dl>
            <h3>Reason for request</h3>
            <p className="reason">{c.reasonForRequest}</p>
            {c.notes.length > 0 && (
              <>
                <h3>Linked notes</h3>
                <ul className="notes">
                  {c.notes.map((n) => (
                    <li key={n.id}>
                      <strong>{n.title}</strong>{" "}
                      <span className={n.signed ? "signed" : "unsigned"}>{n.signed ? "Signed" : "Unsigned"}</span>
                      <span className="muted">
                        {" "}
                        {personName(n.author)}, {ago(n.createdAt)}
                      </span>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </section>
        </div>
      </div>
    </article>
  );
}
