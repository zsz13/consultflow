package workflow

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

var (
	now        = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	cardiology = Service{ID: 4, Name: "CARDIOLOGY"}
	pulmonary  = Service{ID: 7, Name: "PULMONARY"}
	remoteSvc  = Service{ID: 1001, Name: "CARDIOLOGY - REMOTE", IFCRoutingSite: "DEMO VAMC NORTH"}
	disabled   = Service{ID: 40, Name: "EYEGLASS REQUEST", Usage: UsageDisabled}
	grouper    = Service{ID: 1, Name: "ALL SERVICES", Usage: UsageGrouper}
)

func consultIn(s Status) Consult {
	return Consult{
		ID: 1, Status: s, StatusChangedAt: now.Add(-2 * time.Hour), DateOfRequest: now.Add(-48 * time.Hour),
		Urgency: "ROUTINE", ToService: cardiology, RequestingProvider: "PROVIDER,DEMO", Attention: "CARDIO,FELLOW",
	}
}

func withUnsignedNote(c Consult) Consult {
	c.Notes = []Note{{ID: 9, Title: "Cardiology consult note", Signed: false}}
	return c
}

func mustDecide(t *testing.T, c Consult, req Request) Outcome {
	t.Helper()
	if req.Now.IsZero() {
		req.Now = now
	}
	out, err := Decide(c, req)
	if err != nil {
		t.Fatalf("%s from %s: unexpected error: %v", req.Action, c.Status, err)
	}
	return out
}

func ruleErr(t *testing.T, err error) *RuleError {
	t.Helper()
	var re *RuleError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RuleError, got %v", err)
	}
	return re
}

// The transition matrix below is written from the legacy routines, not from
// actionDefs, so that the two can disagree.
func TestTransitionMatrix(t *testing.T) {
	type spec struct {
		action Action
		role   Role
		from   []Status
		to     func(Status) Status
		req    Request
	}
	same := func(s Status) Status { return s }
	to := func(n Status) func(Status) Status { return func(Status) Status { return n } }
	specs := []spec{
		{Receive, RoleServiceUser, []Status{Pending}, to(Active), Request{}},
		{Schedule, RoleServiceUser, []Status{Pending, Active}, to(Scheduled), Request{}},
		{Forward, RoleServiceUser, []Status{Pending, Active, Scheduled}, to(Pending), Request{ToService: &pulmonary}},
		{Discontinue, RoleServiceUser, []Status{Pending, Active, Scheduled}, to(Discontinued), Request{Comment: "no longer needed"}},
		{Cancel, RoleServiceUser, []Status{Pending, Active, Scheduled}, to(Cancelled), Request{Comment: "needs echo first"}},
		{Resubmit, RoleRequester, []Status{Cancelled}, to(Pending), Request{}},
		{AddNote, RoleServiceUser, []Status{Pending, Active, Scheduled, PartialResults, Complete}, to(Complete), Request{NoteTitle: "Note", NoteSigned: true}},
		{AdminComplete, RoleServiceAdmin, []Status{Pending, Active, Scheduled, PartialResults}, to(Complete), Request{Comment: "seen in clinic"}},
		{Comment, RoleServiceUser, allStatuses, same, Request{Comment: "called patient"}},
		{SigFindings, RoleServiceUser, allStatuses, same, Request{SigFindings: "Y"}},
	}
	for _, sp := range specs {
		for _, from := range allStatuses {
			req := sp.req
			req.Action, req.Role, req.Now = sp.action, sp.role, now
			out, err := Decide(consultIn(from), req)
			allowed := slices.Contains(sp.from, from)
			if allowed && err != nil {
				t.Errorf("%s from %s: want allowed, got %v", sp.action, from, err)
				continue
			}
			if !allowed {
				if err == nil {
					t.Errorf("%s from %s: want rejected, got %s", sp.action, from, out.Next)
				} else if re := ruleErr(t, err); re.Kind != ErrConflict {
					t.Errorf("%s from %s: want CONFLICT, got %s (%s)", sp.action, from, re.Kind, re.Message)
				}
				continue
			}
			if want := sp.to(from); out.Next != want {
				t.Errorf("%s from %s: next = %s, want %s", sp.action, from, out.Next, want)
			}
			if out.Previous != from {
				t.Errorf("%s from %s: previous = %s", sp.action, from, out.Previous)
			}
		}
	}
}

func TestLegacyActionCodes(t *testing.T) {
	cases := []struct {
		c    Consult
		req  Request
		want int
	}{
		{consultIn(Pending), Request{Action: Receive, Role: RoleServiceUser}, 21},
		{consultIn(Active), Request{Action: Schedule, Role: RoleServiceUser}, 8},
		{consultIn(Active), Request{Action: Forward, Role: RoleServiceUser, ToService: &pulmonary}, 17},
		{consultIn(Active), Request{Action: Forward, Role: RoleServiceUser, ToService: &remoteSvc}, 25},
		{consultIn(Active), Request{Action: Discontinue, Role: RoleServiceUser, Comment: "x"}, 6},
		{consultIn(Active), Request{Action: Cancel, Role: RoleServiceUser, Comment: "x"}, 19},
		{consultIn(Cancelled), Request{Action: Resubmit, Role: RoleRequester}, 11},
		{consultIn(Scheduled), Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: "n"}, 9},
		{consultIn(Scheduled), Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: "n", NoteSigned: true}, 10},
		{consultIn(Complete), Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: "n", NoteSigned: true}, 10},
		{withUnsignedNote(consultIn(PartialResults)), Request{Action: SignNote, Role: RoleServiceUser, NoteID: 9}, 10},
		{consultIn(Active), Request{Action: AdminComplete, Role: RoleServiceAdmin, Comment: "x"}, 10},
		{consultIn(Active), Request{Action: Comment, Role: RoleRequester, Comment: "x"}, 20},
		{consultIn(Active), Request{Action: SigFindings, Role: RoleServiceUser, SigFindings: "N"}, 4},
	}
	for _, tc := range cases {
		if got := mustDecide(t, tc.c, tc.req).Legacy.IEN; got != tc.want {
			t.Errorf("%s from %s: legacy action %d, want %d", tc.req.Action, tc.c.Status, got, tc.want)
		}
	}
}

func TestCommentRequired(t *testing.T) {
	for _, a := range []Action{Discontinue, Cancel, AdminComplete, Comment} {
		role := RoleServiceUser
		if a == AdminComplete {
			role = RoleServiceAdmin
		}
		_, err := Decide(consultIn(Active), Request{Action: a, Role: role, Comment: "   \n", Now: now})
		if re := ruleErr(t, err); re.Kind != ErrInvalid {
			t.Errorf("%s with blank comment: kind %s, want INVALID", a, re.Kind)
		}
	}
	out := mustDecide(t, consultIn(Active), Request{Action: Discontinue, Role: RoleServiceUser, Comment: "  duplicate order  "})
	if out.Comment != "duplicate order" {
		t.Errorf("comment not trimmed: %q", out.Comment)
	}
}

func TestPartialResultsBlocksForwardDiscontinueCancel(t *testing.T) {
	c := withUnsignedNote(consultIn(PartialResults))
	for _, req := range []Request{
		{Action: Forward, Role: RoleServiceUser, ToService: &pulmonary},
		{Action: Discontinue, Role: RoleServiceUser, Comment: "x"},
		{Action: Cancel, Role: RoleServiceUser, Comment: "x"},
	} {
		req.Now = now
		_, err := Decide(c, req)
		if re := ruleErr(t, err); re.Rule != "R-PARTIAL-BLOCKS" {
			t.Errorf("%s: rule %q, want R-PARTIAL-BLOCKS (%s)", req.Action, re.Rule, re.Message)
		}
	}
	// An unsigned note on a completed consult still blocks forward (GMRCGUIA FR).
	done := withUnsignedNote(consultIn(Complete))
	_, err := Decide(done, Request{Action: Forward, Role: RoleServiceUser, ToService: &pulmonary, Now: now})
	if err == nil {
		t.Fatal("forward of completed consult should be rejected")
	}
}

func TestForwardRules(t *testing.T) {
	c := consultIn(Scheduled)
	c.Urgency = "WITHIN 1 WEEK"

	_, err := Decide(c, Request{Action: Forward, Role: RoleServiceUser, ToService: &cardiology, Now: now})
	if re := ruleErr(t, err); !strings.Contains(re.Message, "to itself") {
		t.Errorf("forward to same service: %s", re.Message)
	}
	for _, s := range []*Service{nil, &disabled, &grouper} {
		_, err := Decide(c, Request{Action: Forward, Role: RoleServiceUser, ToService: s, Now: now})
		if re := ruleErr(t, err); re.Kind != ErrInvalid {
			t.Errorf("forward to %v: kind %s", s, re.Kind)
		}
	}
	_, err = Decide(c, Request{Action: Forward, Role: RoleServiceUser, ToService: &pulmonary, Urgency: "SOMEDAY", Now: now})
	if re := ruleErr(t, err); re.Kind != ErrInvalid {
		t.Errorf("bad urgency: kind %s", re.Kind)
	}

	out := mustDecide(t, c, Request{Action: Forward, Role: RoleServiceUser, ToService: &pulmonary})
	if out.Patch.ToService.ID != pulmonary.ID || out.Next != Pending {
		t.Errorf("forward patch: %+v next %s", out.Patch, out.Next)
	}
	if out.Patch.Urgency != nil {
		t.Errorf("blank urgency must keep the current one, got %q", *out.Patch.Urgency)
	}
	if out.Patch.Attention == nil || *out.Patch.Attention != "" {
		t.Errorf("forward without attention must clear it (7///@)")
	}
	if out.Details["previousAttention"] != "CARDIO,FELLOW" || out.Details["forwardedFrom"] != "CARDIOLOGY" {
		t.Errorf("forward details: %v", out.Details)
	}

	remote := mustDecide(t, c, Request{Action: Forward, Role: RoleServiceUser, ToService: &remoteSvc})
	if *remote.Patch.IFCRole != IFCPlacer || *remote.Patch.IFCRemoteSite != "DEMO VAMC NORTH" {
		t.Errorf("forward to IFC service should make this site the placer: %+v", remote.Patch)
	}

	filler := c
	filler.IFCRole = IFCFiller
	_, err = Decide(filler, Request{Action: Forward, Role: RoleServiceUser, ToService: &remoteSvc, Now: now})
	if re := ruleErr(t, err); re.Rule != "R-FORWARD-IFC" {
		t.Errorf("IFC to IFC forward: rule %q", re.Rule)
	}
	mustDecide(t, filler, Request{Action: Forward, Role: RoleServiceUser, ToService: &pulmonary})
}

func TestIFCPlacerRestrictions(t *testing.T) {
	placer := func(s Status) Consult {
		c := consultIn(s)
		c.IFCRole, c.IFCRemoteSite = IFCPlacer, "DEMO VAMC NORTH"
		return c
	}
	for _, req := range []Request{
		{Action: Receive, Role: RoleServiceUser},
		{Action: Schedule, Role: RoleServiceUser},
		{Action: Forward, Role: RoleServiceUser, ToService: &pulmonary},
		{Action: Cancel, Role: RoleServiceUser, Comment: "x"},
		{Action: Discontinue, Role: RoleServiceUser, Comment: "x"},
		{Action: AddNote, Role: RoleServiceUser, NoteTitle: "n"},
		{Action: AdminComplete, Role: RoleServiceAdmin, Comment: "x"},
		{Action: SigFindings, Role: RoleServiceUser, SigFindings: "Y"},
	} {
		req.Now = now
		_, err := Decide(placer(Pending), req)
		if re := ruleErr(t, err); re.Rule != "R-IFC-PLACER" {
			t.Errorf("placer %s as %s: rule %q", req.Action, req.Role, re.Rule)
		}
	}
	// The placer check comes before status checks, so it wins over partial results.
	_, err := Decide(withUnsignedNote(placer(PartialResults)), Request{Action: SignNote, Role: RoleServiceUser, NoteID: 9, Now: now})
	if re := ruleErr(t, err); re.Rule != "R-IFC-PLACER" {
		t.Errorf("placer sign note: rule %q", re.Rule)
	}

	mustDecide(t, placer(Pending), Request{Action: Comment, Role: RoleRequester, Comment: "any update?"})
	// The ordering provider can still discontinue through CPRS (DC^GMRCHL7B)...
	out := mustDecide(t, placer(Scheduled), Request{Action: Discontinue, Role: RoleRequester, Comment: "no longer needed"})
	if out.Next != Discontinued {
		t.Errorf("requester DC of placed IFC: next %s", out.Next)
	}
	// ...but status guards still apply.
	_, err = Decide(withUnsignedNote(placer(PartialResults)), Request{Action: Discontinue, Role: RoleRequester, Comment: "x", Now: now})
	if re := ruleErr(t, err); re.Rule != "R-PARTIAL-BLOCKS" {
		t.Errorf("requester DC of placed IFC with partial results: rule %q", re.Rule)
	}
	cancelled := placer(Cancelled)
	mustDecide(t, cancelled, Request{Action: Resubmit, Role: RoleRequester})
	cancelled.StatusChangedAt = now.Add(-40 * day)
	mustDecide(t, cancelled, Request{Action: AutoDiscontinue, Role: RoleSystem})
}

func TestResubmitRouting(t *testing.T) {
	out := mustDecide(t, consultIn(Cancelled), Request{Action: Resubmit, Role: RoleRequester, ToService: &remoteSvc})
	if out.Patch.IFCRole == nil || *out.Patch.IFCRole != IFCPlacer || *out.Patch.IFCRemoteSite != "DEMO VAMC NORTH" {
		t.Errorf("resubmit to IFC service must make this site the placer: %+v", out.Patch)
	}
	placed := consultIn(Cancelled)
	placed.ToService, placed.IFCRole, placed.IFCRemoteSite = remoteSvc, IFCPlacer, "DEMO VAMC NORTH"
	out = mustDecide(t, placed, Request{Action: Resubmit, Role: RoleRequester, ToService: &pulmonary})
	if out.Patch.IFCRole == nil || *out.Patch.IFCRole != "" || *out.Patch.IFCRemoteSite != "" {
		t.Errorf("resubmit of placed IFC to a local service must make it local: %+v", out.Patch)
	}
	filler := consultIn(Cancelled)
	filler.IFCRole = IFCFiller
	_, err := Decide(filler, Request{Action: Resubmit, Role: RoleRequester, ToService: &remoteSvc, Now: now})
	if re := ruleErr(t, err); re.Rule != "R-FORWARD-IFC" {
		t.Errorf("resubmit IFC to IFC: rule %q", re.Rule)
	}
	for _, s := range []*Service{&disabled, &grouper} {
		_, err := Decide(consultIn(Cancelled), Request{Action: Resubmit, Role: RoleRequester, ToService: s, Now: now})
		if re := ruleErr(t, err); re.Kind != ErrInvalid {
			t.Errorf("resubmit to %s: kind %s", s.Name, re.Kind)
		}
	}
}

func TestRoles(t *testing.T) {
	cases := []struct {
		req  Request
		from Status
		ok   bool
	}{
		{Request{Action: Receive, Role: RoleRequester}, Pending, false},
		{Request{Action: Cancel, Role: RoleRequester, Comment: "x"}, Pending, false},
		{Request{Action: Discontinue, Role: RoleRequester, Comment: "x"}, Pending, true},
		{Request{Action: AdminComplete, Role: RoleServiceUser, Comment: "x"}, Active, false},
		{Request{Action: AdminComplete, Role: RoleServiceAdmin, Comment: "x"}, Active, true},
		{Request{Action: Resubmit, Role: RoleServiceUser}, Cancelled, true},
		{Request{Action: AutoDiscontinue, Role: RoleServiceAdmin}, Cancelled, false},
		{Request{Action: Receive, Role: RoleSystem}, Pending, false},
	}
	for _, tc := range cases {
		c := consultIn(tc.from)
		c.StatusChangedAt = now.Add(-40 * day)
		tc.req.Now = now
		_, err := Decide(c, tc.req)
		if tc.ok && err != nil {
			t.Errorf("%s as %s: %v", tc.req.Action, tc.req.Role, err)
		}
		if !tc.ok {
			if re := ruleErr(t, err); re.Kind != ErrForbidden {
				t.Errorf("%s as %s: kind %s, want FORBIDDEN", tc.req.Action, tc.req.Role, re.Kind)
			}
		}
	}
}

func TestNotes(t *testing.T) {
	// Unsigned note on a completed consult: INCOMPLETE RPT recorded, stays complete.
	out := mustDecide(t, consultIn(Complete), Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: "Addendum draft"})
	if out.Next != Complete || out.Legacy.IEN != 9 {
		t.Errorf("unsigned note on complete: next %s legacy %d", out.Next, out.Legacy.IEN)
	}
	// EVALACT: a signed note on a completed consult is 14 only when results are linked.
	adminDone := consultIn(Complete)
	if got := mustDecide(t, adminDone, Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: "n", NoteSigned: true}).Legacy.IEN; got != 10 {
		t.Errorf("signed note on completed consult without notes: action %d, want 10", got)
	}
	adminDone.Notes = []Note{{ID: 3, Title: "Earlier note", Signed: true}}
	if got := mustDecide(t, adminDone, Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: "n", NoteSigned: true}).Legacy.IEN; got != 14 {
		t.Errorf("signed note on completed consult with notes: action %d, want 14", got)
	}
	_, err := Decide(consultIn(Active), Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: " ", Now: now})
	if re := ruleErr(t, err); re.Kind != ErrInvalid {
		t.Errorf("blank note title: %s", re.Kind)
	}

	c := withUnsignedNote(consultIn(PartialResults))
	_, err = Decide(c, Request{Action: SignNote, Role: RoleServiceUser, NoteID: 404, Now: now})
	if re := ruleErr(t, err); re.Kind != ErrInvalid {
		t.Errorf("sign unknown note: %s", re.Kind)
	}
	signed := mustDecide(t, c, Request{Action: SignNote, Role: RoleServiceUser, NoteID: 9})
	if signed.Next != Complete || signed.Note.SignID != 9 {
		t.Errorf("sign note: %+v", signed)
	}
	if c.Notes[0].Signed {
		t.Error("Decide must not mutate the consult")
	}
	c.Notes[0].Signed = true
	_, err = Decide(c, Request{Action: SignNote, Role: RoleServiceUser, NoteID: 9, Now: now})
	if re := ruleErr(t, err); re.Kind != ErrConflict {
		t.Errorf("sign already-signed note: %s", re.Kind)
	}
	for _, s := range []Status{Discontinued, Cancelled} {
		_, err := Decide(consultIn(s), Request{Action: AddNote, Role: RoleServiceUser, NoteTitle: "n", Now: now})
		if re := ruleErr(t, err); re.Rule != "R-NOTE-STATUS" && re.Rule != "R-NOTE-GUARD" {
			t.Errorf("note on %s: rule %q", s, re.Rule)
		}
	}
}

func TestResubmitRecordsPreviousValues(t *testing.T) {
	c := consultIn(Cancelled)
	attn := "PULM,ATTENDING"
	out := mustDecide(t, c, Request{Action: Resubmit, Role: RoleRequester, ToService: &pulmonary, Urgency: "WITHIN 72 HOURS", Attention: &attn, Reason: "Added PFT results"})
	prev := out.Details["previousValues"].(map[string]string)
	if prev["To Service"] != "CARDIOLOGY" || prev["Urgency"] != "ROUTINE" || prev["Attention"] != "CARDIO,FELLOW" {
		t.Errorf("previous values: %v", prev)
	}
	if out.Next != Pending || *out.Patch.ReasonForRequest != "Added PFT results" {
		t.Errorf("resubmit outcome: %+v", out)
	}
	// Resubmitting unchanged is allowed and changes nothing but status.
	plain := mustDecide(t, c, Request{Action: Resubmit, Role: RoleRequester, ToService: &cardiology})
	if plain.Patch.ToService != nil || len(plain.Details["previousValues"].(map[string]string)) != 0 {
		t.Errorf("unchanged resubmit should not patch: %+v", plain.Patch)
	}
}

func TestAutoDiscontinue(t *testing.T) {
	c := consultIn(Cancelled)
	c.StatusChangedAt = now.Add(-30 * day)
	_, err := Decide(c, Request{Action: AutoDiscontinue, Role: RoleSystem, Now: now})
	if re := ruleErr(t, err); re.Kind != ErrConflict {
		t.Errorf("30 days: %s", re.Kind)
	}
	c.StatusChangedAt = now.Add(-31 * day)
	out := mustDecide(t, c, Request{Action: AutoDiscontinue, Role: RoleSystem})
	if out.Next != Discontinued || out.Legacy.IEN != 6 || !strings.HasPrefix(out.Comment, "ADC:") {
		t.Errorf("auto-DC outcome: %+v", out)
	}
	// Users still cannot discontinue a cancelled consult (see R-CANC-QUIRK).
	_, err = Decide(c, Request{Action: Discontinue, Role: RoleServiceUser, Comment: "x", Now: now})
	if re := ruleErr(t, err); re.Kind != ErrConflict {
		t.Errorf("user DC of cancelled: %s", re.Kind)
	}
}

func TestSigFindingsValues(t *testing.T) {
	_, err := Decide(consultIn(Active), Request{Action: SigFindings, Role: RoleServiceUser, SigFindings: "MAYBE", Now: now})
	if re := ruleErr(t, err); re.Kind != ErrInvalid {
		t.Errorf("bad sig finding: %s", re.Kind)
	}
	out := mustDecide(t, consultIn(Active), Request{Action: AdminComplete, Role: RoleServiceAdmin, Comment: "x", SigFindings: "Y"})
	if *out.Patch.SigFindings != "Y" || !strings.Contains(out.Summary, "with significant findings") {
		t.Errorf("admin complete with sig findings: %+v", out)
	}
}

func TestAvailableMatchesDecide(t *testing.T) {
	for _, s := range allStatuses {
		for _, role := range []Role{RoleRequester, RoleServiceUser, RoleServiceAdmin} {
			c := consultIn(s)
			for _, a := range Available(c, role, now) {
				_, err := Decide(c, Request{Action: a.Action, Role: role, Now: now,
					Comment: "x", ToService: &pulmonary, NoteTitle: "n", NoteID: 9, SigFindings: "U"})
				if a.Allowed && err != nil {
					t.Errorf("%s/%s/%s: Available says allowed, Decide says %v", s, role, a.Action, err)
				}
				if !a.Allowed && err == nil {
					t.Errorf("%s/%s/%s: Available says %q, Decide accepts", s, role, a.Action, a.Reason)
				}
			}
		}
	}
	for _, a := range Available(consultIn(Active), RoleServiceUser, now) {
		if a.Action == SignNote && a.Allowed {
			t.Error("SIGN_NOTE must be unavailable without an unsigned note")
		}
	}
}

func TestValidateNew(t *testing.T) {
	if err := ValidateNew(&cardiology, "STAT", "chest pain"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		svc     *Service
		urgency string
		reason  string
	}{
		{nil, "STAT", "x"}, {&disabled, "STAT", "x"}, {&cardiology, "SOON", "x"}, {&cardiology, "STAT", " "},
	} {
		if err := ValidateNew(tc.svc, tc.urgency, tc.reason); err == nil {
			t.Errorf("ValidateNew(%v, %q, %q) accepted", tc.svc, tc.urgency, tc.reason)
		}
	}
}
