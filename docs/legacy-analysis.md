# Legacy analysis: GMRC Consult/Request Tracking

What ConsultFlow recovered from VistA, how firmly each part is established, and what was assumed. The machine-checked
version of the rules is `backend/internal/workflow/trace.go`, shown in the app under **Legacy rules**.

## Scope

Only `Packages/Consult Request Tracking/` and `Packages.csv` were analysed. One file outside the package was read,
because GMRC stores statuses as pointers into it: `Packages/Order Entry Results Reporting/Globals/100.01+ORDER STATUS.zwr`.

**Files inspected**

- `Packages.csv`: the GMRC rows (files 123, 123.033, 123.1, 123.3, 123.5, 123.6, 123.9)
- Globals: `123.1+REQUEST ACTION TYPES`, `123.5+REQUEST SERVICES`, `123.9+CONSULTS PARAMETERS FILE`, and `123`, `123.6`,
  `123.033` (headers only; the export contains no consults)
- Routines read in full: `GMRCGUIA` (GUI new/receive/DC/forward), `GMRCGUIB` (GUI complete, schedule, comment),
  `GMRCP` (status update and audit), `GMRCACTM` (action menus), `GMRCAAC` (admin complete), `GMRCDPCK` (duplicates)
- Routines read in part: `GMRCA1` (receive/schedule), `GMRCADC` (discontinue), `GMRCAFRD` (forward), `GMRCASV` (service
  selection), `GMRCTIU`, `GMRCTIU1`, `GMRCTIUE` (notes to status), `GMRCEDT2`, `GMRCEDT3`, `GMRCEDIT` (edit/resubmit),
  `GMRCCXDC` (overnight cancel→DC), `GMRCASF` (significant findings), `GMRCSTL1` / `GMRCSTL8` (active list, 30/60-day
  monitor), `GMRCSLM2` (detail display), `GMRCHL7A` (urgency codes, inbound DC), `GMRCIACT` (inbound IFC)

## Recovered entities

**REQUEST/CONSULTATION (#123).** The 0-node pieces were identified from routine code. The file's data dictionary is
not in the export, so field names come from comments and display code.

| Piece | Field                           | Evidence                                    | ConsultFlow column          |
| ----- | ------------------------------- | ------------------------------------------- | --------------------------- |
| 2     | .02 patient                     | `DFN=$P(^GMR(123,GMRCO,0),"^",2)`           | `patient_name`, `patient_ref` |
| 5     | 1 TO SERVICE → #123.5           | `GMRCSLM2` "To Service:"                    | `to_service_id`             |
| 6     | FROM (hospital location)        | `GMRCSLM2` "From Service:"                  | `from_location`             |
| 7     | 3 DATE OF REQUEST               | `GMRCSTL8:179`                              | `date_of_request`           |
| 8     | 4 PROCEDURE                     | `GMRCEDT2`                                  | `procedure_name`            |
| 9     | 5 URGENCY → #101 protocol       | `GMRCSLM2` "Urgency:"                       | `urgency`                   |
| 10    | 6 PLACE OF CONSULTATION         | `GMRCSLM2` "Place:"                         | `place`                     |
| 11    | 7 ATTENTION                     | `GMRCSLM2` "Attention:"                     | `attention`                 |
| 12    | 8 CPRS STATUS → #100.01         | `GMRCGUIA:94`                               | `status`                    |
| 13    | 9 LAST ACTION TAKEN → #123.1    | `GMRCP` STATUS                              | `last_action`               |
| 14    | 10 SENDING PROVIDER             | `GMRCSLM2` "Requesting Provider:"           | `requesting_provider`       |
| 18    | 14 inpatient/outpatient         | `GMRCSLM2`                                  | `inpatient_outpatient`      |
| 19    | 15 SIGNIFICANT FINDINGS (Y/N/U) | `GMRCASF`, `GMRCGUIB:78-80`                 | `significant_findings`      |
| 24    | CLINICALLY INDICATED DATE       | `GMRCSLM2:69`                               | `clinically_indicated_date` |

Other nodes: `20` reason for request (word processing), `30` provisional diagnosis, `12` IFC data (piece 5 role:
`P` placer, `F` filler), `50` linked results (TIU notes), `40` activity log.

**REQUEST PROCESSING ACTIVITY (#123.02, node 40).** Fields: .01 date/time entered, 1 activity (→ #123.1), 2 date/time
of actual activity, 3 responsible person, 4 entered by, 6 forwarded from, 7 previous attention, 9 linked result, and a
comment. See `GMRCP:41`. ConsultFlow stores one row per action in `consult_activities`, adding the previous and new
status.

**REQUEST ACTION TYPES (#123.1).** 24 entries. Modelled: 2 CPRS RELEASED ORDER, 4 SIG FINDING UPDATE, 6 DISCONTINUED,
8 SCHEDULED, 9 INCOMPLETE RPT, 10 COMPLETE/UPDATE, 11 EDIT/RESUBMITTED, 14 NEW NOTE ADDED, 17 FORWARDED FROM,
19 CANCELLED, 20 ADDED COMMENT, 21 RECEIVED, 23 REMOTE REQUEST RECEIVED, 25 FWD TO REMOTE SERVICE. Not modelled:
1 (superseded by 2 after patch 21), 3, 12, 13, 15, 16, 22, 24, 26, 99.

**REQUEST SERVICES (#123.5).** The real national entries (MEDICINE, CARDIOLOGY, GASTROENTEROLOGY, HEMATOLOGY, PULMONARY,
RHEUMATOLOGY, PHARMACY SERVICE, …). Piece 2 usage: blank normal, 1 grouper, 2 tracking, 9 disabled (`GMRCASV`). A
service with an `IFC` node routes consults to another facility. The remote service in the demo is synthetic.

**ORDER STATUS (#100.01).** 1 dc, 2 comp, 5 pend, 6 actv, 8 schd, 9 part and 13 canc are the values GMRC sets.

## Confirmed transitions

Every row cites the guard and the assignment in the source; see `trace.go` for exact lines.

- **Create:** status 5 (`NEW^GMRCGUIA`). Duplicate warning when the same patient, service and procedure has a
  pending, active or scheduled consult within 365 days; the user may continue (`GMRCDPCK`).
- **Receive:** only from 5 → 6, action 21 (`GMRCA1:77,100`).
- **Schedule:** from 5 or 6 → 8, action 8 (`GMRCA1:82,102`).
- **Forward:** refused for 1/2/13, for 9, and with an unsigned note. The target must differ from the current service
  and must not be disabled. → 5, action 17, ATTENTION cleared unless re-entered. Forwarding to an IFC service records
  action 25 and makes this site the placer. An IFC consult may not be forwarded to another IFC service (`GMRCAFRD`,
  `GMRCGUIA:FR`, `GMRCASV`).
- **Discontinue / cancel (deny):** a comment is required (`GMRCGUIA:93`); → 1 (action 6) or 13 (action 19)
  (`GMRCGUIA:98`). The List Manager path refuses 1, 2, 9 and 13 (`GMRCADC:38-41`). The GUI path refuses only dc and
  comp (`GMRCGUIA:94-97`; see the quirk below). A DC order control from CPRS on the ordering side (`GMRCHL7A:115` →
  `DC^GMRCHL7B`) has no status guard. ConsultFlow applies the List Manager guards to every role.
- **Edit/resubmit:** only from 13 → 5, action 11. The activity comment records the previous values of edited fields.
  Allowed for the ordering provider or a service update user (`GMRCEDT2`, `GMRCEDT3`, `VALPROV^GMRCEDIT`).
- **Notes:** an unsigned (incomplete) note gives action 9 and status 9; a signed note gives action 10 and status 2. A
  completed consult never drops back to 9. A new note on a completed consult is action 14 when results are already
  linked, otherwise 10 (`EVALACT`). No notes on 1 or 13 (`GMRCTIU:12`, `GMRCTIU1:89,154-156`, `GMRCTIUE:128`). The
  addendum action 13 is not modelled.
- **Administrative complete:** refused for 1, 2 and 13 → 2, action 10. The GUI requires comment text. Administrative
  users only (`GMRCAAC`, `GMRCGUIB:101`, `GMRCTIUE:30`).
- **Comment / significant findings:** no status change and no status guard (`GMRCGUIB:CMT`, `GMRCASF`).
- **IFC placer:** the requesting facility may not take the service-side actions: receive, schedule, forward, cancel,
  discontinue from the service screens, notes, completion, significant findings (`GMRCA1:59`, `GMRCTIUE:15`,
  `GMRCACTM:37`). Comments and edit/resubmit remain. The ordering provider can still discontinue the order, because
  `DC^GMRCHL7B` has no IFC check and `GMRCIEVT:22` handles an IFC discontinued before it was sent.
- **Overnight job:** discontinues consults that stayed cancelled, through the normal DC API, with an `ADC:` comment
  (`GMRCCXDC`). Legacy runs it only when the `CSLT CANCELLED TO DISCONTINUED` parameter enables it, scans a configured
  window of days, and records the cancelling provider as responsible. ConsultFlow's fixed 31 days with no upper window
  is an assumption, marked DERIVED in the rule list.

## Legacy quirk preserved deliberately

`DC^GMRCGUIA` (line 96) checks the status abbreviation against `"ca"`, but ORDER STATUS uses `"canc"`, so the GUI DC
path does not actually block cancelled consults. The overnight job depends on this to discontinue them. ConsultFlow
refuses user DC of a cancelled consult (as the List Manager path `GMRCADC` does) and allows it only for the system job.

## Assumptions (not established by the source)

- **Attention flags are a modern layer.** Legacy has reports, not an exception queue. The thresholds are grounded where
  possible:
  - The 30/60-day completion windows use the consult performance monitor's measure (`GMRCSTL8 CHKRNG`).
  - Auto-DC in N days comes from `GMRCCXDC`.
  - Partial results blocking comes from `GMRCGUIA`/`GMRCADC`.
  - Duplicates come from `GMRCDPCK`.
- **Urgency windows:** urgency values come from `GMRCHL7A URG`. The 24/48/72 hours, 1 week and 1 month windows follow
  from their names. STAT, EMERGENCY and TODAY = 24 hours, and ROUTINE / NEXT AVAILABLE = 7 days, are assumptions.
- **Roles** (requester, service user, service administrator, system) simplify GMRC's per-service update and
  administrative authority (`GMRCACTM`, `VALID^GMRCAU`). The requester may discontinue (as through a CPRS order DC),
  comment and resubmit. Any service user may forward to a tracking-only service; legacy limits that to that service's
  update users (`GMRCASV:41`). A service administrator may also link notes; legacy sends a pure administrative user to
  administrative complete (`GMRCTIUE:30`).
- **Resubmitting to a different service** routes like a new order: an IFC service makes this site the placer, and
  moving a placed IFC to a local service makes it local. The edit routines read here do not show how legacy handles
  this case.
- **The receive clock** (used by the urgency flag) restarts when a consult is forwarded, because the new service has
  not seen it yet.
- **`POST /api/demo/reset`** truncates all tables, including the append-only audit log. It is a demo control only and
  would be removed outside a hackathon.
- **Auto-discontinue delay:** fixed at 31 days, from the routine title. Legacy reads it from the
  `CSLT CANCELLED TO DISCONTINUED` parameter, whose value is not in the export.
- **A consult received from another facility** is filed with action 23 and role `F` (`GMRCIACT:59`); the HL7 exchange
  itself is not modelled.
- **Status-only guards:** the CPRS GUI (Delphi, not in this repository) may enable or disable menu items beyond the
  server-side guards found here. ConsultFlow enforces the stricter List Manager guards on the server for every client.
