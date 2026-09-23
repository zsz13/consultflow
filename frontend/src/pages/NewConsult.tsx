import { useState } from "react";
import type { NewConsultRequest } from "../api";
import { api, ApiError } from "../api";
import { useSession } from "../App";
import { RuleLink } from "../components/common";

const locations = ["PRIMARY CARE CLINIC A", "EMERGENCY DEPARTMENT", "5 WEST MEDICINE", "MENTAL HEALTH CLINIC"];
const places = ["CONSULTANT'S CHOICE", "BEDSIDE"];

export function NewConsult() {
  const { services, meta, role, actor } = useSession();
  const [form, setForm] = useState<NewConsultRequest>({
    patientName: "ZZTEST,QUEBEC",
    patientRef: "DEMO-0101",
    toServiceId: 0,
    fromLocation: "PRIMARY CARE CLINIC A",
    requestingProvider: role === "REQUESTER" ? actor : "ZZPROVIDER,BRUNO",
    attention: "",
    urgency: "ROUTINE",
    place: "CONSULTANT'S CHOICE",
    inpatientOutpatient: "O",
    requestType: "CONSULT",
    procedure: "",
    reasonForRequest: "",
    provisionalDiagnosis: "",
    clinicallyIndicatedDate: null,
    acknowledgeDuplicate: false,
  });
  const [error, setError] = useState<{ message: string; rule?: string } | null>(null);
  const [duplicates, setDuplicates] = useState<number[] | null>(null);
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof NewConsultRequest>(k: K, v: NewConsultRequest[K]) => {
    setForm((f) => ({ ...f, [k]: v }));
    setDuplicates(null);
  };
  const orderable = services.filter((s) => s.usage !== "DISABLED" && s.usage !== "GROUPER");

  async function submit(acknowledge: boolean) {
    setBusy(true);
    setError(null);
    try {
      const c = await api.create({ ...form, acknowledgeDuplicate: acknowledge });
      window.location.hash = `/consults/${c.id}`;
    } catch (err) {
      if (err instanceof ApiError && err.body.kind === "DUPLICATE") {
        setDuplicates(err.body.duplicates ?? []);
      } else if (err instanceof ApiError) {
        setError({ message: err.body.message, rule: err.body.rule });
      } else {
        setError({ message: "The request did not reach the server. Check the API is running and try again." });
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="new-page">
      <h1>Place a consult</h1>
      <p className="panel-lede">
        The order is filed as pending and goes to the service's receive queue. Use synthetic patients only.
      </p>
      <form
        className="new-form"
        onSubmit={(e) => {
          e.preventDefault();
          void submit(false);
        }}
      >
        <fieldset>
          <legend>Patient</legend>
          <label>
            Name (LAST,FIRST)
            <input required value={form.patientName} onChange={(e) => set("patientName", e.target.value)} />
          </label>
          <label>
            Identifier
            <input required value={form.patientRef} onChange={(e) => set("patientRef", e.target.value)} />
          </label>
        </fieldset>

        <fieldset>
          <legend>Order</legend>
          <label>
            To service
            <select required value={form.toServiceId} onChange={(e) => set("toServiceId", Number(e.target.value))}>
              <option value={0} disabled>
                Choose a service
              </option>
              {orderable.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                  {s.ifcRoutingSite ? ` (inter-facility, ${s.ifcRoutingSite})` : ""}
                </option>
              ))}
            </select>
          </label>
          <label>
            Request type
            <select
              value={form.requestType}
              onChange={(e) => set("requestType", e.target.value as "CONSULT" | "PROCEDURE")}
            >
              <option value="CONSULT">Consult</option>
              <option value="PROCEDURE">Procedure</option>
            </select>
          </label>
          {form.requestType === "PROCEDURE" && (
            <label>
              Procedure
              <input required value={form.procedure} onChange={(e) => set("procedure", e.target.value)} />
            </label>
          )}
          <label>
            Urgency
            <select value={form.urgency} onChange={(e) => set("urgency", e.target.value)}>
              {meta?.urgencies.map((u) => (
                <option key={u} value={u}>
                  {u.toLowerCase()}
                </option>
              ))}
            </select>
          </label>
          <label>
            Place of consultation
            <select value={form.place} onChange={(e) => set("place", e.target.value)}>
              {places.map((p) => (
                <option key={p} value={p}>
                  {p.toLowerCase()}
                </option>
              ))}
            </select>
          </label>
          <label>
            Setting
            <select
              value={form.inpatientOutpatient}
              onChange={(e) => set("inpatientOutpatient", e.target.value as "I" | "O")}
            >
              <option value="O">Outpatient</option>
              <option value="I">Inpatient</option>
            </select>
          </label>
          <label>
            Clinically indicated date (optional)
            <input
              type="date"
              value={form.clinicallyIndicatedDate?.slice(0, 10) ?? ""}
              onChange={(e) => set("clinicallyIndicatedDate", e.target.value ? `${e.target.value}T00:00:00Z` : null)}
            />
          </label>
        </fieldset>

        <fieldset>
          <legend>Requester</legend>
          <label>
            From service
            <select value={form.fromLocation} onChange={(e) => set("fromLocation", e.target.value)}>
              {locations.map((l) => (
                <option key={l} value={l}>
                  {l}
                </option>
              ))}
            </select>
          </label>
          <label>
            Requesting provider
            <input
              required
              value={form.requestingProvider}
              onChange={(e) => set("requestingProvider", e.target.value)}
            />
          </label>
          <label>
            Attention (optional)
            <input value={form.attention} onChange={(e) => set("attention", e.target.value)} placeholder="LAST,FIRST" />
          </label>
        </fieldset>

        <fieldset className="wide">
          <legend>Clinical question</legend>
          <label>
            Reason for request
            <textarea
              required
              rows={4}
              value={form.reasonForRequest}
              onChange={(e) => set("reasonForRequest", e.target.value)}
            />
          </label>
          <label>
            Provisional diagnosis (optional)
            <input value={form.provisionalDiagnosis} onChange={(e) => set("provisionalDiagnosis", e.target.value)} />
          </label>
        </fieldset>

        {duplicates && (
          <div className="duplicate-warning" role="alert">
            <p>
              <strong>This patient already has an open consult to this service.</strong> Legacy CPRS warns about
              pending, active or scheduled orders for the same service and procedure within 12 months:{" "}
              {duplicates.map((id, i) => (
                <span key={id}>
                  {i > 0 && ", "}
                  <a href={`#/consults/${id}`}>#{id}</a>
                </span>
              ))}
              . <RuleLink id="R-DUPLICATE" />
            </p>
            <div className="form-buttons">
              <button type="button" className="primary" disabled={busy} onClick={() => void submit(true)}>
                Place consult anyway
              </button>
              <button type="button" onClick={() => setDuplicates(null)}>
                Review the order
              </button>
            </div>
          </div>
        )}
        {error && (
          <p className="form-error" role="alert">
            {error.message} {error.rule && <RuleLink id={error.rule} />}
          </p>
        )}
        {!duplicates && (
          <div className="form-buttons">
            <button type="submit" className="primary" disabled={busy}>
              {busy ? "Placing consult" : "Place consult"}
            </button>
          </div>
        )}
      </form>
    </div>
  );
}
