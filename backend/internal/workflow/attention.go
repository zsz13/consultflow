package workflow

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Severity orders attention flags.
type Severity string

const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
)

// Flag is one reason a consult needs attention.
type Flag struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Rule     string   `json:"rule"`
}

const day = 24 * time.Hour

// Completion windows from the consult performance monitor (GMRCSTL8 CHKRNG).
const (
	completionWindow     = 30 * day
	completionWindowLate = 60 * day
	duplicateLookback    = 365 * day // GMRCDPCK
)

// Assess returns the deterministic attention flags for c, most severe first.
// duplicates are other open consults GMRCDPCK would treat as duplicates.
func Assess(c Consult, duplicates []int64, now time.Time) []Flag {
	var flags []Flag
	add := func(f Flag) { flags = append(flags, f) }

	if c.Status == PartialResults {
		title := "an unsigned note"
		if n := c.UnsignedNotes(); len(n) > 0 {
			title = fmt.Sprintf("unsigned note %q", n[0].Title)
		}
		add(Flag{"UNSIGNED_NOTE", Warning, "Unsigned note blocks completion",
			fmt.Sprintf("Partial results: %s is linked. It cannot be forwarded, discontinued or cancelled until the note is signed.", title),
			"R-PARTIAL-BLOCKS"})
	}

	if c.Status == Cancelled {
		left := AutoDiscontinueAfter - now.Sub(c.StatusChangedAt)
		sev := Warning
		if left <= 7*day {
			sev = Critical
		}
		f := Flag{"AWAITING_RESUBMIT", sev, "Returned to requester",
			fmt.Sprintf("Cancelled by the service. Edit and resubmit within %s or it will be discontinued automatically.", humanDuration(left)),
			"R-AUTO-DC"}
		if left <= 0 {
			f.Title = "Due for automatic discontinue"
			f.Detail = "Cancelled 31 or more days ago and not resubmitted; the overnight job will discontinue it."
		}
		add(f)
	}

	if c.Status == Pending {
		if window, ok := UrgencyWindow(c.Urgency); ok {
			if waited := now.Sub(c.StatusChangedAt); waited > window {
				sev := Warning
				if window <= day {
					sev = Critical
				}
				who := c.ToService.Name
				if c.IFCRole == IFCPlacer {
					who = remoteLabel(c)
				}
				add(Flag{"NOT_RECEIVED", sev, fmt.Sprintf("Not received within %s window", strings.ToLower(c.Urgency)),
					fmt.Sprintf("Pending for %s; %s has not received it (window %s).", humanDuration(waited), who, humanDuration(window)),
					"R-URGENCY-WINDOW"})
			}
		}
	}

	if c.Status.Open() {
		ref, basis := c.DateOfRequest, "date of request"
		if c.ClinicallyIndicated != nil {
			ref, basis = *c.ClinicallyIndicated, "clinically indicated date"
		}
		switch age := now.Sub(ref); {
		case age > completionWindowLate:
			add(Flag{"PAST_60_DAYS", Critical, "Open beyond 60-day completion window",
				fmt.Sprintf("Still open %s after the %s.", humanDuration(age), basis), "R-COMPLETION-WINDOW"})
		case age > completionWindow:
			add(Flag{"PAST_30_DAYS", Warning, "Open beyond 30-day completion window",
				fmt.Sprintf("Still open %s after the %s.", humanDuration(age), basis), "R-COMPLETION-WINDOW"})
		}
	}

	if len(duplicates) > 0 && c.Status.Open() {
		ids := make([]string, len(duplicates))
		for i, id := range duplicates {
			ids[i] = fmt.Sprintf("#%d", id)
		}
		add(Flag{"POSSIBLE_DUPLICATE", Warning, "Possible duplicate order",
			"Same patient, service and procedure is also open: " + strings.Join(ids, ", ") + ".", "R-DUPLICATE"})
	}

	slices.SortStableFunc(flags, func(a, b Flag) int { return severityRank(a.Severity) - severityRank(b.Severity) })
	return flags
}

func severityRank(s Severity) int {
	if s == Critical {
		return 0
	}
	return 1
}

// DuplicateKey is what GMRCDPCK compares: patient, destination service and
// procedure (empty for a plain consult).
type DuplicateKey struct {
	PatientRef string
	ServiceID  int64
	Procedure  string
}

// IsDuplicateCandidate reports whether a consult in status s, requested at
// requested, can be a duplicate at now: pending, active or scheduled within
// the last 365 days (GMRCDPCK).
func IsDuplicateCandidate(s Status, requested, now time.Time) bool {
	return (s == Pending || s == Active || s == Scheduled) && now.Sub(requested) <= duplicateLookback
}

// NextStep is a deterministic suggestion tied to an action.
type NextStep struct {
	Action Action `json:"action,omitempty"`
	Role   Role   `json:"role,omitempty"`
	Text   string `json:"text"`
}

// Explanation answers "what is this consult waiting for, and why".
type Explanation struct {
	Headline  string     `json:"headline"`
	Owner     string     `json:"owner"`
	Blocked   bool       `json:"blocked"`
	Reasons   []string   `json:"reasons"`
	NextSteps []NextStep `json:"nextSteps"`
}

// Explain derives the waiting-on / blocked explanation from state alone.
func Explain(c Consult, now time.Time) Explanation {
	svc := c.ToService.Name
	since := humanDuration(now.Sub(c.StatusChangedAt))
	if c.IFCRole == IFCPlacer && !c.Status.Closed() {
		e := Explanation{
			Headline: "Waiting on " + remoteLabel(c),
			Owner:    "Remote facility",
			Reasons: []string{
				"This facility placed the inter-facility consult. Receive, schedule, results and completion happen at the filling facility and arrive here as updates.",
			},
			NextSteps: []NextStep{{Comment, RoleRequester, "Add a comment; it is sent to the remote facility."}},
		}
		if c.Status == Pending || c.Status == Active || c.Status == Scheduled {
			e.NextSteps = append(e.NextSteps, NextStep{Discontinue, RoleRequester, "Or discontinue the order if it is no longer needed."})
		}
		if c.Status == Cancelled {
			e.Headline = "Denied by " + remoteLabel(c) + "; awaiting edit & resubmit"
			e.Owner = "Requesting provider"
			e.Reasons = append(e.Reasons, cancelReason(c))
			e.NextSteps = []NextStep{
				{Resubmit, RoleRequester, "Edit and resubmit the request."},
				{Comment, RoleRequester, "Add a comment; it is sent to the remote facility."},
			}
		}
		return e
	}
	switch c.Status {
	case Pending:
		return Explanation{
			Headline: "Waiting for " + svc + " to receive the consult",
			Owner:    svc,
			Reasons:  []string{"Pending for " + since + ". Nothing happens until the consulting service acts on it."},
			NextSteps: []NextStep{
				{Receive, RoleServiceUser, "Receive it into " + svc + "."},
				{Schedule, RoleServiceUser, "Or schedule it directly."},
				{Forward, RoleServiceUser, "Forward it if another service should handle it."},
				{Cancel, RoleServiceUser, "Cancel (deny) with a reason to return it to the requester."},
			},
		}
	case Active:
		return Explanation{
			Headline: "Received by " + svc + "; not yet scheduled",
			Owner:    svc,
			Reasons:  []string{"Received " + since + " ago. No appointment or result is recorded yet."},
			NextSteps: []NextStep{
				{Schedule, RoleServiceUser, "Schedule the consult."},
				{AddNote, RoleServiceUser, "Link a consult note (signed completes the consult)."},
				{Forward, RoleServiceUser, "Forward it if another service should handle it."},
			},
		}
	case Scheduled:
		return Explanation{
			Headline: "Scheduled; waiting for results",
			Owner:    svc,
			Reasons:  []string{"Scheduled " + since + " ago. The consult completes when a signed note is linked."},
			NextSteps: []NextStep{
				{AddNote, RoleServiceUser, "Link the consult note once written."},
				{AdminComplete, RoleServiceAdmin, "Administratively complete it if no note will be written."},
			},
		}
	case PartialResults:
		title := "an unsigned note"
		if n := c.UnsignedNotes(); len(n) > 0 {
			title = fmt.Sprintf("the unsigned note %q", n[0].Title)
		}
		return Explanation{
			Headline: "Blocked by " + title,
			Owner:    svc,
			Blocked:  true,
			Reasons: []string{
				"An incomplete report (unsigned note) sets the consult to partial results.",
				"While partial results exist it cannot be forwarded, discontinued or cancelled.",
			},
			NextSteps: []NextStep{
				{SignNote, RoleServiceUser, "Sign the note; the consult then completes."},
				{AdminComplete, RoleServiceAdmin, "Or administratively complete it."},
			},
		}
	case Cancelled:
		left := AutoDiscontinueAfter - now.Sub(c.StatusChangedAt)
		reasons := []string{cancelReason(c)}
		if left > 0 {
			reasons = append(reasons, "It will be discontinued automatically in "+humanDuration(left)+" if not resubmitted.")
		} else {
			reasons = append(reasons, "It is past the 31-day limit and is due for automatic discontinue.")
		}
		return Explanation{
			Headline:  "Returned to " + requester(c) + " for edit & resubmit",
			Owner:     "Requesting provider",
			Blocked:   true,
			Reasons:   reasons,
			NextSteps: []NextStep{{Resubmit, RoleRequester, "Edit the request and resubmit it to " + svc + "."}},
		}
	case Complete:
		return Explanation{
			Headline:  "Completed",
			Owner:     "None",
			Reasons:   []string{"The consult is complete. Comments, significant findings and additional notes can still be recorded."},
			NextSteps: []NextStep{{Comment, RoleServiceUser, "Add a comment if follow-up is needed."}},
		}
	case Discontinued:
		reason := "The consult was discontinued."
		if c.StatusComment != "" {
			reason = "Discontinued: " + c.StatusComment
		}
		return Explanation{Headline: "Discontinued", Owner: "None", Reasons: []string{reason, "No further workflow actions are possible."}}
	}
	return Explanation{Headline: string(c.Status), Owner: "Unknown"}
}

func cancelReason(c Consult) string {
	if c.StatusComment != "" {
		return "Cancellation reason: " + c.StatusComment
	}
	return "The service cancelled (denied) the request."
}

func requester(c Consult) string {
	if c.RequestingProvider != "" {
		return c.RequestingProvider
	}
	return "the requester"
}

// humanDuration renders a duration as days or hours, never negative.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= 2*day {
		return fmt.Sprintf("%d days", int(d/day))
	}
	h := int(d / time.Hour)
	if h == 0 {
		return "less than an hour"
	}
	if h == 1 {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", h)
}
