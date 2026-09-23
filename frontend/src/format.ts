import type { Role } from "./api";

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

/** Duration since iso: "3 days", "5 hours", "less than an hour". */
export function since(iso: string, now = Date.now()): string {
  const ms = Math.max(0, now - new Date(iso).getTime());
  if (ms < HOUR) return "less than an hour";
  if (ms < 2 * DAY) {
    const h = Math.floor(ms / HOUR);
    return h === 1 ? "1 hour" : `${h} hours`;
  }
  return `${Math.floor(ms / DAY)} days`;
}

/** How long ago iso was, in plain words. */
export const ago = (iso: string, now = Date.now()) => `${since(iso, now)} ago`;

const dateTime = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  hour: "numeric",
  minute: "2-digit",
});
const dateTimeYear = new Intl.DateTimeFormat("en-US", {
  month: "short",
  day: "numeric",
  year: "numeric",
  hour: "numeric",
  minute: "2-digit",
});
const dateOnly = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric" });

/** "Sep 9, 1:31 PM"; the year is shown only when it is not the current one. */
export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  return (d.getFullYear() === new Date().getFullYear() ? dateTime : dateTimeYear).format(d);
}
export const formatDate = (iso: string) => dateOnly.format(new Date(iso));

export const roleLabels: Record<Role, string> = {
  REQUESTER: "Requesting provider",
  SERVICE_USER: "Service clinician",
  SERVICE_ADMIN: "Service administrator",
};

/** Default synthetic actor names for each role. */
export const defaultActors: Record<Role, string> = {
  REQUESTER: "ZZPROVIDER,BRUNO",
  SERVICE_USER: "ZZSTAFF,RILEY",
  SERVICE_ADMIN: "ZZADMIN,KAI",
};

export function titleCase(s: string): string {
  return s.toLowerCase().replace(/(^|[\s,/-])([a-z])/g, (_, sep: string, c: string) => sep + c.toUpperCase());
}

/** VistA names are "LAST,FIRST"; keep that form (it marks ZZ test entries) but space it. */
export function personName(vista: string): string {
  return vista.replace(/,\s*/, ", ");
}

export const sigFindingsLabel: Record<string, string> = { Y: "Yes", N: "No", U: "Unknown", "": "Not entered" };
