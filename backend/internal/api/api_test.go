package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"consultflow/internal/api"
	"consultflow/internal/store"
	"consultflow/internal/workflow"
)

// Integration tests run against a real PostgreSQL. Set TEST_DATABASE_URL,
// e.g. postgres://consultflow:consultflow@localhost:55432/consultflow_test?sslmode=disable
// (the database is created if missing). Without it the tests are skipped.

var now = time.Now().UTC().Truncate(time.Second)

type env struct {
	t   *testing.T
	srv *httptest.Server
	db  *pgx.Conn
	st  *store.Store
}

func setup(t *testing.T) *env {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	ensureDatabase(t, dbURL)
	st, err := store.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Reset(ctx, now); err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(ctx) })
	srv := httptest.NewServer(api.New(st, func() time.Time { return now }))
	t.Cleanup(srv.Close)
	return &env{t, srv, conn, st}
}

func ensureDatabase(t *testing.T, dbURL string) {
	u, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	admin := *u
	admin.Path = "/postgres"
	conn, err := pgx.Connect(context.Background(), admin.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var exists bool
	if err := conn.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := conn.Exec(context.Background(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *env) do(method, path string, body any, into any) int {
	e.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	if into != nil {
		if err := json.NewDecoder(res.Body).Decode(into); err != nil {
			e.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
	return res.StatusCode
}

type consult struct {
	ID               int64                   `json:"id"`
	StatusChangedAt  time.Time               `json:"statusChangedAt"`
	Procedure        string                  `json:"procedure"`
	PatientName      string                  `json:"patientName"`
	Status           workflow.Status         `json:"status"`
	LastAction       string                  `json:"lastAction"`
	Flags            []workflow.Flag         `json:"flags"`
	Duplicates       []int64                 `json:"duplicates"`
	Notes            []store.NoteRecord      `json:"notes"`
	Explanation      workflow.Explanation    `json:"explanation"`
	AvailableActions []workflow.Availability `json:"availableActions"`
	ToService        workflow.Service        `json:"toService"`
	Attention        string                  `json:"attention"`
}

type apiError struct {
	Error struct {
		Kind       string  `json:"kind"`
		Message    string  `json:"message"`
		Rule       string  `json:"rule"`
		Duplicates []int64 `json:"duplicates"`
	} `json:"error"`
}

func (e *env) consultByPatient(name string) consult {
	e.t.Helper()
	var list []consult
	e.do("GET", "/api/consults?q="+url.QueryEscape(name), nil, &list)
	for _, c := range list {
		if c.PatientName == name {
			return c
		}
	}
	e.t.Fatalf("no consult for %s", name)
	return consult{}
}

func (e *env) act(id int64, body map[string]any, into any) int {
	return e.do("POST", fmt.Sprintf("/api/consults/%d/actions", id), body, into)
}

func TestSeededDashboard(t *testing.T) {
	e := setup(t)
	var d struct {
		Totals       map[string]int `json:"totals"`
		StatusCounts []struct {
			Status workflow.Status `json:"status"`
			Count  int             `json:"count"`
		} `json:"statusCounts"`
		Attention []consult `json:"attention"`
	}
	if code := e.do("GET", "/api/dashboard", nil, &d); code != 200 {
		t.Fatalf("dashboard: %d", code)
	}
	want := map[workflow.Status]int{
		workflow.Pending: 6, workflow.Active: 3, workflow.Scheduled: 2, workflow.PartialResults: 1,
		workflow.Cancelled: 2, workflow.Complete: 2, workflow.Discontinued: 1,
	}
	for _, sc := range d.StatusCounts {
		if sc.Count != want[sc.Status] {
			t.Errorf("%s count = %d, want %d", sc.Status, sc.Count, want[sc.Status])
		}
	}
	if d.Totals["total"] != 17 || d.Totals["active"] != 12 {
		t.Errorf("totals: %v", d.Totals)
	}
	var names []string
	for _, c := range d.Attention {
		names = append(names, c.PatientName)
	}
	slices.Sort(names)
	wantNames := []string{"ZZTEST,ALPHA", "ZZTEST,CHARLIE", "ZZTEST,CHARLIE", "ZZTEST,DELTA", "ZZTEST,ECHO",
		"ZZTEST,FOXTROT", "ZZTEST,GOLF", "ZZTEST,KILO", "ZZTEST,MIKE", "ZZTEST,PAPA"}
	if !slices.Equal(names, wantNames) {
		t.Errorf("attention = %v\nwant       %v", names, wantNames)
	}
	if d.Attention[0].Flags[0].Severity != workflow.Critical {
		t.Errorf("attention list must start with a critical flag, got %+v", d.Attention[0].Flags[0])
	}
}

func TestActionFlowUpdatesStateAndAudit(t *testing.T) {
	e := setup(t)
	c := e.consultByPatient("ZZTEST,ALPHA")
	if c.Status != workflow.Pending || len(c.Flags) == 0 || c.Flags[0].Code != "NOT_RECEIVED" {
		t.Fatalf("seeded STAT consult: %+v", c)
	}
	user := map[string]any{"role": "SERVICE_USER", "actor": "ZZCARDIO,DANA"}
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{}
		for k, v := range user {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	var res struct {
		Consult consult `json:"consult"`
		Outcome struct {
			PreviousStatus workflow.Status       `json:"previousStatus"`
			NewStatus      workflow.Status       `json:"newStatus"`
			LegacyAction   workflow.LegacyAction `json:"legacyAction"`
		} `json:"outcome"`
	}
	if code := e.act(c.ID, with(map[string]any{"action": "RECEIVE"}), &res); code != 200 {
		t.Fatalf("receive: %d", code)
	}
	if res.Outcome.NewStatus != workflow.Active || res.Outcome.LegacyAction.IEN != 21 || res.Consult.LastAction != "RECEIVED" {
		t.Errorf("receive outcome: %+v last=%s", res.Outcome, res.Consult.LastAction)
	}
	if len(res.Consult.Flags) != 0 {
		t.Errorf("received consult should no longer be flagged: %+v", res.Consult.Flags)
	}

	e.act(c.ID, with(map[string]any{"action": "SCHEDULE"}), &res)
	e.act(c.ID, with(map[string]any{"action": "ADD_NOTE", "noteTitle": "CARDIOLOGY CONSULT NOTE"}), &res)
	if res.Consult.Status != workflow.PartialResults || !res.Consult.Explanation.Blocked {
		t.Fatalf("after unsigned note: %s blocked=%v", res.Consult.Status, res.Consult.Explanation.Blocked)
	}
	var errRes apiError
	if code := e.act(c.ID, with(map[string]any{"action": "FORWARD", "toServiceId": 7}), &errRes); code != 409 || errRes.Error.Rule != "R-PARTIAL-BLOCKS" {
		t.Errorf("forward with partial results: %d %+v", code, errRes.Error)
	}
	noteID := res.Consult.Notes[0].ID
	e.act(c.ID, with(map[string]any{"action": "SIGN_NOTE", "noteId": noteID}), &res)
	if res.Consult.Status != workflow.Complete || !res.Consult.Notes[0].Signed {
		t.Fatalf("after signing: %s %+v", res.Consult.Status, res.Consult.Notes)
	}
	if code := e.act(c.ID, with(map[string]any{"action": "RECEIVE"}), &errRes); code != 409 {
		t.Errorf("receive on complete consult: %d", code)
	}

	var tl []store.Activity
	e.do("GET", fmt.Sprintf("/api/consults/%d/timeline", c.ID), nil, &tl)
	type step struct {
		action   workflow.Action
		prev     workflow.Status
		next     workflow.Status
		legacyID int
	}
	want := []step{
		{workflow.Create, "", workflow.Pending, 2},
		{workflow.Receive, workflow.Pending, workflow.Active, 21},
		{workflow.Schedule, workflow.Active, workflow.Scheduled, 8},
		{workflow.AddNote, workflow.Scheduled, workflow.PartialResults, 9},
		{workflow.SignNote, workflow.PartialResults, workflow.Complete, 10},
	}
	if len(tl) != len(want) {
		t.Fatalf("timeline has %d entries, want %d (rejected actions must not be recorded)", len(tl), len(want))
	}
	for i, w := range want {
		a := tl[i]
		prev := workflow.Status("")
		if a.PreviousStatus != nil {
			prev = *a.PreviousStatus
		}
		if a.Action != w.action || prev != w.prev || a.NewStatus != w.next || a.LegacyActionIEN != w.legacyID {
			t.Errorf("timeline[%d] = %s %s->%s (%d), want %+v", i, a.Action, prev, a.NewStatus, a.LegacyActionIEN, w)
		}
		if a.OccurredAt.IsZero() || a.Actor == "" {
			t.Errorf("timeline[%d] missing timestamp or actor", i)
		}
	}
}

func TestErrorResponses(t *testing.T) {
	e := setup(t)
	c := e.consultByPatient("ZZTEST,BRAVO")
	cases := []struct {
		name string
		path string
		body any
		code int
		rule string
	}{
		{"discontinue needs comment", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "DISCONTINUE", "role": "SERVICE_USER", "comment": " "}, 422, "R-DC-CANCEL"},
		{"requester cannot receive", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "RECEIVE", "role": "REQUESTER"}, 403, "R-ROLES"},
		{"system role refused", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "AUTO_DISCONTINUE", "role": "SYSTEM"}, 403, "R-AUTO-DC"},
		{"unknown role", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "RECEIVE", "role": "JANITOR"}, 400, ""},
		{"unknown action", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "TELEPORT", "role": "SERVICE_USER"}, 422, ""},
		{"forward to missing service", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "FORWARD", "role": "SERVICE_USER", "toServiceId": 999}, 422, "R-FORWARD"},
		{"forward to itself", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "FORWARD", "role": "SERVICE_USER", "toServiceId": 5}, 422, "R-FORWARD"},
		{"unknown consult", "/api/consults/99999/actions", map[string]any{"action": "RECEIVE", "role": "SERVICE_USER"}, 404, ""},
		{"malformed json", fmt.Sprintf("/api/consults/%d/actions", c.ID), "{not json", 400, ""},
		{"unknown field", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "RECEIVE", "role": "SERVICE_USER", "status": "COMPLETE"}, 400, ""},
		{"NUL in comment", fmt.Sprintf("/api/consults/%d/actions", c.ID), map[string]any{"action": "COMMENT", "role": "SERVICE_USER", "comment": "a\x00b"}, 400, ""},
	}
	for _, tc := range cases {
		var er apiError
		code := e.do("POST", tc.path, tc.body, &er)
		if code != tc.code || er.Error.Rule != tc.rule {
			t.Errorf("%s: got %d rule %q (%s), want %d rule %q", tc.name, code, er.Error.Rule, er.Error.Message, tc.code, tc.rule)
		}
	}
	var tl []store.Activity
	e.do("GET", fmt.Sprintf("/api/consults/%d/timeline", c.ID), nil, &tl)
	if len(tl) != 1 {
		t.Errorf("rejected actions must not add activities; timeline has %d", len(tl))
	}
	if code := e.do("GET", "/api/consults/abc", nil, &apiError{}); code != 400 {
		t.Errorf("non-numeric id: %d", code)
	}
	if code := e.do("GET", "/api/consults?bucket=nope", nil, &apiError{}); code != 400 {
		t.Errorf("bad bucket: %d", code)
	}
}

func TestActivityLogIsAppendOnly(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if _, err := e.db.Exec(ctx, `UPDATE consult_activities SET comment = 'tampered' WHERE id = 1`); err == nil {
		t.Error("UPDATE on consult_activities must be refused")
	}
	if _, err := e.db.Exec(ctx, `DELETE FROM consult_activities WHERE id = 1`); err == nil {
		t.Error("DELETE on consult_activities must be refused")
	}
}

func TestAutoDiscontinueJob(t *testing.T) {
	e := setup(t)
	golf := e.consultByPatient("ZZTEST,GOLF")
	var res struct {
		Discontinued []int64 `json:"discontinued"`
	}
	if code := e.do("POST", "/api/jobs/auto-discontinue", nil, &res); code != 200 {
		t.Fatalf("job: %d", code)
	}
	if !slices.Equal(res.Discontinued, []int64{golf.ID}) {
		t.Fatalf("discontinued %v, want only GOLF (%d)", res.Discontinued, golf.ID)
	}
	if got := e.consultByPatient("ZZTEST,GOLF"); got.Status != workflow.Discontinued {
		t.Errorf("GOLF status %s", got.Status)
	}
	if got := e.consultByPatient("ZZTEST,FOXTROT"); got.Status != workflow.Cancelled {
		t.Errorf("FOXTROT (cancelled 26 days) must stay cancelled, got %s", got.Status)
	}
	var tl []store.Activity
	e.do("GET", fmt.Sprintf("/api/consults/%d/timeline", golf.ID), nil, &tl)
	last := tl[len(tl)-1]
	if last.Action != workflow.AutoDiscontinue || last.ActorRole != workflow.RoleSystem || !strings.HasPrefix(last.Comment, "ADC:") {
		t.Errorf("auto-DC activity: %+v", last)
	}
	e.do("POST", "/api/jobs/auto-discontinue", nil, &res)
	if len(res.Discontinued) != 0 {
		t.Errorf("second run must be a no-op, got %v", res.Discontinued)
	}
}

func TestCreateWarnsOnDuplicate(t *testing.T) {
	e := setup(t)
	body := map[string]any{
		"patientName": "ZZTEST,CHARLIE", "patientRef": "DEMO-0003", "toServiceId": 7, "fromLocation": "PRIMARY CARE CLINIC A",
		"requestingProvider": "ZZPROVIDER,BRUNO", "urgency": "ROUTINE", "reasonForRequest": "Worsening cough.",
	}
	var er apiError
	if code := e.do("POST", "/api/consults", body, &er); code != 409 || er.Error.Kind != "DUPLICATE" || len(er.Error.Duplicates) != 2 {
		t.Fatalf("duplicate warning: %d %+v", code, er.Error)
	}
	body["acknowledgeDuplicate"] = true
	var c consult
	if code := e.do("POST", "/api/consults", body, &c); code != 201 || c.Status != workflow.Pending || len(c.Duplicates) != 2 {
		t.Fatalf("acknowledged create: %d %+v", code, c)
	}
	var tl []store.Activity
	e.do("GET", fmt.Sprintf("/api/consults/%d/timeline", c.ID), nil, &tl)
	if len(tl) != 1 || tl[0].Details["duplicatesAcknowledged"] == nil || tl[0].PreviousStatus != nil {
		t.Errorf("create activity: %+v", tl)
	}
	// A plain consult never keeps a procedure name (e.g. left over in the form).
	var plain consult
	e.do("POST", "/api/consults", map[string]any{
		"patientName": "ZZTEST,ROMEO", "patientRef": "DEMO-0201", "toServiceId": 5, "fromLocation": "PRIMARY CARE CLINIC A",
		"requestingProvider": "ZZPROVIDER,BRUNO", "urgency": "ROUTINE", "reasonForRequest": "Dyspepsia.",
		"requestType": "CONSULT", "procedure": "COLONOSCOPY",
	}, &plain)
	if plain.Procedure != "" {
		t.Errorf("CONSULT request stored procedure %q", plain.Procedure)
	}
	delete(body, "acknowledgeDuplicate")
	body["reasonForRequest"] = ""
	body["patientRef"] = "DEMO-9999"
	if code := e.do("POST", "/api/consults", body, &er); code != 422 {
		t.Errorf("missing reason: %d", code)
	}
}

func TestConcurrentActionsAreSerialized(t *testing.T) {
	e := setup(t)
	c := e.consultByPatient("ZZTEST,PAPA")
	codes := make([]int, 2)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.act(c.ID, map[string]any{"action": "RECEIVE", "role": "SERVICE_USER"}, &json.RawMessage{})
		}()
	}
	wg.Wait()
	slices.Sort(codes)
	if !slices.Equal(codes, []int{200, 409}) {
		t.Fatalf("two concurrent receives: %v, want exactly one 200 and one 409", codes)
	}
	var tl []store.Activity
	e.do("GET", fmt.Sprintf("/api/consults/%d/timeline", c.ID), nil, &tl)
	if len(tl) != 2 {
		t.Errorf("timeline has %d entries, want 2", len(tl))
	}
}

func TestIFCPlacerDetail(t *testing.T) {
	e := setup(t)
	kilo := e.consultByPatient("ZZTEST,KILO")
	var d consult
	e.do("GET", fmt.Sprintf("/api/consults/%d?role=SERVICE_USER", kilo.ID), nil, &d)
	if d.Explanation.Owner != "Remote facility" {
		t.Errorf("owner %q", d.Explanation.Owner)
	}
	for _, a := range d.AvailableActions {
		switch a.Action {
		case workflow.Comment:
			if !a.Allowed {
				t.Error("comment must stay available for the placer")
			}
		default:
			if a.Allowed {
				t.Errorf("%s must be unavailable for an IFC placer", a.Action)
			}
		}
	}
	var tl []store.Activity
	e.do("GET", fmt.Sprintf("/api/consults/%d/timeline", kilo.ID), nil, &tl)
	last := tl[len(tl)-1]
	if last.LegacyActionIEN != 25 {
		t.Errorf("forward to remote service must be action 25, got %d", last.LegacyActionIEN)
	}
	if u, _ := last.Details["ifcUpdate"].(string); !strings.Contains(u, "DEMO VAMC NORTH") {
		t.Errorf("forward to remote service must record the IFC update, details %v", last.Details)
	}
}

func TestForwardClearsAttention(t *testing.T) {
	e := setup(t)
	juliet := e.consultByPatient("ZZTEST,JULIET")
	if juliet.ToService.Name != "PULMONARY" || juliet.Attention != "" || juliet.Status != workflow.Pending {
		t.Errorf("forwarded consult: %+v", juliet)
	}
}

func TestStatusClock(t *testing.T) {
	e := setup(t)
	papa := e.consultByPatient("ZZTEST,PAPA") // pending 60h, within-48h urgency
	if len(papa.Flags) == 0 || papa.Flags[0].Code != "NOT_RECEIVED" {
		t.Fatalf("seeded flags: %+v", papa.Flags)
	}
	var res struct{ Consult consult }
	e.act(papa.ID, map[string]any{"action": "COMMENT", "role": "REQUESTER", "comment": "Any update?"}, &res)
	if !res.Consult.StatusChangedAt.Equal(papa.StatusChangedAt) {
		t.Errorf("a comment must not restart the status clock: %v -> %v", papa.StatusChangedAt, res.Consult.StatusChangedAt)
	}
	e.act(papa.ID, map[string]any{"action": "FORWARD", "role": "SERVICE_USER", "toServiceId": 7}, &res)
	if !res.Consult.StatusChangedAt.Equal(now) || len(res.Consult.Flags) != 0 {
		t.Errorf("a forward restarts the receive clock at the new service: %v flags %+v", res.Consult.StatusChangedAt, res.Consult.Flags)
	}
}

func TestExplanationUsesPersistedCancellationReason(t *testing.T) {
	e := setup(t)
	c := e.consultByPatient("ZZTEST,BRAVO")
	user := func(action, comment string) map[string]any {
		return map[string]any{"action": action, "role": "SERVICE_USER", "comment": comment}
	}
	var res struct{ Consult consult }
	e.act(c.ID, user("CANCEL", "First reason"), &res)
	e.act(c.ID, map[string]any{"action": "RESUBMIT", "role": "REQUESTER"}, &res)
	e.act(c.ID, user("COMMENT", "Unrelated comment"), &res)
	e.act(c.ID, user("CANCEL", "Second reason"), &res)
	e.act(c.ID, user("COMMENT", "Another comment"), &res)
	var d consult
	e.do("GET", fmt.Sprintf("/api/consults/%d", c.ID), nil, &d)
	if !slices.Contains(d.Explanation.Reasons, "Cancellation reason: Second reason") {
		t.Errorf("explanation must cite the latest cancellation: %v", d.Explanation.Reasons)
	}
}

func TestTimelineStaysOrderedWhenClockIsBehind(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c := e.consultByPatient("ZZTEST,PAPA")
	in := store.ActionInput{Action: workflow.Receive, Role: workflow.RoleServiceUser, Actor: "ZZHEME,GRACE"}
	if _, err := e.st.ApplyAction(ctx, c.ID, in, now); err != nil {
		t.Fatal(err)
	}
	// A request that read the clock earlier but got the lock later.
	in.Action = workflow.Schedule
	if _, err := e.st.ApplyAction(ctx, c.ID, in, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	tl, err := e.st.Timeline(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	last := tl[len(tl)-1]
	if last.Action != workflow.Schedule || last.OccurredAt.Before(tl[len(tl)-2].OccurredAt) {
		t.Errorf("timeline out of order: %s at %v after %s at %v", last.Action, last.OccurredAt, tl[len(tl)-2].Action, tl[len(tl)-2].OccurredAt)
	}
	got, _ := e.st.GetConsult(ctx, c.ID)
	if got.StatusChangedAt.Before(now) {
		t.Errorf("status clock moved backwards to %v", got.StatusChangedAt)
	}
}
