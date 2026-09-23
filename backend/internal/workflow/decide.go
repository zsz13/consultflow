package workflow

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// AutoDiscontinueAfter is how long a consult stays cancelled before the
// overnight job discontinues it (GMRCCXDC, "after 31 days").
const AutoDiscontinueAfter = 31 * 24 * time.Hour

// ErrorKind classifies a rejected action; the API maps it to an HTTP status.
type ErrorKind string

const (
	ErrConflict  ErrorKind = "CONFLICT"  // current state does not allow the action
	ErrInvalid   ErrorKind = "INVALID"   // required input missing or malformed
	ErrForbidden ErrorKind = "FORBIDDEN" // role may not take the action
)

// RuleError is a rejected action, carrying the rule that rejected it.
type RuleError struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
	Rule    string    `json:"rule,omitempty"`
}

func (e *RuleError) Error() string { return e.Message }

func conflict(rule, format string, args ...any) *RuleError {
	return &RuleError{ErrConflict, fmt.Sprintf(format, args...), rule}
}

func invalid(rule, format string, args ...any) *RuleError {
	return &RuleError{ErrInvalid, fmt.Sprintf(format, args...), rule}
}

// Request is a requested action plus its inputs. The store resolves
// ToService before calling Decide.
type Request struct {
	Action      Action
	Role        Role
	Comment     string
	ToService   *Service // FORWARD (required), RESUBMIT (optional)
	Urgency     string   // FORWARD, RESUBMIT (optional)
	Attention   *string  // FORWARD: nil clears it, as legacy does; RESUBMIT: nil keeps it
	Reason      string   // RESUBMIT (optional)
	SigFindings string   // SIG_FINDINGS (required), ADMIN_COMPLETE (optional)
	NoteTitle   string   // ADD_NOTE
	NoteSigned  bool     // ADD_NOTE
	NoteID      int64    // SIGN_NOTE
	Now         time.Time
}

// Patch lists the consult fields an action changes. Nil means unchanged.
type Patch struct {
	ToService        *Service
	Urgency          *string
	Attention        *string // "" clears
	SigFindings      *string
	IFCRole          *string
	IFCRemoteSite    *string
	ReasonForRequest *string
}

// NoteOp is the note change an action makes.
type NoteOp struct {
	CreateTitle  string
	CreateSigned bool
	SignID       int64
}

// Outcome is an accepted action: the new status and everything to persist.
type Outcome struct {
	Previous Status
	Next     Status
	Legacy   LegacyAction
	Summary  string
	Comment  string
	Patch    Patch
	Note     *NoteOp
	Details  map[string]any
}

// StatusChanged reports whether the action moved the consult to a new status.
func (o Outcome) StatusChanged() bool { return o.Previous != o.Next }

// Decide validates req against the consult and returns the outcome, or a
// *RuleError naming the rule that rejects it. It never mutates c.
func Decide(c Consult, req Request) (Outcome, error) {
	def, ok := req.Action.Def()
	if !ok || req.Action == Create {
		return Outcome{}, invalid("", "unknown action %q", req.Action)
	}
	if err := guard(c, def, req.Role, req.Now); err != nil {
		return Outcome{}, err
	}
	comment := strings.TrimSpace(req.Comment)
	if def.RequiresComment && comment == "" {
		return Outcome{}, invalid(def.Rules[0], "A comment is required for %s.", strings.ToLower(def.Label))
	}
	out := Outcome{Previous: c.Status, Next: c.Status, Legacy: def.Legacy, Comment: comment, Details: map[string]any{}}
	switch req.Action {
	case Receive:
		out.Next = Active
		out.Summary = "Received into " + c.ToService.Name
	case Schedule:
		out.Next = Scheduled
		out.Summary = "Scheduled by " + c.ToService.Name
	case Forward:
		return decideForward(c, req, out)
	case Discontinue:
		out.Next = Discontinued
		out.Summary = "Discontinued"
	case Cancel:
		out.Next = Cancelled
		out.Summary = "Cancelled (denied) by " + c.ToService.Name + "; returned to requester"
	case Resubmit:
		return decideResubmit(c, req, out)
	case AddNote:
		return decideAddNote(c, req, out)
	case SignNote:
		return decideSignNote(c, req, out)
	case AdminComplete:
		if req.SigFindings != "" {
			if !validSigFindings(req.SigFindings) {
				return Outcome{}, invalid("R-SIG-FINDINGS", "Significant findings must be Y, N or U.")
			}
			out.Patch.SigFindings = &req.SigFindings
		}
		out.Next = Complete
		out.Summary = "Administratively completed" + sigFindingsSuffix(req.SigFindings)
	case Comment:
		out.Summary = "Comment added"
	case SigFindings:
		if !validSigFindings(req.SigFindings) {
			return Outcome{}, invalid("R-SIG-FINDINGS", "Significant findings must be Y, N or U.")
		}
		out.Patch.SigFindings = &req.SigFindings
		out.Summary = "Significant findings set to " + sigFindingsLabel(req.SigFindings)
	case AutoDiscontinue:
		out.Next = Discontinued
		out.Comment = fmt.Sprintf("ADC:Consult automatically discontinued %d days after cancellation", int(AutoDiscontinueAfter.Hours()/24))
		out.Summary = "Automatically discontinued after cancellation"
	}
	return out, nil
}

// guard applies the checks that depend only on consult state and role, in
// the order legacy applies them. Available() reuses it to explain why an
// action is unavailable before the user fills in any input.
func guard(c Consult, def ActionDef, role Role, now time.Time) *RuleError {
	if !slices.Contains(def.Roles, role) {
		return &RuleError{ErrForbidden, fmt.Sprintf("%s may not %s.", roleLabel(role), strings.ToLower(def.Label)), rolesRule(def)}
	}
	// The requesting facility of an inter-facility consult may only comment
	// or edit/resubmit; the filling facility owns the workflow. Exempt: the
	// ordering provider discontinuing the order through CPRS (DC^GMRCHL7B) and
	// the overnight job (DC^GMRCGUIA), neither of which checks the IFC role.
	if c.IFCRole == IFCPlacer && !placerMay(def.Action, role) {
		return conflict("R-IFC-PLACER", "The requesting facility may not take this action on an inter-facility consult; %s performs it.", remoteLabel(c))
	}
	if slices.Contains(def.From, c.Status) {
		switch def.Action {
		case Forward:
			if n := c.UnsignedNotes(); len(n) > 0 {
				return conflict("R-PARTIAL-BLOCKS", "Invalid action. This consult has an unsigned note (%q).", n[0].Title)
			}
		case AutoDiscontinue:
			if now.Sub(c.StatusChangedAt) < AutoDiscontinueAfter {
				return conflict("R-AUTO-DC", "Not eligible yet: cancelled consults are discontinued %d days after cancellation.", int(AutoDiscontinueAfter.Hours()/24))
			}
		}
		return nil
	}
	return statusConflict(c, def)
}

func placerMay(a Action, role Role) bool {
	switch a {
	case Comment, Resubmit, AutoDiscontinue:
		return true
	case Discontinue:
		return role == RoleRequester
	}
	return false
}

// statusConflict returns the legacy wording for an action started from a
// status it does not allow.
func statusConflict(c Consult, def ActionDef) *RuleError {
	rule := def.Rules[0]
	switch def.Action {
	case Forward:
		if c.Status == PartialResults {
			return conflict("R-PARTIAL-BLOCKS", "Invalid action. This consult has partial results.")
		}
		if c.Status == Cancelled {
			return conflict(rule, "NO ACTION POSSIBLE. This consult has already been cancelled.")
		}
		return conflict(rule, "NO ACTION POSSIBLE. This consult has already been completed or discontinued.")
	case Discontinue, Cancel:
		if c.Status == PartialResults {
			return conflict("R-PARTIAL-BLOCKS", "Action invalid. This consult has partial results. Sign or remove the associated results first.")
		}
		return conflict(rule, "This consult has already been %s.", pastTense(c.Status))
	case Receive:
		if closedOrCancelled(c.Status) {
			return conflict(rule, "This consult has already been %s. This action may not be taken now.", pastTense(c.Status))
		}
		return conflict(rule, "The receive action may only be taken when the consult has a pending status.")
	case Schedule:
		if closedOrCancelled(c.Status) {
			return conflict(rule, "This consult has already been %s. This action may not be taken now.", pastTense(c.Status))
		}
		return conflict(rule, "This consult may not be scheduled with the current status.")
	case Resubmit:
		return conflict(rule, "Only a cancelled consult can be edited and resubmitted; this consult is no longer editable.")
	case AddNote, SignNote:
		return conflict(rule, "This order has been %s. A note cannot be entered.", pastTense(c.Status))
	case AdminComplete:
		return conflict(rule, "This order has already been %s.", pastTense(c.Status))
	case AutoDiscontinue:
		return conflict(rule, "Only cancelled consults are automatically discontinued.")
	}
	return conflict(rule, "%s is not allowed from %s.", def.Label, c.Status)
}

func decideForward(c Consult, req Request, out Outcome) (Outcome, error) {
	to := req.ToService
	if err := checkTargetService(to); err != nil {
		return Outcome{}, err
	}
	if to.ID == c.ToService.ID {
		return Outcome{}, invalid("R-FORWARD", "The forwarding service cannot forward a consult to itself.")
	}
	if c.IFCRole != "" && to.IsIFC() {
		return Outcome{}, invalid("R-FORWARD-IFC", "You may not forward this inter-facility consult to another inter-facility consult service.")
	}
	if req.Urgency != "" {
		if _, ok := UrgencyWindow(req.Urgency); !ok {
			return Outcome{}, invalid("R-FORWARD", "Unknown urgency %q.", req.Urgency)
		}
		out.Patch.Urgency = &req.Urgency
	}
	// Legacy clears ATTENTION on forward unless a new one is given (7///@).
	attention := ""
	if req.Attention != nil {
		attention = strings.TrimSpace(*req.Attention)
	}
	out.Patch.Attention = &attention
	out.Patch.ToService = to
	out.Next = Pending
	out.Details["forwardedFrom"] = c.ToService.Name
	out.Details["forwardedTo"] = to.Name
	if c.Attention != "" {
		out.Details["previousAttention"] = c.Attention
	}
	out.Summary = fmt.Sprintf("Forwarded from %s to %s", c.ToService.Name, to.Name)
	if to.IsIFC() {
		role, site := IFCPlacer, to.IFCRoutingSite
		out.Patch.IFCRole, out.Patch.IFCRemoteSite = &role, &site
		out.Legacy = laFwdRemote
		out.Details["remoteSite"] = site
		out.Summary = fmt.Sprintf("Forwarded to remote service %s at %s", to.Name, site)
	}
	return out, nil
}

func decideResubmit(c Consult, req Request, out Outcome) (Outcome, error) {
	previous := map[string]string{}
	if to := req.ToService; to != nil && to.ID != c.ToService.ID {
		if err := checkTargetService(to); err != nil {
			return Outcome{}, err
		}
		if c.IFCRole != "" && to.IsIFC() {
			return Outcome{}, invalid("R-FORWARD-IFC", "You may not send this inter-facility consult to another inter-facility consult service.")
		}
		out.Patch.ToService = to
		previous["To Service"] = c.ToService.Name
		// Route like a new order: an IFC service makes this site the placer;
		// moving a placed IFC back to a local service makes it local again.
		if to.IsIFC() {
			role, site := IFCPlacer, to.IFCRoutingSite
			out.Patch.IFCRole, out.Patch.IFCRemoteSite = &role, &site
		} else if c.IFCRole == IFCPlacer {
			none := ""
			out.Patch.IFCRole, out.Patch.IFCRemoteSite = &none, &none
		}
	}
	if req.Urgency != "" && req.Urgency != c.Urgency {
		if _, ok := UrgencyWindow(req.Urgency); !ok {
			return Outcome{}, invalid("R-RESUBMIT", "Unknown urgency %q.", req.Urgency)
		}
		out.Patch.Urgency = &req.Urgency
		previous["Urgency"] = c.Urgency
	}
	if req.Attention != nil && strings.TrimSpace(*req.Attention) != c.Attention {
		a := strings.TrimSpace(*req.Attention)
		out.Patch.Attention = &a
		previous["Attention"] = c.Attention
	}
	if r := strings.TrimSpace(req.Reason); r != "" {
		out.Patch.ReasonForRequest = &r
		previous["Reason for Request"] = "(edited)"
	}
	out.Next = Pending
	out.Details["previousValues"] = previous
	out.Summary = "Edited and resubmitted to " + c.ToService.Name
	if out.Patch.ToService != nil {
		out.Summary = "Edited and resubmitted to " + out.Patch.ToService.Name
	}
	return out, nil
}

func decideAddNote(c Consult, req Request, out Outcome) (Outcome, error) {
	title := strings.TrimSpace(req.NoteTitle)
	if title == "" {
		return Outcome{}, invalid("R-NOTE-STATUS", "A note title is required.")
	}
	out.Note = &NoteOp{CreateTitle: title, CreateSigned: req.NoteSigned}
	out.Details["noteTitle"] = title
	if !req.NoteSigned {
		// INCOMPLETE RPT; a completed consult stays complete (GMRCTIU1).
		out.Legacy = laIncomplete
		if c.Status != Complete {
			out.Next = PartialResults
		}
		out.Summary = fmt.Sprintf("Unsigned note %q linked", title)
		return out, nil
	}
	out.Next = Complete
	out.Summary = fmt.Sprintf("Signed note %q linked; consult completed", title)
	// EVALACT^GMRCTIU1: NEW NOTE ADDED only when a completed consult already
	// has linked results; otherwise (e.g. after admin complete) it is 10.
	if c.Status == Complete && len(c.Notes) > 0 {
		out.Legacy = laNewNote
		out.Summary = fmt.Sprintf("New signed note %q added to completed consult", title)
	}
	return out, nil
}

func decideSignNote(c Consult, req Request, out Outcome) (Outcome, error) {
	var note *Note
	for i := range c.Notes {
		if c.Notes[i].ID == req.NoteID {
			note = &c.Notes[i]
		}
	}
	if note == nil {
		return Outcome{}, invalid("R-NOTE-STATUS", "Note %d is not linked to this consult.", req.NoteID)
	}
	if note.Signed {
		return Outcome{}, conflict("R-NOTE-STATUS", "Note %q is already signed.", note.Title)
	}
	out.Note = &NoteOp{SignID: note.ID}
	out.Details["noteTitle"] = note.Title
	out.Next = Complete
	out.Summary = fmt.Sprintf("Note %q signed; consult completed", note.Title)
	if c.Status == Complete {
		out.Legacy = laNewNote
		out.Summary = fmt.Sprintf("Note %q signed", note.Title)
	}
	return out, nil
}

func checkTargetService(s *Service) *RuleError {
	if s == nil {
		return invalid("R-FORWARD", "A destination service is required.")
	}
	if s.Usage == UsageDisabled {
		return invalid("R-FORWARD", "You have selected a disabled service.")
	}
	if !s.Orderable() {
		return invalid("R-FORWARD", "%s is a grouper and cannot receive consults.", s.Name)
	}
	return nil
}

// ValidateNew checks a new consult order before it is filed.
func ValidateNew(to *Service, urgency, reason string) error {
	if err := checkTargetService(to); err != nil {
		err.Rule = "R-CREATE"
		return err
	}
	if _, ok := UrgencyWindow(urgency); !ok {
		return invalid("R-CREATE", "Unknown urgency %q.", urgency)
	}
	if strings.TrimSpace(reason) == "" {
		return invalid("R-CREATE", "A reason for request is required.")
	}
	return nil
}

// Availability says whether a role can take an action right now, and why not.
type Availability struct {
	Action          Action `json:"action"`
	Label           string `json:"label"`
	Allowed         bool   `json:"allowed"`
	Reason          string `json:"reason,omitempty"`
	Rule            string `json:"rule,omitempty"`
	RequiresComment bool   `json:"requiresComment"`
}

// Available evaluates every user-facing action for role against c.
func Available(c Consult, role Role, now time.Time) []Availability {
	var out []Availability
	for _, def := range actionDefs {
		if def.Action == Create || def.Action == AutoDiscontinue {
			continue
		}
		a := Availability{Action: def.Action, Label: def.Label, Allowed: true, RequiresComment: def.RequiresComment}
		if err := guard(c, def, role, now); err != nil {
			a.Allowed, a.Reason, a.Rule = false, err.Message, err.Rule
		} else if def.Action == SignNote && len(c.UnsignedNotes()) == 0 {
			a.Allowed, a.Reason, a.Rule = false, "There is no unsigned note to sign.", "R-NOTE-STATUS"
		}
		out = append(out, a)
	}
	return out
}

func closedOrCancelled(s Status) bool { return s.Closed() || s == Cancelled }

func pastTense(s Status) string {
	switch s {
	case Complete:
		return "completed"
	case Discontinued:
		return "discontinued"
	case Cancelled:
		return "cancelled"
	}
	info, _ := s.Info()
	return strings.ToLower(info.Label)
}

func validSigFindings(v string) bool { return v == "Y" || v == "N" || v == "U" }

func sigFindingsLabel(v string) string {
	switch v {
	case "Y":
		return "Yes"
	case "N":
		return "No"
	}
	return "Unknown"
}

func sigFindingsSuffix(v string) string {
	switch v {
	case "Y":
		return " with significant findings"
	case "N":
		return " with no significant findings"
	}
	return ""
}

func roleLabel(r Role) string {
	switch r {
	case RoleRequester:
		return "The requesting provider"
	case RoleServiceUser:
		return "A service user"
	case RoleServiceAdmin:
		return "A service administrator"
	case RoleSystem:
		return "The system"
	}
	return fmt.Sprintf("Role %q", r)
}

func rolesRule(def ActionDef) string {
	if def.Action == AutoDiscontinue {
		return "R-AUTO-DC"
	}
	if def.Action == AdminComplete {
		return "R-ADMIN-COMPLETE"
	}
	return "R-ROLES"
}

func remoteLabel(c Consult) string {
	if c.IFCRemoteSite != "" {
		return "the filling facility (" + c.IFCRemoteSite + ")"
	}
	return "the filling facility"
}
