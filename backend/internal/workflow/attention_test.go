package workflow

import (
	"slices"
	"testing"
	"time"
)

func codes(flags []Flag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = f.Code
	}
	return out
}

func TestAssess(t *testing.T) {
	cid := now.Add(-45 * day)
	cases := []struct {
		name  string
		c     func() Consult
		dups  []int64
		want  []string
		first Severity
	}{
		{"fresh pending routine", func() Consult { return consultIn(Pending) }, nil, nil, ""},
		{"stat pending 30h", func() Consult {
			c := consultIn(Pending)
			c.Urgency, c.StatusChangedAt = "STAT", now.Add(-30*time.Hour)
			return c
		}, nil, []string{"NOT_RECEIVED"}, Critical},
		{"routine pending 8 days", func() Consult {
			c := consultIn(Pending)
			c.StatusChangedAt = now.Add(-8 * day)
			return c
		}, nil, []string{"NOT_RECEIVED"}, Warning},
		{"active is not a receipt problem", func() Consult {
			c := consultIn(Active)
			c.Urgency, c.StatusChangedAt = "STAT", now.Add(-30*time.Hour)
			return c
		}, nil, nil, ""},
		{"partial results", func() Consult { return withUnsignedNote(consultIn(PartialResults)) }, nil, []string{"UNSIGNED_NOTE"}, Warning},
		{"cancelled 10 days", func() Consult {
			c := consultIn(Cancelled)
			c.StatusChangedAt = now.Add(-10 * day)
			return c
		}, nil, []string{"AWAITING_RESUBMIT"}, Warning},
		{"cancelled 26 days is critical", func() Consult {
			c := consultIn(Cancelled)
			c.StatusChangedAt = now.Add(-26 * day)
			return c
		}, nil, []string{"AWAITING_RESUBMIT"}, Critical},
		{"scheduled past 30 days from CID", func() Consult {
			c := consultIn(Scheduled)
			c.ClinicallyIndicated = &cid
			return c
		}, nil, []string{"PAST_30_DAYS"}, Warning},
		{"active 70 days after request", func() Consult {
			c := consultIn(Active)
			c.DateOfRequest = now.Add(-70 * day)
			return c
		}, nil, []string{"PAST_60_DAYS"}, Critical},
		{"completed old consult is fine", func() Consult {
			c := consultIn(Complete)
			c.DateOfRequest = now.Add(-200 * day)
			return c
		}, nil, nil, ""},
		{"duplicate", func() Consult { return consultIn(Active) }, []int64{12}, []string{"POSSIBLE_DUPLICATE"}, Warning},
		{"critical sorts first", func() Consult {
			c := consultIn(Pending)
			c.Urgency, c.StatusChangedAt, c.DateOfRequest = "STAT", now.Add(-2*day), now.Add(-35*day)
			return c
		}, []int64{3}, []string{"NOT_RECEIVED", "PAST_30_DAYS", "POSSIBLE_DUPLICATE"}, Critical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flags := Assess(tc.c(), tc.dups, now)
			if got := codes(flags); !slices.Equal(got, tc.want) {
				t.Fatalf("flags = %v, want %v", got, tc.want)
			}
			if len(flags) > 0 && flags[0].Severity != tc.first {
				t.Errorf("first severity = %s, want %s", flags[0].Severity, tc.first)
			}
			for _, f := range flags {
				if _, ok := RuleByID(f.Rule); !ok {
					t.Errorf("flag %s cites unknown rule %s", f.Code, f.Rule)
				}
			}
		})
	}
}

func TestIsDuplicateCandidate(t *testing.T) {
	if !IsDuplicateCandidate(Scheduled, now.Add(-300*day), now) {
		t.Error("scheduled within a year is a candidate")
	}
	if IsDuplicateCandidate(Scheduled, now.Add(-400*day), now) {
		t.Error("older than 365 days is not a candidate")
	}
	for _, s := range []Status{PartialResults, Complete, Discontinued, Cancelled} {
		if IsDuplicateCandidate(s, now, now) {
			t.Errorf("%s is not a GMRCDPCK status", s)
		}
	}
}

// Every next step Explain suggests must be accepted by the state machine for
// the role it names.
func TestExplainNextStepsAreAllowed(t *testing.T) {
	for _, s := range allStatuses {
		for _, placer := range []bool{false, true} {
			c := consultIn(s)
			if s == PartialResults {
				c = withUnsignedNote(c)
			}
			if placer {
				c.IFCRole, c.IFCRemoteSite = IFCPlacer, "DEMO VAMC NORTH"
			}
			e := Explain(c, now)
			if e.Headline == "" || e.Owner == "" {
				t.Errorf("%s placer=%v: empty explanation", s, placer)
			}
			for _, step := range e.NextSteps {
				if step.Action == "" {
					continue
				}
				_, err := Decide(c, Request{Action: step.Action, Role: step.Role, Now: now,
					Comment: "x", ToService: &pulmonary, NoteTitle: "n", NoteID: 9, SigFindings: "U"})
				if err != nil {
					t.Errorf("%s placer=%v: suggested %s as %s is rejected: %v", s, placer, step.Action, step.Role, err)
				}
			}
		}
	}
}

func TestExplainUsesStatusComment(t *testing.T) {
	c := consultIn(Cancelled)
	c.StatusComment = "Please attach recent echo"
	c.StatusChangedAt = now.Add(-5 * day)
	e := Explain(c, now)
	if !e.Blocked || !slices.Contains(e.Reasons, "Cancellation reason: Please attach recent echo") {
		t.Errorf("cancelled explanation: %+v", e)
	}
}

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Hour: "less than an hour", 20 * time.Minute: "less than an hour", time.Hour: "1 hour", 5 * time.Hour: "5 hours", 30 * time.Hour: "30 hours", 50 * time.Hour: "2 days",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
