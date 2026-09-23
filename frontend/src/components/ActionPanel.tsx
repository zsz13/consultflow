import { useState } from "react";
import type { Action, ActionRequest, Availability, ConsultDetail } from "../api";
import { api, ApiError } from "../api";
import { useSession } from "../App";
import { roleLabels } from "../format";
import { RuleLink } from "./common";

interface Props {
  consult: ConsultDetail;
  onDone: (updated: ConsultDetail, summary: string) => void;
  onStale: () => void;
}

/** Actions the current role can take now, with a form for each one's inputs. */
export function ActionPanel({ consult, onDone, onStale }: Props) {
  const { role } = useSession();
  const [open, setOpen] = useState<Action | null>(null);
  const allowed = consult.availableActions.filter((a) => a.allowed);
  const blocked = consult.availableActions.filter((a) => !a.allowed);
  const current = allowed.find((a) => a.action === open) ?? null;

  return (
    <section className="panel actions" aria-labelledby="actions-title">
      <h2 id="actions-title">Actions</h2>
      <p className="panel-lede">
        As {roleLabels[role].toLowerCase()}, you can take{" "}
        {allowed.length === 0 ? "no actions" : allowed.length === 1 ? "one action" : `${allowed.length} actions`} on
        this consult.
      </p>
      {allowed.length > 0 && (
        <div className="action-buttons">
          {allowed.map((a) => (
            <button
              key={a.action}
              type="button"
              className={a.action === open ? "selected" : undefined}
              aria-expanded={a.action === open}
              onClick={() => setOpen(a.action === open ? null : a.action)}
            >
              {a.label}
            </button>
          ))}
        </div>
      )}
      {current && (
        <ActionForm
          key={current.action}
          availability={current}
          consult={consult}
          onCancel={() => setOpen(null)}
          onDone={(c, summary) => {
            setOpen(null);
            onDone(c, summary);
          }}
          onStale={onStale}
        />
      )}
      {blocked.length > 0 && (
        <details className="blocked-actions">
          <summary>Why {blocked.length === 1 ? "one action is" : `${blocked.length} actions are`} unavailable</summary>
          <ul>
            {blocked.map((a) => (
              <li key={a.action}>
                <strong>{a.label}</strong>
                <span>{a.reason}</span>
                {a.rule && <RuleLink id={a.rule} />}
              </li>
            ))}
          </ul>
        </details>
      )}
    </section>
  );
}

interface FormProps {
  availability: Availability;
  consult: ConsultDetail;
  onCancel: () => void;
  onDone: (c: ConsultDetail, summary: string) => void;
  onStale: () => void;
}

function ActionForm({ availability, consult, onCancel, onDone, onStale }: FormProps) {
  const { role, actor, services, meta } = useSession();
  const action = availability.action;
  const [comment, setComment] = useState("");
  const [toServiceId, setToServiceId] = useState<number>(action === "RESUBMIT" ? consult.toService.id : 0);
  const [urgency, setUrgency] = useState(action === "RESUBMIT" ? consult.urgency : "");
  const [attention, setAttention] = useState(action === "RESUBMIT" ? consult.attention : "");
  const [reason, setReason] = useState(consult.reasonForRequest);
  const [sig, setSig] = useState(action === "SIG_FINDINGS" ? consult.significantFindings || "Y" : "");
  const [noteTitle, setNoteTitle] = useState(`${consult.toService.name} CONSULT NOTE`);
  const [noteSigned, setNoteSigned] = useState(false);
  const unsigned = consult.notes.filter((n) => !n.signed);
  const [noteId, setNoteId] = useState(unsigned[0]?.id ?? 0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<{ message: string; rule?: string } | null>(null);

  const targets = services.filter(
    (s) => s.usage !== "DISABLED" && s.usage !== "GROUPER" && (action === "RESUBMIT" || s.id !== consult.toService.id),
  );
  const commentRequired = availability.requiresComment;

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const body: ActionRequest = { action, role, actor, comment };
    if (action === "FORWARD") Object.assign(body, { toServiceId, urgency, attention });
    if (action === "RESUBMIT") {
      Object.assign(body, { toServiceId, urgency, attention });
      if (reason.trim() !== consult.reasonForRequest.trim()) body.reason = reason;
    }
    if (action === "ADD_NOTE") Object.assign(body, { noteTitle, noteSigned });
    if (action === "SIGN_NOTE") body.noteId = noteId;
    if (action === "SIG_FINDINGS" || action === "ADMIN_COMPLETE") body.significantFindings = sig;
    try {
      const res = await api.act(consult.id, body);
      onDone(res.consult, res.outcome.summary);
    } catch (err) {
      if (err instanceof ApiError) {
        setError({ message: err.body.message, rule: err.body.rule });
        if (err.status === 409) onStale();
      } else {
        setError({ message: "The request did not reach the server. Check the API is running and try again." });
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="action-form" onSubmit={(e) => void submit(e)}>
      <h3>{availability.label}</h3>

      {(action === "FORWARD" || action === "RESUBMIT") && (
        <>
          <label>
            {action === "FORWARD" ? "Forward to" : "Send to"}
            <select required value={toServiceId} onChange={(e) => setToServiceId(Number(e.target.value))}>
              {action === "FORWARD" && (
                <option value={0} disabled>
                  Choose a service
                </option>
              )}
              {targets.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                  {s.ifcRoutingSite ? ` (inter-facility, ${s.ifcRoutingSite})` : ""}
                </option>
              ))}
            </select>
          </label>
          <label>
            Urgency
            <select value={urgency} onChange={(e) => setUrgency(e.target.value)}>
              {action === "FORWARD" && <option value="">Keep {consult.urgency.toLowerCase()}</option>}
              {meta?.urgencies.map((u) => (
                <option key={u} value={u}>
                  {u.toLowerCase()}
                </option>
              ))}
            </select>
          </label>
          <label>
            Attention
            <input
              value={attention}
              onChange={(e) => setAttention(e.target.value)}
              placeholder="LAST,FIRST (optional)"
            />
            {action === "FORWARD" && consult.attention && (
              <small>Leave empty to clear {consult.attention}; CPRS clears attention on forward.</small>
            )}
          </label>
        </>
      )}

      {action === "RESUBMIT" && (
        <label>
          Reason for request
          <textarea rows={3} value={reason} onChange={(e) => setReason(e.target.value)} />
        </label>
      )}

      {action === "ADD_NOTE" && (
        <>
          <label>
            Note title
            <input required value={noteTitle} onChange={(e) => setNoteTitle(e.target.value)} />
          </label>
          <label className="check">
            <input type="checkbox" checked={noteSigned} onChange={(e) => setNoteSigned(e.target.checked)} />
            The note is signed
            <small>
              {noteSigned
                ? "A signed note completes the consult."
                : "An unsigned note sets partial results until it is signed."}
            </small>
          </label>
        </>
      )}

      {action === "SIGN_NOTE" && (
        <label>
          Note
          <select value={noteId} onChange={(e) => setNoteId(Number(e.target.value))}>
            {unsigned.map((n) => (
              <option key={n.id} value={n.id}>
                {n.title}
              </option>
            ))}
          </select>
        </label>
      )}

      {(action === "SIG_FINDINGS" || action === "ADMIN_COMPLETE") && (
        <fieldset className="sig">
          <legend>Significant findings{action === "ADMIN_COMPLETE" ? " (optional)" : ""}</legend>
          {(action === "ADMIN_COMPLETE" ? ["", "Y", "N", "U"] : ["Y", "N", "U"]).map((v) => (
            <label key={v || "none"} className="radio">
              <input type="radio" name="sig" value={v} checked={sig === v} onChange={() => setSig(v)} />
              {{ "": "Not recorded", Y: "Yes", N: "No", U: "Unknown" }[v]}
            </label>
          ))}
        </fieldset>
      )}

      <label>
        Comment{commentRequired ? "" : " (optional)"}
        <textarea
          rows={3}
          required={commentRequired}
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          placeholder={commentRequired ? "Required: explain the action" : ""}
        />
      </label>

      {error && (
        <p className="form-error" role="alert">
          {error.message} {error.rule && <RuleLink id={error.rule} />}
        </p>
      )}
      <div className="form-buttons">
        <button type="submit" className="primary" disabled={busy || (action === "FORWARD" && toServiceId === 0)}>
          {busy ? "Saving" : availability.label}
        </button>
        <button type="button" onClick={onCancel}>
          Close
        </button>
      </div>
    </form>
  );
}
