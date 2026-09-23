// Package workflow is the explicit consult state model recovered from the
// VistA Consult/Request Tracking (GMRC) package. It is pure: no I/O and no
// clock of its own, so every rule is unit-testable and deterministic.
package workflow

import "time"

// Status is the consult's CPRS STATUS. GMRC stores it as a pointer to the
// ORDER STATUS file (#100.01) in 0-node piece 12 of file #123.
type Status string

const (
	Pending        Status = "PENDING"
	Active         Status = "ACTIVE"
	Scheduled      Status = "SCHEDULED"
	PartialResults Status = "PARTIAL_RESULTS"
	Complete       Status = "COMPLETE"
	Discontinued   Status = "DISCONTINUED"
	Cancelled      Status = "CANCELLED"
)

// StatusInfo ties a modern status to its ORDER STATUS (#100.01) entry.
type StatusInfo struct {
	Status      Status `json:"status"`
	Label       string `json:"label"`
	LegacyIEN   int    `json:"legacyIen"`
	LegacyName  string `json:"legacyName"`
	LegacyAbbr  string `json:"legacyAbbr"`
	Closed      bool   `json:"closed"`
	Description string `json:"description"`
}

var statuses = []StatusInfo{
	{Pending, "Pending", 5, "PENDING", "pend", false, "Ordered, forwarded or resubmitted; not yet received by the consulting service."},
	{Active, "Active", 6, "ACTIVE", "actv", false, "Received by the consulting service."},
	{Scheduled, "Scheduled", 8, "SCHEDULED", "schd", false, "Scheduled by the consulting service; waiting for results."},
	{PartialResults, "Partial results", 9, "PARTIAL RESULTS", "part", false, "An unsigned (incomplete) note is linked to the consult."},
	{Cancelled, "Cancelled", 13, "CANCELLED", "canc", false, "Cancelled (denied) by the service and returned to the requester for edit/resubmit."},
	{Complete, "Complete", 2, "COMPLETE", "comp", true, "Completed by a signed note or by administrative completion."},
	{Discontinued, "Discontinued", 1, "DISCONTINUED", "dc", true, "Discontinued; no further workflow."},
}

// Statuses returns every status in display order.
func Statuses() []StatusInfo { return append([]StatusInfo(nil), statuses...) }

// Info returns the metadata for s.
func (s Status) Info() (StatusInfo, bool) {
	for _, i := range statuses {
		if i.Status == s {
			return i, true
		}
	}
	return StatusInfo{}, false
}

// Valid reports whether s is a known status.
func (s Status) Valid() bool { _, ok := s.Info(); return ok }

// Open reports whether the consult is still in the service's active work
// list: pending, active, scheduled or partial results (GMRCSTL1).
func (s Status) Open() bool {
	return s == Pending || s == Active || s == Scheduled || s == PartialResults
}

// Closed reports whether the consult is complete or discontinued.
func (s Status) Closed() bool { return s == Complete || s == Discontinued }

// Role is a simplified stand-in for the GMRC user levels (GMRCACTM, VALID^GMRCAU).
type Role string

const (
	RoleRequester    Role = "REQUESTER"     // ordering (sending) provider
	RoleServiceUser  Role = "SERVICE_USER"  // update user of the consulting service
	RoleServiceAdmin Role = "SERVICE_ADMIN" // administrative update user
	RoleSystem       Role = "SYSTEM"        // background jobs (e.g. GMRCCXDC)
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleRequester, RoleServiceUser, RoleServiceAdmin, RoleSystem:
		return true
	}
	return false
}

// LegacyAction is an entry in REQUEST ACTION TYPES (#123.1), which GMRC
// stores as the ACTIVITY of each audit entry and as LAST ACTION TAKEN.
type LegacyAction struct {
	IEN  int    `json:"ien"`
	Name string `json:"name"`
}

var (
	laReleased     = LegacyAction{2, "CPRS RELEASED ORDER"}
	laSigFinding   = LegacyAction{4, "SIG FINDING UPDATE"}
	laDiscontinued = LegacyAction{6, "DISCONTINUED"}
	laScheduled    = LegacyAction{8, "SCHEDULED"}
	laIncomplete   = LegacyAction{9, "INCOMPLETE RPT"}
	laComplete     = LegacyAction{10, "COMPLETE/UPDATE"}
	laResubmitted  = LegacyAction{11, "EDIT/RESUBMITTED"}
	laNewNote      = LegacyAction{14, "NEW NOTE ADDED"}
	laForwarded    = LegacyAction{17, "FORWARDED FROM"}
	laCancelled    = LegacyAction{19, "CANCELLED"}
	laComment      = LegacyAction{20, "ADDED COMMENT"}
	laReceived     = LegacyAction{21, "RECEIVED"}
	laRemoteNew    = LegacyAction{23, "REMOTE REQUEST RECEIVED"}
	laFwdRemote    = LegacyAction{25, "FWD TO REMOTE SERVICE"}
)

// Action is a workflow event a caller can request.
type Action string

const (
	Create          Action = "CREATE"
	Receive         Action = "RECEIVE"
	Schedule        Action = "SCHEDULE"
	Forward         Action = "FORWARD"
	Discontinue     Action = "DISCONTINUE"
	Cancel          Action = "CANCEL"
	Resubmit        Action = "RESUBMIT"
	AddNote         Action = "ADD_NOTE"
	SignNote        Action = "SIGN_NOTE"
	AdminComplete   Action = "ADMIN_COMPLETE"
	Comment         Action = "COMMENT"
	SigFindings     Action = "SIG_FINDINGS"
	AutoDiscontinue Action = "AUTO_DISCONTINUE"
)

// ActionDef describes one action: where it may start, who may take it, and
// which traceability rules back it.
type ActionDef struct {
	Action          Action       `json:"action"`
	Label           string       `json:"label"`
	Description     string       `json:"description"`
	From            []Status     `json:"from"`
	Roles           []Role       `json:"roles"`
	RequiresComment bool         `json:"requiresComment"`
	Legacy          LegacyAction `json:"legacy"`
	Rules           []string     `json:"rules"`
}

var (
	allStatuses = []Status{Pending, Active, Scheduled, PartialResults, Cancelled, Complete, Discontinued}
	humans      = []Role{RoleRequester, RoleServiceUser, RoleServiceAdmin}
	service     = []Role{RoleServiceUser, RoleServiceAdmin}
)

var actionDefs = []ActionDef{
	{Create, "Place consult", "Release a new consult order to a service.", nil,
		[]Role{RoleRequester}, false, laReleased, []string{"R-CREATE", "R-DUPLICATE"}},
	{Receive, "Receive", "The consulting service accepts the request.", []Status{Pending},
		service, false, laReceived, []string{"R-RECEIVE", "R-IFC-PLACER"}},
	{Schedule, "Schedule", "The service schedules the consult.", []Status{Pending, Active},
		service, false, laScheduled, []string{"R-SCHEDULE", "R-IFC-PLACER"}},
	{Forward, "Forward", "Send the consult to a different service; it becomes pending there.",
		[]Status{Pending, Active, Scheduled},
		service, false, laForwarded, []string{"R-FORWARD", "R-FORWARD-IFC", "R-PARTIAL-BLOCKS", "R-IFC-PLACER"}},
	{Discontinue, "Discontinue", "Stop the consult. A reason is required.",
		[]Status{Pending, Active, Scheduled},
		humans, true, laDiscontinued, []string{"R-DC-CANCEL", "R-PARTIAL-BLOCKS", "R-IFC-PLACER"}},
	{Cancel, "Cancel (deny)", "Deny the request and return it to the requester. A reason is required.",
		[]Status{Pending, Active, Scheduled},
		service, true, laCancelled, []string{"R-DC-CANCEL", "R-PARTIAL-BLOCKS", "R-IFC-PLACER"}},
	{Resubmit, "Edit & resubmit", "Correct a cancelled consult and send it back to the service.",
		[]Status{Cancelled},
		humans, false, laResubmitted, []string{"R-RESUBMIT"}},
	{AddNote, "Link note", "Link a result note. Unsigned gives partial results; signed completes.",
		[]Status{Pending, Active, Scheduled, PartialResults, Complete},
		service, false, laComplete, []string{"R-NOTE-STATUS", "R-NOTE-GUARD", "R-IFC-PLACER"}},
	{SignNote, "Sign note", "Sign a linked note; the consult becomes complete.",
		[]Status{Pending, Active, Scheduled, PartialResults, Complete},
		service, false, laComplete, []string{"R-NOTE-STATUS", "R-NOTE-GUARD", "R-IFC-PLACER"}},
	{AdminComplete, "Administrative complete", "Complete without a note. A comment is required.",
		[]Status{Pending, Active, Scheduled, PartialResults},
		[]Role{RoleServiceAdmin}, true, laComplete, []string{"R-ADMIN-COMPLETE", "R-IFC-PLACER"}},
	{Comment, "Add comment", "Add a comment; status does not change.", allStatuses,
		humans, true, laComment, []string{"R-COMMENT"}},
	{SigFindings, "Significant findings", "Record significant findings (Yes/No/Unknown); status does not change.",
		allStatuses, service, false, laSigFinding, []string{"R-SIG-FINDINGS", "R-IFC-PLACER"}},
	{AutoDiscontinue, "Auto-discontinue", "Overnight job: discontinue consults cancelled for 31+ days.",
		[]Status{Cancelled}, []Role{RoleSystem}, false, laDiscontinued, []string{"R-AUTO-DC", "R-CANC-QUIRK"}},
}

// CreateLegacyAction is the activity recorded when a consult is filed:
// a local order is released from CPRS; an IFC filler receives it remotely.
func CreateLegacyAction(ifcRole string) LegacyAction {
	if ifcRole == IFCFiller {
		return laRemoteNew
	}
	return laReleased
}

// ActionDefs returns every action definition.
func ActionDefs() []ActionDef { return append([]ActionDef(nil), actionDefs...) }

// Def returns the definition of a.
func (a Action) Def() (ActionDef, bool) {
	for _, d := range actionDefs {
		if d.Action == a {
			return d, true
		}
	}
	return ActionDef{}, false
}

// Urgencies recovered from GMRCHL7A URG(X), with the "act within" window used
// by the modern attention rule. Windows named in the urgency are taken from
// the name; the rest are assumptions (see rule R-URGENCY-WINDOW).
var urgencyWindows = []struct {
	Name   string
	Window time.Duration
}{
	{"STAT", 24 * time.Hour},
	{"EMERGENCY", 24 * time.Hour},
	{"TODAY", 24 * time.Hour},
	{"WITHIN 24 HOURS", 24 * time.Hour},
	{"WITHIN 48 HOURS", 48 * time.Hour},
	{"WITHIN 72 HOURS", 72 * time.Hour},
	{"WITHIN 1 WEEK", 7 * 24 * time.Hour},
	{"WITHIN 1 MONTH", 30 * 24 * time.Hour},
	{"NEXT AVAILABLE", 7 * 24 * time.Hour},
	{"ROUTINE", 7 * 24 * time.Hour},
}

// Urgencies returns the recognised urgency names.
func Urgencies() []string {
	out := make([]string, len(urgencyWindows))
	for i, u := range urgencyWindows {
		out[i] = u.Name
	}
	return out
}

// UrgencyWindow returns how long a consult of this urgency may wait to be
// received before it is flagged.
func UrgencyWindow(urgency string) (time.Duration, bool) {
	for _, u := range urgencyWindows {
		if u.Name == urgency {
			return u.Window, true
		}
	}
	return 0, false
}

// ServiceUsage mirrors REQUEST SERVICES (#123.5) piece 2 as used by GMRCASV.
type ServiceUsage string

const (
	UsageNormal   ServiceUsage = ""
	UsageGrouper  ServiceUsage = "GROUPER"  // 1: grouper only
	UsageTracking ServiceUsage = "TRACKING" // 2: tracking only
	UsageDisabled ServiceUsage = "DISABLED" // 9: disabled
)

// Service is a consulting service (file #123.5).
type Service struct {
	ID             int64        `json:"id"`
	Name           string       `json:"name"`
	Usage          ServiceUsage `json:"usage"`
	IFCRoutingSite string       `json:"ifcRoutingSite,omitempty"`
}

// IsIFC reports whether consults sent here go to another facility
// (123.5 "IFC" node with a routing facility).
func (s Service) IsIFC() bool { return s.IFCRoutingSite != "" }

// Orderable reports whether a consult may be sent or forwarded here.
func (s Service) Orderable() bool { return s.Usage != UsageDisabled && s.Usage != UsageGrouper }

// IFC roles from file #123 node 12 piece 5.
const (
	IFCPlacer = "P" // this facility requested the consult
	IFCFiller = "F" // this facility performs a consult requested elsewhere
)

// Note is a result note linked to a consult (node 50 results).
type Note struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Signed bool   `json:"signed"`
}

// Consult is the consult state the workflow rules read.
type Consult struct {
	ID                  int64
	Status              Status
	StatusChangedAt     time.Time
	StatusComment       string // comment on the activity that set Status
	DateOfRequest       time.Time
	ClinicallyIndicated *time.Time
	Urgency             string
	ToService           Service
	RequestingProvider  string
	Attention           string
	IFCRole             string
	IFCRemoteSite       string
	Notes               []Note
}

// UnsignedNotes returns the linked notes that are not yet signed.
func (c Consult) UnsignedNotes() []Note {
	var out []Note
	for _, n := range c.Notes {
		if !n.Signed {
			out = append(out, n)
		}
	}
	return out
}
