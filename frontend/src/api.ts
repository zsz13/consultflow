// Types mirror backend/internal/api and backend/internal/workflow JSON.

export type Status = "PENDING" | "ACTIVE" | "SCHEDULED" | "PARTIAL_RESULTS" | "COMPLETE" | "DISCONTINUED" | "CANCELLED";

export type Role = "REQUESTER" | "SERVICE_USER" | "SERVICE_ADMIN";

export type Action =
  | "CREATE"
  | "RECEIVE"
  | "SCHEDULE"
  | "FORWARD"
  | "DISCONTINUE"
  | "CANCEL"
  | "RESUBMIT"
  | "ADD_NOTE"
  | "SIGN_NOTE"
  | "ADMIN_COMPLETE"
  | "COMMENT"
  | "SIG_FINDINGS"
  | "AUTO_DISCONTINUE";

export interface Service {
  id: number;
  name: string;
  usage: "" | "GROUPER" | "TRACKING" | "DISABLED";
  ifcRoutingSite?: string;
  synthetic?: boolean;
}

export interface Flag {
  code: string;
  severity: "critical" | "warning";
  title: string;
  detail: string;
  rule: string;
}

export interface NextStep {
  action?: Action;
  role?: Role;
  text: string;
}

export interface Explanation {
  headline: string;
  owner: string;
  blocked: boolean;
  reasons: string[];
  nextSteps: NextStep[];
}

export interface Note {
  id: number;
  title: string;
  signed: boolean;
  author: string;
  createdAt: string;
  signedAt: string | null;
}

export interface Consult {
  id: number;
  patientName: string;
  patientRef: string;
  toService: Service;
  fromLocation: string;
  requestingProvider: string;
  attention: string;
  urgency: string;
  place: string;
  inpatientOutpatient: "I" | "O";
  requestType: "CONSULT" | "PROCEDURE";
  procedure: string;
  reasonForRequest: string;
  provisionalDiagnosis: string;
  dateOfRequest: string;
  clinicallyIndicatedDate: string | null;
  status: Status;
  statusLabel: string;
  statusChangedAt: string;
  statusComment: string;
  lastAction: string;
  significantFindings: "" | "Y" | "N" | "U";
  ifcRole: "" | "P" | "F";
  ifcRemoteSite: string;
  ifcRemoteConsultId: string;
  notes: Note[];
  flags: Flag[];
  needsAttention: boolean;
  duplicates: number[];
  explanation: Explanation;
}

export interface Availability {
  action: Action;
  label: string;
  allowed: boolean;
  reason?: string;
  rule?: string;
  requiresComment: boolean;
}

export interface ConsultDetail extends Consult {
  role: Role;
  availableActions: Availability[];
}

export interface Activity {
  id: number;
  consultId: number;
  action: Action;
  legacyActionIen: number;
  legacyActionName: string;
  previousStatus: Status | null;
  newStatus: Status;
  actor: string;
  actorRole: Role | "SYSTEM";
  occurredAt: string;
  recordedAt: string;
  summary: string;
  comment: string;
  details: Record<string, unknown>;
}

export interface StatusInfo {
  status: Status;
  label: string;
  legacyIen: number;
  legacyName: string;
  legacyAbbr: string;
  closed: boolean;
  description: string;
}

export interface Dashboard {
  generatedAt: string;
  totals: Record<string, number>;
  statusCounts: (StatusInfo & { count: number })[];
  attention: Consult[];
  byService: { serviceId: number; name: string; open: number; attention: number }[];
  recentActivity: Activity[];
}

export interface LegacyRef {
  file: string;
  line: number;
  snippet: string;
}

export interface Rule {
  id: string;
  title: string;
  kind: "LEGACY" | "DERIVED" | "ASSUMPTION";
  behavior: string;
  legacy: LegacyRef[];
  modern: string[];
}

export interface Meta {
  statuses: StatusInfo[];
  urgencies: string[];
  roles: Role[];
}

export interface ApiErrorBody {
  kind: string;
  message: string;
  rule?: string;
  ruleTitle?: string;
  duplicates?: number[];
}

export class ApiError extends Error {
  constructor(
    public status: number,
    public body: ApiErrorBody,
  ) {
    super(body.message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const data: unknown = await res.json().catch(() => null);
  if (!res.ok) {
    const err = (data as { error?: ApiErrorBody } | null)?.error;
    throw new ApiError(res.status, err ?? { kind: "HTTP", message: `Request failed (${res.status})` });
  }
  return data as T;
}

export interface ActionRequest {
  action: Action;
  role: Role;
  actor: string;
  comment?: string;
  toServiceId?: number;
  urgency?: string;
  attention?: string | null;
  reason?: string;
  significantFindings?: string;
  noteTitle?: string;
  noteSigned?: boolean;
  noteId?: number;
}

export interface NewConsultRequest {
  patientName: string;
  patientRef: string;
  toServiceId: number;
  fromLocation: string;
  requestingProvider: string;
  attention: string;
  urgency: string;
  place: string;
  inpatientOutpatient: "I" | "O";
  requestType: "CONSULT" | "PROCEDURE";
  procedure: string;
  reasonForRequest: string;
  provisionalDiagnosis: string;
  clinicallyIndicatedDate: string | null;
  acknowledgeDuplicate: boolean;
}

export const api = {
  meta: () => request<Meta>("GET", "/api/meta"),
  services: () => request<Service[]>("GET", "/api/services"),
  dashboard: () => request<Dashboard>("GET", "/api/dashboard"),
  consults: (params: Record<string, string>) =>
    request<Consult[]>("GET", "/api/consults?" + new URLSearchParams(params).toString()),
  consult: (id: number, role: Role) => request<ConsultDetail>("GET", `/api/consults/${id}?role=${role}`),
  timeline: (id: number) => request<Activity[]>("GET", `/api/consults/${id}/timeline`),
  act: (id: number, body: ActionRequest) =>
    request<{ consult: ConsultDetail; outcome: { summary: string } }>("POST", `/api/consults/${id}/actions`, body),
  create: (body: NewConsultRequest) => request<ConsultDetail>("POST", "/api/consults", body),
  rules: () => request<Rule[]>("GET", "/api/traceability"),
  runAutoDiscontinue: () => request<{ discontinued: number[] }>("POST", "/api/jobs/auto-discontinue"),
  resetDemo: () => request<{ status: string }>("POST", "/api/demo/reset"),
};
