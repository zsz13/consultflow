package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"consultflow/internal/workflow"
)

// Synthetic demo data only. Names use the VistA "ZZ" prefix for test
// entries; there is no real patient or provider data here. Service names and
// IENs below 1000 are the national entries in REQUEST SERVICES (#123.5).
var seedServices = []ServiceRecord{
	{workflow.Service{ID: 1, Name: "ALL SERVICES", Usage: workflow.UsageGrouper}, false},
	{workflow.Service{ID: 2, Name: "MEDICINE"}, false},
	{workflow.Service{ID: 3, Name: "PHARMACY SERVICE"}, false},
	{workflow.Service{ID: 4, Name: "CARDIOLOGY"}, false},
	{workflow.Service{ID: 5, Name: "GASTROENTEROLOGY"}, false},
	{workflow.Service{ID: 6, Name: "HEMATOLOGY"}, false},
	{workflow.Service{ID: 7, Name: "PULMONARY"}, false},
	{workflow.Service{ID: 8, Name: "RHEUMATOLOGY"}, false},
	{workflow.Service{ID: 40, Name: "EYEGLASS REQUEST", Usage: workflow.UsageDisabled}, false},
	{workflow.Service{ID: 1001, Name: "ELECTROPHYSIOLOGY - REMOTE", IFCRoutingSite: "DEMO VAMC NORTH"}, true},
}

const (
	svcMedicine  = 2
	svcPharmacy  = 3
	svcCardio    = 4
	svcGI        = 5
	svcHeme      = 6
	svcPulm      = 7
	svcRheum     = 8
	svcRemoteEP  = 1001
	edProvider   = "ZZPROVIDER,ANA"
	pcProvider   = "ZZPROVIDER,BRUNO"
	wardProvider = "ZZPROVIDER,CHEN"
	adminUser    = "ZZADMIN,KAI"
)

// Staff who act for each service in the demo.
var serviceStaff = map[int64]string{
	svcMedicine: "ZZMED,IRIS", svcPharmacy: "ZZPHARM,JONAH", svcCardio: "ZZCARDIO,DANA", svcGI: "ZZGI,FARAH",
	svcHeme: "ZZHEME,GRACE", svcPulm: "ZZPULM,EVAN", svcRheum: "ZZRHEUM,HUGO",
}

type seedStep struct {
	ago time.Duration
	in  ActionInput
}

type scenario struct {
	ago   time.Duration
	cid   time.Duration // clinically indicated date, as time before now; 0 = none
	c     NewConsult
	steps []seedStep
}

const (
	hour = time.Hour
	day  = 24 * time.Hour
)

func svcUser(svc int64, a workflow.Action, extra ActionInput) ActionInput {
	extra.Action, extra.Role, extra.Actor = a, workflow.RoleServiceUser, serviceStaff[svc]
	return extra
}

var scenarios = []scenario{
	// STAT consult from the ED that nobody has received for 30 hours.
	{ago: 30 * hour, c: NewConsult{PatientName: "ZZTEST,ALPHA", PatientRef: "DEMO-0001", ToServiceID: svcCardio,
		FromLocation: "EMERGENCY DEPARTMENT", RequestingProvider: edProvider, Urgency: "STAT", Place: "BEDSIDE",
		InpatientOutpatient: "I", ReasonForRequest: "Intermittent chest pain, borderline troponin. Please evaluate for ACS.",
		ProvisionalDiagnosis: "Chest pain, unspecified"}},
	// Routine procedure request, recently ordered: normal.
	{ago: 2 * day, c: NewConsult{PatientName: "ZZTEST,BRAVO", PatientRef: "DEMO-0002", ToServiceID: svcGI,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE",
		Place: "CONSULTANT'S CHOICE", RequestType: "PROCEDURE", Procedure: "COLONOSCOPY",
		ReasonForRequest:     "Screening colonoscopy; first-degree relative with colon cancer at 52.",
		ProvisionalDiagnosis: "Family history of malignant neoplasm of digestive organs"}},
	// Received, waiting to be scheduled. Also has a duplicate (next scenario).
	{ago: 12 * day, c: NewConsult{PatientName: "ZZTEST,CHARLIE", PatientRef: "DEMO-0003", ToServiceID: svcPulm,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE",
		Place: "CONSULTANT'S CHOICE", ReasonForRequest: "Chronic cough for 3 months; spirometry shows mild obstruction.",
		ProvisionalDiagnosis: "Chronic cough"},
		steps: []seedStep{{10 * day, svcUser(svcPulm, workflow.Receive, ActionInput{})}}},
	{ago: 1 * day, c: NewConsult{PatientName: "ZZTEST,CHARLIE", PatientRef: "DEMO-0003", ToServiceID: svcPulm,
		FromLocation: "5 WEST MEDICINE", RequestingProvider: wardProvider, Urgency: "WITHIN 1 WEEK",
		Place: "CONSULTANT'S CHOICE", ReasonForRequest: "Dyspnea on exertion; please assess before discharge follow-up.",
		ProvisionalDiagnosis: "Shortness of breath", AcknowledgeDuplicate: true}},
	// Scheduled long ago, never resulted: past the 30-day window from the CID.
	{ago: 44 * day, cid: 40 * day, c: NewConsult{PatientName: "ZZTEST,DELTA", PatientRef: "DEMO-0004", ToServiceID: svcCardio,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "New systolic murmur; echocardiogram and cardiology evaluation.", ProvisionalDiagnosis: "Cardiac murmur"},
		steps: []seedStep{
			{42 * day, svcUser(svcCardio, workflow.Receive, ActionInput{})},
			{38 * day, svcUser(svcCardio, workflow.Schedule, ActionInput{Comment: "Clinic visit booked with echo same day."})},
		}},
	// Unsigned note: partial results, blocked.
	{ago: 14 * day, c: NewConsult{PatientName: "ZZTEST,ECHO", PatientRef: "DEMO-0005", ToServiceID: svcHeme,
		FromLocation: "5 WEST MEDICINE", RequestingProvider: wardProvider, Urgency: "WITHIN 72 HOURS", Place: "BEDSIDE",
		InpatientOutpatient: "I", ReasonForRequest: "Unexplained normocytic anemia, Hgb 8.9. Please evaluate.",
		ProvisionalDiagnosis: "Anemia, unspecified"},
		steps: []seedStep{
			{13 * day, svcUser(svcHeme, workflow.Receive, ActionInput{})},
			{9 * day, svcUser(svcHeme, workflow.Schedule, ActionInput{})},
			{3 * day, svcUser(svcHeme, workflow.AddNote, ActionInput{NoteTitle: "HEMATOLOGY CONSULT NOTE"})},
		}},
	// Cancelled 26 days ago: 5 days left before automatic discontinue.
	{ago: 30 * day, c: NewConsult{PatientName: "ZZTEST,FOXTROT", PatientRef: "DEMO-0006", ToServiceID: svcRheum,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Joint pain.", ProvisionalDiagnosis: "Arthralgia"},
		steps: []seedStep{
			{26 * day, svcUser(svcRheum, workflow.Cancel, ActionInput{Comment: "Please document ANA and ESR results and a trial of NSAIDs before referral."})},
		}},
	// Cancelled 35 days ago: due for the overnight auto-discontinue job.
	{ago: 40 * day, c: NewConsult{PatientName: "ZZTEST,GOLF", PatientRef: "DEMO-0007", ToServiceID: svcGI,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Recurrent epigastric pain.", ProvisionalDiagnosis: "Dyspepsia"},
		steps: []seedStep{
			{38 * day, svcUser(svcGI, workflow.Receive, ActionInput{})},
			{35 * day, svcUser(svcGI, workflow.Cancel, ActionInput{Comment: "Patient already followed by community GI; duplicate of outside referral."})},
		}},
	// Full normal lifecycle to completion with significant findings.
	{ago: 20 * day, c: NewConsult{PatientName: "ZZTEST,HOTEL", PatientRef: "DEMO-0008", ToServiceID: svcCardio,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "WITHIN 1 WEEK", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Palpitations with exertion; Holter monitor requested.", ProvisionalDiagnosis: "Palpitations"},
		steps: []seedStep{
			{19 * day, svcUser(svcCardio, workflow.Receive, ActionInput{})},
			{18 * day, svcUser(svcCardio, workflow.Schedule, ActionInput{})},
			{6 * day, svcUser(svcCardio, workflow.AddNote, ActionInput{NoteTitle: "CARDIOLOGY CONSULT NOTE"})},
			{5*day + 2*hour, svcUser(svcCardio, workflow.Comment, ActionInput{Comment: "Holter reviewed with attending: paroxysmal AF."})},
			{5*day + hour, svcUser(svcCardio, workflow.SignNote, ActionInput{NoteID: -1})},
			{5 * day, svcUser(svcCardio, workflow.SigFindings, ActionInput{SigFindings: "Y"})},
		}},
	// Discontinued by the requesting provider.
	{ago: 6 * day, c: NewConsult{PatientName: "ZZTEST,INDIA", PatientRef: "DEMO-0009", ToServiceID: svcMedicine,
		FromLocation: "EMERGENCY DEPARTMENT", RequestingProvider: edProvider, Urgency: "WITHIN 48 HOURS", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Uncontrolled diabetes; outpatient medicine follow-up.", ProvisionalDiagnosis: "Type 2 diabetes with hyperglycemia"},
		steps: []seedStep{
			{4 * day, ActionInput{Action: workflow.Discontinue, Role: workflow.RoleRequester, Actor: edProvider,
				Comment: "Patient admitted; inpatient team will manage."}},
		}},
	// Received by Medicine, then forwarded to Pulmonary (attention cleared).
	{ago: 3 * day, c: NewConsult{PatientName: "ZZTEST,JULIET", PatientRef: "DEMO-0010", ToServiceID: svcMedicine,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Attention: "ZZMED,IRIS", Urgency: "ROUTINE",
		Place: "CONSULTANT'S CHOICE", ReasonForRequest: "Nocturnal hypoxia on home oximetry.", ProvisionalDiagnosis: "Hypoxemia"},
		steps: []seedStep{
			{2 * day, svcUser(svcMedicine, workflow.Receive, ActionInput{})},
			{1 * day, svcUser(svcMedicine, workflow.Forward, ActionInput{ToServiceID: svcPulm, Comment: "Primarily a pulmonary question; forwarding."})},
		}},
	// Inter-facility: this site is the placer; the remote site has not received it.
	{ago: 9 * day, c: NewConsult{PatientName: "ZZTEST,KILO", PatientRef: "DEMO-0011", ToServiceID: svcCardio,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Recurrent SVT despite medication; evaluate for ablation.", ProvisionalDiagnosis: "Supraventricular tachycardia"},
		steps: []seedStep{
			{8 * day, svcUser(svcCardio, workflow.Forward, ActionInput{ToServiceID: svcRemoteEP,
				Comment: "Electrophysiology study is not offered locally; forwarding to DEMO VAMC NORTH."})},
		}},
	// Inter-facility: this site is the filler for a remote request.
	{ago: 6 * day, c: NewConsult{PatientName: "ZZTEST,LIMA", PatientRef: "DEMO-0012", ToServiceID: svcPulm,
		FromLocation: "DEMO VAMC SOUTH", RequestingProvider: "ZZREMOTE,MAYA", Urgency: "WITHIN 1 MONTH", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Pulmonary nodule 9 mm on CT; please evaluate.", ProvisionalDiagnosis: "Solitary pulmonary nodule",
		IFCFillerFrom: "DEMO VAMC SOUTH", RemoteConsultID: "SOUTH-88121"},
		steps: []seedStep{
			{5 * day, svcUser(svcPulm, workflow.Receive, ActionInput{})},
			{2 * day, svcUser(svcPulm, workflow.Schedule, ActionInput{})},
		}},
	// Received 66 days ago and never scheduled: past the 60-day window.
	{ago: 70 * day, c: NewConsult{PatientName: "ZZTEST,MIKE", PatientRef: "DEMO-0013", ToServiceID: svcPulm,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Suspected obstructive sleep apnea; sleep study evaluation.", ProvisionalDiagnosis: "Sleep apnea, unspecified"},
		steps: []seedStep{
			{66 * day, svcUser(svcPulm, workflow.Receive, ActionInput{})},
			{40 * day, svcUser(svcPulm, workflow.Comment, ActionInput{Comment: "Left voicemail for patient to schedule. No response."})},
		}},
	// Cancelled, then edited and resubmitted, then received.
	{ago: 10 * day, c: NewConsult{PatientName: "ZZTEST,NOVEMBER", PatientRef: "DEMO-0014", ToServiceID: svcRheum,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "ROUTINE", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Hand pain.", ProvisionalDiagnosis: "Polyarthralgia"},
		steps: []seedStep{
			{8 * day, svcUser(svcRheum, workflow.Cancel, ActionInput{Comment: "Reason for request lacks exam findings and labs."})},
			{6 * day, ActionInput{Action: workflow.Resubmit, Role: workflow.RoleRequester, Actor: pcProvider, Urgency: "WITHIN 1 WEEK",
				Reason: "Symmetric small-joint polyarthritis of both hands for 8 weeks; RF positive, ESR 48."}},
			{5 * day, svcUser(svcRheum, workflow.Receive, ActionInput{})},
		}},
	// Administratively completed; no note needed.
	{ago: 5 * day, c: NewConsult{PatientName: "ZZTEST,OSCAR", PatientRef: "DEMO-0015", ToServiceID: svcPharmacy,
		FromLocation: "5 WEST MEDICINE", RequestingProvider: wardProvider, Urgency: "WITHIN 72 HOURS", Place: "BEDSIDE",
		InpatientOutpatient: "I", ReasonForRequest: "Medication reconciliation before discharge; 14 active medications.",
		ProvisionalDiagnosis: "Polypharmacy"},
		steps: []seedStep{
			{4 * day, svcUser(svcPharmacy, workflow.Receive, ActionInput{})},
			{2 * day, ActionInput{Action: workflow.AdminComplete, Role: workflow.RoleServiceAdmin, Actor: adminUser, SigFindings: "N",
				Comment: "Reconciliation completed by phone with the ward team; no note required."}},
		}},
	// Within-48-hours request pending for 60 hours.
	{ago: 60 * hour, c: NewConsult{PatientName: "ZZTEST,PAPA", PatientRef: "DEMO-0016", ToServiceID: svcHeme,
		FromLocation: "PRIMARY CARE CLINIC A", RequestingProvider: pcProvider, Urgency: "WITHIN 48 HOURS", Place: "CONSULTANT'S CHOICE",
		ReasonForRequest: "Platelets 62k, new finding. Please advise.", ProvisionalDiagnosis: "Thrombocytopenia, unspecified"}},
}

// SeedIfEmpty loads the demo data when there are no consults yet.
func (s *Store) SeedIfEmpty(ctx context.Context, now time.Time) (bool, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM consults`).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	return true, s.Reset(ctx, now)
}

// Reset replaces all data with the demo scenarios, timed relative to now.
func (s *Store) Reset(ctx context.Context, now time.Time) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// TRUNCATE does not fire the append-only row trigger.
		if _, err := tx.Exec(ctx, `TRUNCATE consult_activities, consult_notes, consults, services RESTART IDENTITY`); err != nil {
			return err
		}
		for _, sv := range seedServices {
			if _, err := tx.Exec(ctx, `INSERT INTO services (id, name, usage, ifc_routing_site, synthetic)
				VALUES ($1, $2, $3, NULLIF($4, ''), $5)`, sv.ID, sv.Name, sv.Usage, sv.IFCRoutingSite, sv.Synthetic); err != nil {
				return err
			}
		}
		for i, sc := range scenarios {
			if err := runScenario(ctx, tx, sc, now); err != nil {
				return fmt.Errorf("seed scenario %d (%s): %w", i+1, sc.c.PatientName, err)
			}
		}
		return nil
	})
}

func runScenario(ctx context.Context, tx pgx.Tx, sc scenario, now time.Time) error {
	c := sc.c
	if sc.cid > 0 {
		cid := now.Add(-sc.cid).Truncate(day)
		c.ClinicallyIndicated = &cid
	}
	id, err := createConsult(ctx, tx, c, now.Add(-sc.ago))
	if err != nil {
		return err
	}
	for _, st := range sc.steps {
		in := st.in
		if in.NoteID == -1 { // sign the note linked earlier in this scenario
			if err := tx.QueryRow(ctx, `SELECT id FROM consult_notes WHERE consult_id = $1 AND NOT signed ORDER BY id LIMIT 1`, id).
				Scan(&in.NoteID); err != nil {
				return err
			}
		}
		if _, err := applyAction(ctx, tx, id, in, now.Add(-st.ago)); err != nil {
			return fmt.Errorf("%s: %w", in.Action, err)
		}
	}
	return nil
}
