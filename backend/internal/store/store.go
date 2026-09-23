// Package store persists consults in PostgreSQL. Every state change goes
// through workflow.Decide inside a transaction that holds a row lock on the
// consult, and appends exactly one audit activity.
package store

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"consultflow/internal/workflow"
)

//go:embed schema.sql
var schema string

// ErrNotFound is returned for an unknown consult.
var ErrNotFound = errors.New("not found")

// errInUse mirrors GMRC's lock failure ("Consult In Use By Another User").
var errInUse = &workflow.RuleError{Kind: workflow.ErrConflict, Rule: "R-LOCK",
	Message: "Unable to update status and last action - consult in use by another user."}

// DuplicateError reports open consults that GMRCDPCK would warn about.
type DuplicateError struct{ IDs []int64 }

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("possible duplicate of %d open consult(s)", len(e.IDs))
}

// Store is the PostgreSQL-backed consult repository.
type Store struct{ pool *pgxpool.Pool }

// Open connects to url, waiting up to a minute for the database to accept
// connections (it may still be starting under docker compose).
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(time.Minute)
	for {
		if err = pool.Ping(ctx); err == nil {
			return &Store{pool}, nil
		}
		if time.Now().After(deadline) {
			pool.Close()
			return nil, fmt.Errorf("database not reachable: %w", err)
		}
		time.Sleep(time.Second)
	}
}

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// Migrate applies the idempotent schema.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// ServiceRecord is a service plus whether it is synthetic demo data.
type ServiceRecord struct {
	workflow.Service
	Synthetic bool `json:"synthetic"`
}

// NoteRecord is a linked result note.
type NoteRecord struct {
	workflow.Note
	Author    string     `json:"author"`
	CreatedAt time.Time  `json:"createdAt"`
	SignedAt  *time.Time `json:"signedAt"`
}

// ConsultRecord is a consult as stored.
type ConsultRecord struct {
	ID                   int64            `json:"id"`
	PatientName          string           `json:"patientName"`
	PatientRef           string           `json:"patientRef"`
	ToService            workflow.Service `json:"toService"`
	FromLocation         string           `json:"fromLocation"`
	RequestingProvider   string           `json:"requestingProvider"`
	Attention            string           `json:"attention"`
	Urgency              string           `json:"urgency"`
	Place                string           `json:"place"`
	InpatientOutpatient  string           `json:"inpatientOutpatient"`
	RequestType          string           `json:"requestType"`
	Procedure            string           `json:"procedure"`
	ReasonForRequest     string           `json:"reasonForRequest"`
	ProvisionalDiagnosis string           `json:"provisionalDiagnosis"`
	DateOfRequest        time.Time        `json:"dateOfRequest"`
	ClinicallyIndicated  *time.Time       `json:"clinicallyIndicatedDate"`
	Status               workflow.Status  `json:"status"`
	StatusChangedAt      time.Time        `json:"statusChangedAt"`
	StatusComment        string           `json:"statusComment"`
	LastAction           string           `json:"lastAction"`
	SignificantFindings  string           `json:"significantFindings"`
	IFCRole              string           `json:"ifcRole"`
	IFCRemoteSite        string           `json:"ifcRemoteSite"`
	IFCRemoteConsultID   string           `json:"ifcRemoteConsultId"`
	CreatedAt            time.Time        `json:"createdAt"`
	UpdatedAt            time.Time        `json:"updatedAt"`
	Notes                []NoteRecord     `json:"notes"`
}

// Workflow returns the state the workflow rules read.
func (r ConsultRecord) Workflow() workflow.Consult {
	notes := make([]workflow.Note, len(r.Notes))
	for i, n := range r.Notes {
		notes[i] = n.Note
	}
	return workflow.Consult{
		ID: r.ID, Status: r.Status, StatusChangedAt: r.StatusChangedAt, StatusComment: r.StatusComment,
		DateOfRequest: r.DateOfRequest, ClinicallyIndicated: r.ClinicallyIndicated, Urgency: r.Urgency,
		ToService: r.ToService, RequestingProvider: r.RequestingProvider, Attention: r.Attention,
		IFCRole: r.IFCRole, IFCRemoteSite: r.IFCRemoteSite, Notes: notes,
	}
}

// DuplicateKey is the GMRCDPCK comparison key.
func (r ConsultRecord) DuplicateKey() workflow.DuplicateKey {
	return workflow.DuplicateKey{PatientRef: r.PatientRef, ServiceID: r.ToService.ID, Procedure: r.Procedure}
}

// Activity is one audit entry.
type Activity struct {
	ID               int64            `json:"id"`
	ConsultID        int64            `json:"consultId"`
	Action           workflow.Action  `json:"action"`
	LegacyActionIEN  int              `json:"legacyActionIen"`
	LegacyActionName string           `json:"legacyActionName"`
	PreviousStatus   *workflow.Status `json:"previousStatus"`
	NewStatus        workflow.Status  `json:"newStatus"`
	Actor            string           `json:"actor"`
	ActorRole        workflow.Role    `json:"actorRole"`
	OccurredAt       time.Time        `json:"occurredAt"`
	RecordedAt       time.Time        `json:"recordedAt"`
	Summary          string           `json:"summary"`
	Comment          string           `json:"comment"`
	Details          map[string]any   `json:"details"`
}

// ActionInput is a requested action as received from the API.
type ActionInput struct {
	Action      workflow.Action
	Actor       string
	Role        workflow.Role
	Comment     string
	ToServiceID int64
	Urgency     string
	Attention   *string
	Reason      string
	SigFindings string
	NoteTitle   string
	NoteSigned  bool
	NoteID      int64
}

// NewConsult is a new order.
type NewConsult struct {
	PatientName          string
	PatientRef           string
	ToServiceID          int64
	FromLocation         string
	RequestingProvider   string
	Attention            string
	Urgency              string
	Place                string
	InpatientOutpatient  string
	RequestType          string
	Procedure            string
	ReasonForRequest     string
	ProvisionalDiagnosis string
	ClinicallyIndicated  *time.Time
	AcknowledgeDuplicate bool
	// Set only for a request arriving from another facility (IFC filler).
	IFCFillerFrom   string
	RemoteConsultID string
}

type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Services returns every service ordered by name.
func (s *Store) Services(ctx context.Context) ([]ServiceRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, usage, COALESCE(ifc_routing_site, ''), synthetic FROM services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ServiceRecord, error) {
		var s ServiceRecord
		err := r.Scan(&s.ID, &s.Name, &s.Usage, &s.IFCRoutingSite, &s.Synthetic)
		return s, err
	})
}

func getService(ctx context.Context, q querier, id int64) (workflow.Service, error) {
	var s workflow.Service
	err := q.QueryRow(ctx, `SELECT id, name, usage, COALESCE(ifc_routing_site, '') FROM services WHERE id = $1`, id).
		Scan(&s.ID, &s.Name, &s.Usage, &s.IFCRoutingSite)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

const consultSelect = `
SELECT c.id, c.patient_name, c.patient_ref, s.id, s.name, s.usage, COALESCE(s.ifc_routing_site, ''),
       c.from_location, c.requesting_provider, c.attention, c.urgency, c.place, c.inpatient_outpatient,
       c.request_type, c.procedure_name, c.reason_for_request, c.provisional_diagnosis, c.date_of_request,
       c.clinically_indicated_date, c.status, c.status_changed_at, c.last_action,
       COALESCE(c.significant_findings, ''), COALESCE(c.ifc_role, ''), c.ifc_remote_site, c.ifc_remote_consult_id,
       c.created_at, c.updated_at,
       COALESCE((SELECT a.comment FROM consult_activities a
                 WHERE a.consult_id = c.id AND a.new_status = c.status
                   AND a.previous_status IS DISTINCT FROM a.new_status
                 ORDER BY a.occurred_at DESC, a.id DESC LIMIT 1), '')
FROM consults c JOIN services s ON s.id = c.to_service_id`

func scanConsult(row pgx.Row) (ConsultRecord, error) {
	var r ConsultRecord
	err := row.Scan(&r.ID, &r.PatientName, &r.PatientRef, &r.ToService.ID, &r.ToService.Name, &r.ToService.Usage,
		&r.ToService.IFCRoutingSite, &r.FromLocation, &r.RequestingProvider, &r.Attention, &r.Urgency, &r.Place,
		&r.InpatientOutpatient, &r.RequestType, &r.Procedure, &r.ReasonForRequest, &r.ProvisionalDiagnosis,
		&r.DateOfRequest, &r.ClinicallyIndicated, &r.Status, &r.StatusChangedAt, &r.LastAction,
		&r.SignificantFindings, &r.IFCRole, &r.IFCRemoteSite, &r.IFCRemoteConsultID, &r.CreatedAt, &r.UpdatedAt,
		&r.StatusComment)
	return r, err
}

func loadConsults(ctx context.Context, q querier, where string, args ...any) ([]ConsultRecord, error) {
	rows, err := q.Query(ctx, consultSelect+" "+where+" ORDER BY c.id", args...)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ConsultRecord, error) { return scanConsult(r) })
	if err != nil || len(list) == 0 {
		return list, err
	}
	ids := make([]int64, len(list))
	byID := make(map[int64]*ConsultRecord, len(list))
	for i := range list {
		ids[i] = list[i].ID
		list[i].Notes = []NoteRecord{}
		byID[list[i].ID] = &list[i]
	}
	nrows, err := q.Query(ctx, `SELECT id, consult_id, title, author, signed, created_at, signed_at
		FROM consult_notes WHERE consult_id = ANY($1) ORDER BY id`, ids)
	if err != nil {
		return nil, err
	}
	defer nrows.Close()
	for nrows.Next() {
		var n NoteRecord
		var consultID int64
		if err := nrows.Scan(&n.ID, &consultID, &n.Title, &n.Author, &n.Signed, &n.CreatedAt, &n.SignedAt); err != nil {
			return nil, err
		}
		c := byID[consultID]
		c.Notes = append(c.Notes, n)
	}
	return list, nrows.Err()
}

func getConsult(ctx context.Context, q querier, id int64) (ConsultRecord, error) {
	list, err := loadConsults(ctx, q, "WHERE c.id = $1", id)
	if err != nil {
		return ConsultRecord{}, err
	}
	if len(list) == 0 {
		return ConsultRecord{}, ErrNotFound
	}
	return list[0], nil
}

// ListConsults returns every consult with its notes.
func (s *Store) ListConsults(ctx context.Context) ([]ConsultRecord, error) {
	return loadConsults(ctx, s.pool, "")
}

// GetConsult returns one consult.
func (s *Store) GetConsult(ctx context.Context, id int64) (ConsultRecord, error) {
	return getConsult(ctx, s.pool, id)
}

const activitySelect = `
SELECT a.id, a.consult_id, a.action, a.legacy_action_ien, a.legacy_action_name, a.previous_status, a.new_status,
       a.actor, a.actor_role, a.occurred_at, a.recorded_at, a.summary, a.comment, a.details
FROM consult_activities a`

func collectActivities(rows pgx.Rows) ([]Activity, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Activity, error) {
		var a Activity
		err := r.Scan(&a.ID, &a.ConsultID, &a.Action, &a.LegacyActionIEN, &a.LegacyActionName, &a.PreviousStatus,
			&a.NewStatus, &a.Actor, &a.ActorRole, &a.OccurredAt, &a.RecordedAt, &a.Summary, &a.Comment, &a.Details)
		return a, err
	})
}

// Timeline returns a consult's activities in chronological order.
func (s *Store) Timeline(ctx context.Context, id int64) ([]Activity, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM consults WHERE id = $1)`, id).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, activitySelect+` WHERE a.consult_id = $1 ORDER BY a.occurred_at, a.id`, id)
	if err != nil {
		return nil, err
	}
	return collectActivities(rows)
}

// RecentActivity returns the latest activities across all consults.
func (s *Store) RecentActivity(ctx context.Context, limit int) ([]Activity, error) {
	rows, err := s.pool.Query(ctx, activitySelect+` ORDER BY a.occurred_at DESC, a.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return collectActivities(rows)
}

// ApplyAction validates and applies one action atomically.
func (s *Store) ApplyAction(ctx context.Context, id int64, in ActionInput, now time.Time) (workflow.Outcome, error) {
	var out workflow.Outcome
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		out, err = applyAction(ctx, tx, id, in, now)
		return err
	})
	return out, err
}

func applyAction(ctx context.Context, tx pgx.Tx, id int64, in ActionInput, now time.Time) (workflow.Outcome, error) {
	// Like L +^GMR(123,GMRCO):2 — wait briefly for the record, then give up.
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`); err != nil {
		return workflow.Outcome{}, err
	}
	var locked int64
	if err := tx.QueryRow(ctx, `SELECT id FROM consults WHERE id = $1 FOR UPDATE`, id).Scan(&locked); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return workflow.Outcome{}, ErrNotFound
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "55P03" {
			return workflow.Outcome{}, errInUse
		}
		return workflow.Outcome{}, err
	}
	rec, err := getConsult(ctx, tx, id)
	if err != nil {
		return workflow.Outcome{}, err
	}
	// The caller read the clock before taking the lock; a concurrent action
	// may have committed a later time since. Keep each consult's history in order.
	var last *time.Time
	if err := tx.QueryRow(ctx, `SELECT max(occurred_at) FROM consult_activities WHERE consult_id = $1`, id).
		Scan(&last); err != nil {
		return workflow.Outcome{}, err
	}
	if last != nil && now.Before(*last) {
		now = *last
	}
	req := workflow.Request{
		Action: in.Action, Role: in.Role, Comment: in.Comment, Urgency: in.Urgency, Attention: in.Attention,
		Reason: in.Reason, SigFindings: in.SigFindings, NoteTitle: in.NoteTitle, NoteSigned: in.NoteSigned,
		NoteID: in.NoteID, Now: now,
	}
	if in.ToServiceID != 0 {
		svc, err := getService(ctx, tx, in.ToServiceID)
		if errors.Is(err, ErrNotFound) {
			return workflow.Outcome{}, &workflow.RuleError{Kind: workflow.ErrInvalid, Rule: "R-FORWARD",
				Message: "Error in service chosen - service does not exist."}
		}
		if err != nil {
			return workflow.Outcome{}, err
		}
		req.ToService = &svc
	}
	out, err := workflow.Decide(rec.Workflow(), req)
	if err != nil {
		return workflow.Outcome{}, err
	}

	next := rec
	p := out.Patch
	if p.ToService != nil {
		next.ToService = *p.ToService
	}
	setIf(&next.Urgency, p.Urgency)
	setIf(&next.Attention, p.Attention)
	setIf(&next.SignificantFindings, p.SigFindings)
	setIf(&next.IFCRole, p.IFCRole)
	setIf(&next.IFCRemoteSite, p.IFCRemoteSite)
	setIf(&next.ReasonForRequest, p.ReasonForRequest)
	next.Status = out.Next
	// A forward restarts the clock too: the new service has not seen it yet.
	if out.StatusChanged() || p.ToService != nil {
		next.StatusChangedAt = now
	}
	if _, err := tx.Exec(ctx, `UPDATE consults SET status = $2, status_changed_at = $3, last_action = $4,
		to_service_id = $5, urgency = $6, attention = $7, significant_findings = NULLIF($8, ''),
		ifc_role = NULLIF($9, ''), ifc_remote_site = $10, reason_for_request = $11, updated_at = $12 WHERE id = $1`,
		id, next.Status, next.StatusChangedAt, out.Legacy.Name, next.ToService.ID, next.Urgency, next.Attention,
		next.SignificantFindings, next.IFCRole, next.IFCRemoteSite, next.ReasonForRequest, now); err != nil {
		return workflow.Outcome{}, err
	}

	actor := strings.TrimSpace(in.Actor)
	if actor == "" {
		actor = string(in.Role)
	}
	if op := out.Note; op != nil {
		if op.SignID != 0 {
			tag, err := tx.Exec(ctx, `UPDATE consult_notes SET signed = true, signed_at = $3
				WHERE id = $1 AND consult_id = $2 AND NOT signed`, op.SignID, id, now)
			if err != nil {
				return workflow.Outcome{}, err
			}
			if tag.RowsAffected() != 1 {
				return workflow.Outcome{}, fmt.Errorf("note %d was not signed", op.SignID)
			}
			out.Details["noteId"] = op.SignID
		} else {
			var noteID int64
			var signedAt *time.Time
			if op.CreateSigned {
				signedAt = &now
			}
			if err := tx.QueryRow(ctx, `INSERT INTO consult_notes (consult_id, title, author, signed, created_at, signed_at)
				VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, id, op.CreateTitle, actor, op.CreateSigned, now, signedAt).
				Scan(&noteID); err != nil {
				return workflow.Outcome{}, err
			}
			out.Details["noteId"] = noteID
		}
	}
	if rec.IFCRole != "" || next.IFCRole != "" {
		site := next.IFCRemoteSite
		out.Details["ifcUpdate"] = "Update message to " + site + " (simulated; HL7 messaging is out of scope)"
	}
	prev := out.Previous
	err = insertActivity(ctx, tx, Activity{
		ConsultID: id, Action: in.Action, LegacyActionIEN: out.Legacy.IEN, LegacyActionName: out.Legacy.Name,
		PreviousStatus: &prev, NewStatus: out.Next, Actor: actor, ActorRole: in.Role, OccurredAt: now,
		RecordedAt: now, Summary: out.Summary, Comment: out.Comment, Details: out.Details,
	})
	return out, err
}

func setIf(dst *string, v *string) {
	if v != nil {
		*dst = *v
	}
}

func insertActivity(ctx context.Context, q querier, a Activity) error {
	if a.Details == nil {
		a.Details = map[string]any{}
	}
	_, err := q.Exec(ctx, `INSERT INTO consult_activities (consult_id, action, legacy_action_ien, legacy_action_name,
		previous_status, new_status, actor, actor_role, occurred_at, recorded_at, summary, comment, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		a.ConsultID, a.Action, a.LegacyActionIEN, a.LegacyActionName, a.PreviousStatus, a.NewStatus, a.Actor,
		a.ActorRole, a.OccurredAt, a.RecordedAt, a.Summary, a.Comment, a.Details)
	return err
}

// CreateConsult files a new consult as PENDING. It returns a
// *DuplicateError when GMRCDPCK would warn and the caller has not
// acknowledged it.
func (s *Store) CreateConsult(ctx context.Context, in NewConsult, now time.Time) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		id, err = createConsult(ctx, tx, in, now)
		return err
	})
	return id, err
}

func createConsult(ctx context.Context, tx pgx.Tx, in NewConsult, now time.Time) (int64, error) {
	for name, v := range map[string]string{
		"patient name": in.PatientName, "patient identifier": in.PatientRef,
		"requesting location": in.FromLocation, "requesting provider": in.RequestingProvider,
	} {
		if strings.TrimSpace(v) == "" {
			return 0, &workflow.RuleError{Kind: workflow.ErrInvalid, Rule: "R-CREATE", Message: "A " + name + " is required."}
		}
	}
	if in.RequestType == "" {
		in.RequestType = "CONSULT"
	}
	if in.InpatientOutpatient == "" {
		in.InpatientOutpatient = "O"
	}
	if in.RequestType != "CONSULT" && in.RequestType != "PROCEDURE" {
		return 0, &workflow.RuleError{Kind: workflow.ErrInvalid, Rule: "R-CREATE", Message: "Request type must be CONSULT or PROCEDURE."}
	}
	if in.RequestType == "CONSULT" {
		in.Procedure = "" // only procedure requests name a procedure
	}
	if in.RequestType == "PROCEDURE" && strings.TrimSpace(in.Procedure) == "" {
		return 0, &workflow.RuleError{Kind: workflow.ErrInvalid, Rule: "R-CREATE", Message: "A procedure request needs a procedure."}
	}
	if in.InpatientOutpatient != "I" && in.InpatientOutpatient != "O" {
		return 0, &workflow.RuleError{Kind: workflow.ErrInvalid, Rule: "R-CREATE", Message: "Inpatient/outpatient must be I or O."}
	}
	svc, err := getService(ctx, tx, in.ToServiceID)
	if errors.Is(err, ErrNotFound) {
		return 0, &workflow.RuleError{Kind: workflow.ErrInvalid, Rule: "R-CREATE", Message: "Error in service chosen - service does not exist."}
	}
	if err != nil {
		return 0, err
	}
	if err := workflow.ValidateNew(&svc, in.Urgency, in.ReasonForRequest); err != nil {
		return 0, err
	}

	dups, err := duplicateCandidates(ctx, tx, workflow.DuplicateKey{PatientRef: in.PatientRef, ServiceID: svc.ID, Procedure: in.Procedure}, now)
	if err != nil {
		return 0, err
	}
	if len(dups) > 0 && !in.AcknowledgeDuplicate {
		return 0, &DuplicateError{IDs: dups}
	}

	ifcRole, remoteSite := "", ""
	summary := "Consult ordered to " + svc.Name
	actor, role := in.RequestingProvider, workflow.RoleRequester
	switch {
	case in.IFCFillerFrom != "":
		ifcRole, remoteSite = workflow.IFCFiller, in.IFCFillerFrom
		summary = "Inter-facility request received from " + remoteSite + " for " + svc.Name
		actor, role = "HL7 from "+remoteSite, workflow.RoleSystem
	case svc.IsIFC():
		ifcRole, remoteSite = workflow.IFCPlacer, svc.IFCRoutingSite
		summary = "Inter-facility consult ordered to " + svc.Name + " at " + remoteSite
	}
	legacy := workflow.CreateLegacyAction(ifcRole)

	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO consults (patient_name, patient_ref, to_service_id, from_location,
		requesting_provider, attention, urgency, place, inpatient_outpatient, request_type, procedure_name,
		reason_for_request, provisional_diagnosis, date_of_request, clinically_indicated_date, status,
		status_changed_at, last_action, ifc_role, ifc_remote_site, ifc_remote_consult_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $14, $17, NULLIF($18, ''), $19, $20, $14, $14)
		RETURNING id`,
		strings.TrimSpace(in.PatientName), strings.TrimSpace(in.PatientRef), svc.ID, strings.TrimSpace(in.FromLocation),
		strings.TrimSpace(in.RequestingProvider), strings.TrimSpace(in.Attention), in.Urgency, in.Place,
		in.InpatientOutpatient, in.RequestType, strings.TrimSpace(in.Procedure), strings.TrimSpace(in.ReasonForRequest),
		strings.TrimSpace(in.ProvisionalDiagnosis), now, in.ClinicallyIndicated, workflow.Pending, legacy.Name,
		ifcRole, remoteSite, in.RemoteConsultID).Scan(&id)
	if err != nil {
		return 0, err
	}
	details := map[string]any{}
	if len(dups) > 0 {
		details["duplicatesAcknowledged"] = dups
	}
	if ifcRole != "" {
		details["ifcRole"] = ifcRole
		details["remoteSite"] = remoteSite
	}
	return id, insertActivity(ctx, tx, Activity{
		ConsultID: id, Action: workflow.Create, LegacyActionIEN: legacy.IEN, LegacyActionName: legacy.Name,
		NewStatus: workflow.Pending, Actor: actor, ActorRole: role, OccurredAt: now, RecordedAt: now,
		Summary: summary, Comment: "", Details: details,
	})
}

func duplicateCandidates(ctx context.Context, q querier, key workflow.DuplicateKey, now time.Time) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT id, status, date_of_request FROM consults
		WHERE patient_ref = $1 AND to_service_id = $2 AND procedure_name = $3 ORDER BY id`,
		key.PatientRef, key.ServiceID, key.Procedure)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		var st workflow.Status
		var requested time.Time
		if err := rows.Scan(&id, &st, &requested); err != nil {
			return nil, err
		}
		if workflow.IsDuplicateCandidate(st, requested, now) {
			ids = append(ids, id)
		}
	}
	return ids, rows.Err()
}

// autoDCActor names the legacy option the job reproduces.
const autoDCActor = "GMRC CHANGE STATUS X TO DC (overnight job)"

// RunAutoDiscontinue discontinues every consult cancelled for at least 31
// days, as GMRCCXDC does, and returns their ids.
func (s *Store) RunAutoDiscontinue(ctx context.Context, now time.Time) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM consults WHERE status = $1 AND status_changed_at <= $2 ORDER BY id`,
		workflow.Cancelled, now.Add(-workflow.AutoDiscontinueAfter))
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	done := []int64{}
	for _, id := range ids {
		_, err := s.ApplyAction(ctx, id, ActionInput{Action: workflow.AutoDiscontinue, Role: workflow.RoleSystem, Actor: autoDCActor}, now)
		var re *workflow.RuleError
		if errors.As(err, &re) {
			continue // changed since the scan (e.g. resubmitted); skip like the legacy job
		}
		if err != nil {
			return done, err
		}
		done = append(done, id)
	}
	return done, nil
}
