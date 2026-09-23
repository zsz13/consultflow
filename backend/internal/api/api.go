// Package api exposes the consult workflow over REST/JSON.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"consultflow/internal/store"
	"consultflow/internal/workflow"
)

// Server holds the HTTP handlers.
type Server struct {
	store *store.Store
	now   func() time.Time
}

// New returns the API router. now is injectable for tests.
func New(s *store.Store, now func() time.Time) http.Handler {
	srv := &Server{store: s, now: now}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/meta", srv.meta)
	mux.HandleFunc("GET /api/services", srv.services)
	mux.HandleFunc("GET /api/dashboard", srv.dashboard)
	mux.HandleFunc("GET /api/consults", srv.listConsults)
	mux.HandleFunc("POST /api/consults", srv.createConsult)
	mux.HandleFunc("GET /api/consults/{id}", srv.getConsult)
	mux.HandleFunc("GET /api/consults/{id}/timeline", srv.timeline)
	mux.HandleFunc("POST /api/consults/{id}/actions", srv.action)
	mux.HandleFunc("GET /api/traceability", srv.traceability)
	mux.HandleFunc("POST /api/jobs/auto-discontinue", srv.autoDiscontinue)
	mux.HandleFunc("POST /api/demo/reset", srv.reset)
	return mux
}

// ConsultView is a consult with its derived, deterministic workflow state.
type ConsultView struct {
	store.ConsultRecord
	StatusLabel    string               `json:"statusLabel"`
	Flags          []workflow.Flag      `json:"flags"`
	NeedsAttention bool                 `json:"needsAttention"`
	Duplicates     []int64              `json:"duplicates"`
	Explanation    workflow.Explanation `json:"explanation"`
}

// ConsultDetail adds the actions available to the requesting role.
type ConsultDetail struct {
	ConsultView
	Role             workflow.Role           `json:"role"`
	AvailableActions []workflow.Availability `json:"availableActions"`
}

func (s *Server) views(ctx context.Context, now time.Time) ([]ConsultView, error) {
	all, err := s.store.ListConsults(ctx)
	if err != nil {
		return nil, err
	}
	dups := duplicates(all, now)
	out := make([]ConsultView, len(all))
	for i, c := range all {
		out[i] = view(c, dups[c.ID], now)
	}
	return out, nil
}

func view(c store.ConsultRecord, dups []int64, now time.Time) ConsultView {
	wc := c.Workflow()
	info, _ := c.Status.Info()
	flags := workflow.Assess(wc, dups, now)
	if flags == nil {
		flags = []workflow.Flag{}
	}
	if dups == nil {
		dups = []int64{}
	}
	return ConsultView{ConsultRecord: c, StatusLabel: info.Label, Flags: flags, NeedsAttention: len(flags) > 0,
		Duplicates: dups, Explanation: workflow.Explain(wc, now)}
}

// duplicates maps each consult to the other consults GMRCDPCK would treat
// as its duplicates.
func duplicates(all []store.ConsultRecord, now time.Time) map[int64][]int64 {
	groups := map[workflow.DuplicateKey][]int64{}
	for _, c := range all {
		if workflow.IsDuplicateCandidate(c.Status, c.DateOfRequest, now) {
			groups[c.DuplicateKey()] = append(groups[c.DuplicateKey()], c.ID)
		}
	}
	out := map[int64][]int64{}
	for _, c := range all {
		for _, id := range groups[c.DuplicateKey()] {
			if id != c.ID {
				out[c.ID] = append(out[c.ID], id)
			}
		}
	}
	return out
}

// Buckets group consults on the dashboard. "active" follows GMRCSTL1.
var buckets = map[string]func(ConsultView) bool{
	"attention":           func(v ConsultView) bool { return v.NeedsAttention },
	"active":              func(v ConsultView) bool { return v.Status.Open() },
	"awaiting_scheduling": func(v ConsultView) bool { return v.Status == workflow.Pending || v.Status == workflow.Active },
	"awaiting_results":    func(v ConsultView) bool { return v.Status == workflow.Scheduled || v.Status == workflow.PartialResults },
	"returned":            func(v ConsultView) bool { return v.Status == workflow.Cancelled },
	"closed":              func(v ConsultView) bool { return v.Status.Closed() },
	"all":                 func(ConsultView) bool { return true },
}

func (s *Server) listConsults(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	all, err := s.views(r.Context(), now)
	if err != nil {
		serverError(w, err)
		return
	}
	q := r.URL.Query()
	bucket := q.Get("bucket")
	if bucket == "" {
		bucket = "all"
	}
	inBucket, ok := buckets[bucket]
	if !ok {
		writeError(w, 400, "INVALID", "unknown bucket "+strconv.Quote(bucket), "")
		return
	}
	status := workflow.Status(q.Get("status"))
	if status != "" && !status.Valid() {
		writeError(w, 400, "INVALID", "unknown status "+strconv.Quote(string(status)), "")
		return
	}
	serviceID, _ := strconv.ParseInt(q.Get("serviceId"), 10, 64)
	text := strings.ToLower(strings.TrimSpace(q.Get("q")))
	out := []ConsultView{}
	for _, v := range all {
		if !inBucket(v) || (status != "" && v.Status != status) || (serviceID != 0 && v.ToService.ID != serviceID) {
			continue
		}
		if text != "" && !matches(v, text) {
			continue
		}
		out = append(out, v)
	}
	sortByUrgencyOfAttention(out)
	writeJSON(w, 200, out)
}

func matches(v ConsultView, text string) bool {
	hay := strings.ToLower(strings.Join([]string{strconv.FormatInt(v.ID, 10), v.PatientName, v.PatientRef,
		v.ToService.Name, v.FromLocation, v.RequestingProvider, v.ReasonForRequest, v.Procedure}, " "))
	return strings.Contains(hay, text)
}

// sortByUrgencyOfAttention puts critical flags first, then warnings, then
// the rest; ties go to whoever has waited longest in their status.
func sortByUrgencyOfAttention(vs []ConsultView) {
	rank := func(v ConsultView) int {
		switch {
		case len(v.Flags) > 0 && v.Flags[0].Severity == workflow.Critical:
			return 0
		case len(v.Flags) > 0:
			return 1
		}
		return 2
	}
	slices.SortStableFunc(vs, func(a, b ConsultView) int {
		if ra, rb := rank(a), rank(b); ra != rb {
			return ra - rb
		}
		return a.StatusChangedAt.Compare(b.StatusChangedAt)
	})
}

type statusCount struct {
	workflow.StatusInfo
	Count int `json:"count"`
}

type serviceLoad struct {
	ServiceID int64  `json:"serviceId"`
	Name      string `json:"name"`
	Open      int    `json:"open"`
	Attention int    `json:"attention"`
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	all, err := s.views(r.Context(), now)
	if err != nil {
		serverError(w, err)
		return
	}
	recent, err := s.store.RecentActivity(r.Context(), 12)
	if err != nil {
		serverError(w, err)
		return
	}
	totals := map[string]int{"total": len(all), "critical": 0}
	attention := []ConsultView{}
	loads := map[int64]*serviceLoad{}
	var loadOrder []int64
	counts := map[workflow.Status]int{}
	for _, v := range all {
		counts[v.Status]++
		for name, in := range buckets {
			if name != "all" && in(v) {
				totals[name]++
			}
		}
		if v.NeedsAttention {
			attention = append(attention, v)
			if v.Flags[0].Severity == workflow.Critical {
				totals["critical"]++
			}
		}
		if v.Status.Open() {
			l, ok := loads[v.ToService.ID]
			if !ok {
				l = &serviceLoad{ServiceID: v.ToService.ID, Name: v.ToService.Name}
				loads[v.ToService.ID] = l
				loadOrder = append(loadOrder, v.ToService.ID)
			}
			l.Open++
			if v.NeedsAttention {
				l.Attention++
			}
		}
	}
	sortByUrgencyOfAttention(attention)
	statusCounts := []statusCount{}
	for _, info := range workflow.Statuses() {
		statusCounts = append(statusCounts, statusCount{info, counts[info.Status]})
	}
	byService := []serviceLoad{}
	for _, id := range loadOrder {
		byService = append(byService, *loads[id])
	}
	slices.SortStableFunc(byService, func(a, b serviceLoad) int {
		if a.Attention != b.Attention {
			return b.Attention - a.Attention
		}
		return b.Open - a.Open
	})
	writeJSON(w, 200, map[string]any{
		"generatedAt": now, "totals": totals, "statusCounts": statusCounts, "attention": attention,
		"byService": byService, "recentActivity": recent,
	})
}

func (s *Server) getConsult(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	role := workflow.Role(r.URL.Query().Get("role"))
	if role == "" {
		role = workflow.RoleServiceUser
	}
	if !role.Valid() {
		writeError(w, 400, "INVALID", "unknown role "+strconv.Quote(string(role)), "")
		return
	}
	d, err := s.detail(r.Context(), id, role)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) detail(ctx context.Context, id int64, role workflow.Role) (ConsultDetail, error) {
	now := s.now()
	c, err := s.store.GetConsult(ctx, id)
	if err != nil {
		return ConsultDetail{}, err
	}
	// Duplicates need the other consults; the list is small in this MVP.
	all, err := s.store.ListConsults(ctx)
	if err != nil {
		return ConsultDetail{}, err
	}
	return ConsultDetail{
		ConsultView:      view(c, duplicates(all, now)[id], now),
		Role:             role,
		AvailableActions: workflow.Available(c.Workflow(), role, now),
	}, nil
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	acts, err := s.store.Timeline(r.Context(), id)
	if err != nil {
		storeError(w, err)
		return
	}
	writeJSON(w, 200, acts)
}

type actionBody struct {
	Action              workflow.Action `json:"action"`
	Actor               string          `json:"actor"`
	Role                workflow.Role   `json:"role"`
	Comment             string          `json:"comment"`
	ToServiceID         int64           `json:"toServiceId"`
	Urgency             string          `json:"urgency"`
	Attention           *string         `json:"attention"`
	Reason              string          `json:"reason"`
	SignificantFindings string          `json:"significantFindings"`
	NoteTitle           string          `json:"noteTitle"`
	NoteSigned          bool            `json:"noteSigned"`
	NoteID              int64           `json:"noteId"`
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b actionBody
	if !decode(w, r, &b) {
		return
	}
	if !b.Role.Valid() {
		writeError(w, 400, "INVALID", "role must be one of REQUESTER, SERVICE_USER, SERVICE_ADMIN", "")
		return
	}
	if b.Role == workflow.RoleSystem {
		writeError(w, 403, string(workflow.ErrForbidden), "System actions run only from the auto-discontinue job.", "R-AUTO-DC")
		return
	}
	out, err := s.store.ApplyAction(r.Context(), id, store.ActionInput{
		Action: b.Action, Actor: b.Actor, Role: b.Role, Comment: b.Comment, ToServiceID: b.ToServiceID,
		Urgency: b.Urgency, Attention: b.Attention, Reason: b.Reason, SigFindings: b.SignificantFindings,
		NoteTitle: b.NoteTitle, NoteSigned: b.NoteSigned, NoteID: b.NoteID,
	}, s.now())
	if err != nil {
		storeError(w, err)
		return
	}
	d, err := s.detail(r.Context(), id, b.Role)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"consult": d,
		"outcome": map[string]any{"previousStatus": out.Previous, "newStatus": out.Next, "legacyAction": out.Legacy, "summary": out.Summary},
	})
}

type createBody struct {
	PatientName             string     `json:"patientName"`
	PatientRef              string     `json:"patientRef"`
	ToServiceID             int64      `json:"toServiceId"`
	FromLocation            string     `json:"fromLocation"`
	RequestingProvider      string     `json:"requestingProvider"`
	Attention               string     `json:"attention"`
	Urgency                 string     `json:"urgency"`
	Place                   string     `json:"place"`
	InpatientOutpatient     string     `json:"inpatientOutpatient"`
	RequestType             string     `json:"requestType"`
	Procedure               string     `json:"procedure"`
	ReasonForRequest        string     `json:"reasonForRequest"`
	ProvisionalDiagnosis    string     `json:"provisionalDiagnosis"`
	ClinicallyIndicatedDate *time.Time `json:"clinicallyIndicatedDate"`
	AcknowledgeDuplicate    bool       `json:"acknowledgeDuplicate"`
}

func (s *Server) createConsult(w http.ResponseWriter, r *http.Request) {
	var b createBody
	if !decode(w, r, &b) {
		return
	}
	id, err := s.store.CreateConsult(r.Context(), store.NewConsult{
		PatientName: b.PatientName, PatientRef: b.PatientRef, ToServiceID: b.ToServiceID, FromLocation: b.FromLocation,
		RequestingProvider: b.RequestingProvider, Attention: b.Attention, Urgency: b.Urgency, Place: b.Place,
		InpatientOutpatient: b.InpatientOutpatient, RequestType: b.RequestType, Procedure: b.Procedure,
		ReasonForRequest: b.ReasonForRequest, ProvisionalDiagnosis: b.ProvisionalDiagnosis,
		ClinicallyIndicated: b.ClinicallyIndicatedDate, AcknowledgeDuplicate: b.AcknowledgeDuplicate,
	}, s.now())
	var dup *store.DuplicateError
	if errors.As(err, &dup) {
		writeJSON(w, 409, map[string]any{"error": map[string]any{
			"kind": "DUPLICATE", "rule": "R-DUPLICATE", "duplicates": dup.IDs,
			"message": "A pending, active or scheduled consult for this patient, service and procedure already exists. Confirm to order anyway.",
		}})
		return
	}
	if err != nil {
		storeError(w, err)
		return
	}
	d, err := s.detail(r.Context(), id, workflow.RoleRequester)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 201, d)
}

func (s *Server) services(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Services(r.Context())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"statuses":  workflow.Statuses(),
		"actions":   workflow.ActionDefs(),
		"urgencies": workflow.Urgencies(),
		"roles":     []workflow.Role{workflow.RoleRequester, workflow.RoleServiceUser, workflow.RoleServiceAdmin},
	})
}

func (s *Server) traceability(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, workflow.Rules())
}

func (s *Server) autoDiscontinue(w http.ResponseWriter, r *http.Request) {
	ids, err := s.store.RunAutoDiscontinue(r.Context(), s.now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"discontinued": ids})
}

func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Reset(r.Context(), s.now()); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "reset"})
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "INVALID", "consult id must be a positive integer", "")
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, 400, "INVALID", "could not read body: "+err.Error(), "")
		return false
	}
	// PostgreSQL text and jsonb cannot store NUL characters.
	if bytes.Contains(body, []byte(`\u0000`)) {
		writeError(w, 400, "INVALID", "text may not contain NUL characters", "")
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, 400, "INVALID", "invalid JSON body: "+err.Error(), "")
		return false
	}
	return true
}

func storeError(w http.ResponseWriter, err error) {
	var re *workflow.RuleError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, 404, "NOT_FOUND", "consult not found", "")
	case errors.As(err, &re):
		status := map[workflow.ErrorKind]int{workflow.ErrConflict: 409, workflow.ErrInvalid: 422, workflow.ErrForbidden: 403}[re.Kind]
		writeError(w, status, string(re.Kind), re.Message, re.Rule)
	default:
		serverError(w, err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeError(w, 500, "INTERNAL", "internal server error", "")
}

func writeError(w http.ResponseWriter, status int, kind, message, rule string) {
	body := map[string]any{"kind": kind, "message": message}
	if rule != "" {
		body["rule"] = rule
		if r, ok := workflow.RuleByID(rule); ok {
			body["ruleTitle"] = r.Title
		}
	}
	writeJSON(w, status, map[string]any{"error": body})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode response: %v", err)
	}
}
