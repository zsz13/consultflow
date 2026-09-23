package workflow

// RuleKind says how firmly a rule is grounded in the legacy source.
type RuleKind string

const (
	KindLegacy     RuleKind = "LEGACY"     // behavior reproduced from GMRC code
	KindDerived    RuleKind = "DERIVED"    // modern rule built on legacy data or behavior
	KindAssumption RuleKind = "ASSUMPTION" // not established by the source
)

// LegacyRef points at a line of legacy source. Snippet is that line, trimmed;
// trace_test.go verifies this against the VistA tree.
type LegacyRef struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// Rule is one traceable business rule.
type Rule struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Kind     RuleKind    `json:"kind"`
	Behavior string      `json:"behavior"`
	Legacy   []LegacyRef `json:"legacy"`
	Modern   []string    `json:"modern"`
}

const (
	routines = "Packages/Consult Request Tracking/Routines/"
	globals  = "Packages/Consult Request Tracking/Globals/"
)

var rules = []Rule{
	{
		ID: "R-STATUS", Title: "Status is an ORDER STATUS pointer", Kind: KindLegacy,
		Behavior: "CPRS STATUS (0-node piece 12) points into ORDER STATUS #100.01. GMRC uses 1 dc, 2 comp, 5 pend, 6 actv, 8 schd, 9 part, 13 canc.",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIA.m", 94, `S GMRCSTS=$P(^ORD(100.01,$P(^GMR(123,GMRCO,0),"^",12),0),U,2)`},
			{"Packages/Order Entry Results Reporting/Globals/100.01+ORDER STATUS.zwr", 143, `^ORD(100.01,13,0)="CANCELLED^canc"`},
		},
		Modern: []string{"backend/internal/workflow/model.go: statuses", "backend/internal/store/schema.sql: consults.status CHECK"},
	},
	{
		ID: "R-OPEN", Title: "Active work list = pending, active, scheduled, partial results", Kind: KindLegacy,
		Behavior: "The service's active consult list gathers pending, active, scheduled and incomplete (partial results) consults.",
		Legacy:   []LegacyRef{{routines + "GMRCSTL1.m", 1, `GMRCSTL1 ;SLC/DCM,dee;MA - List Manager Format Routine - Get Active Consults by service - pending,active,scheduled,incomplete,etc. ;11/13/02 08:30`}},
		Modern:   []string{"backend/internal/workflow/model.go: Status.Open", "backend/internal/api/api.go: dashboard buckets"},
	},
	{
		ID: "R-CREATE", Title: "A new consult is filed as PENDING", Kind: KindLegacy,
		Behavior: "NEW^GMRCGUIA files the order with status 5 (pending). Action type 1 is superseded by 2 CPRS RELEASED ORDER after patch 21.",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIA.m", 24, `S (DA,GMRCO)=+Y,GMRCSTS=5,GMRCA=1,DIE=DIC`},
			{globals + "123.1+REQUEST ACTION TYPES.zwr", 4, `^GMR(123.1,1,0)="ENTERED IN CPRS^^^USE CPRS RELEASE ORDER AFTER PATCH #21^  "`},
		},
		Modern: []string{"backend/internal/store/store.go: CreateConsult", "backend/internal/workflow/decide.go: ValidateNew"},
	},
	{
		ID: "R-DUPLICATE", Title: "Duplicate order warning", Kind: KindLegacy,
		Behavior: "Warn when the same patient already has a pending, active or scheduled consult to the same service and procedure within 12 months; the user may continue anyway.",
		Legacy: []LegacyRef{
			{routines + "GMRCDPCK.m", 10, `S X1=DT,X2=-365 D C^%DTC S GMRCMDT=9999999-X ;Only look back 12 months for duplicates`},
			{routines + "GMRCDPCK.m", 11, `F STS=5,6,8 S GMRCDATE=0 D`},
		},
		Modern: []string{"backend/internal/workflow/attention.go: IsDuplicateCandidate", "backend/internal/store/store.go: CreateConsult (acknowledgeDuplicate)"},
	},
	{
		ID: "R-RECEIVE", Title: "Receive only from PENDING", Kind: KindLegacy,
		Behavior: "Receive is refused unless the consult is pending; it sets status 6 (active) and records action 21 RECEIVED.",
		Legacy: []LegacyRef{
			{routines + "GMRCA1.m", 77, `I '$G(GMRCSCH),$P(GMRC(0),"^",12)'=5 D  Q`},
			{routines + "GMRCA1.m", 100, `. I $P(GMRC(0),"^",12)=5 S GMRCSTS=6,$P(GMRC(0),"^",12)=GMRCSTS`},
			{routines + "GMRCGUIA.m", 57, `S GMRCSTS=6,GMRCA=21`},
		},
		Modern: []string{"backend/internal/workflow/model.go: actionDefs[Receive]", "backend/internal/workflow/decide.go: guard"},
	},
	{
		ID: "R-SCHEDULE", Title: "Schedule only from PENDING or ACTIVE", Kind: KindLegacy,
		Behavior: "Schedule is refused unless status is 5 or 6; it sets status 8 and action 8 SCHEDULED.",
		Legacy: []LegacyRef{
			{routines + "GMRCA1.m", 82, `I $G(GMRCSCH),"56"'[$P(GMRC(0),"^",12) D  Q`},
			{routines + "GMRCA1.m", 102, `I $G(GMRCSCH) S GMRCA=8,GMRCSTS=8`},
			{routines + "GMRCGUIB.m", 164, `S GMRCSTS=8,GMRCA=8`},
		},
		Modern: []string{"backend/internal/workflow/model.go: actionDefs[Schedule]"},
	},
	{
		ID: "R-FORWARD", Title: "Forward resets to PENDING at the new service", Kind: KindLegacy,
		Behavior: "Forward is refused for completed, discontinued or cancelled consults and when the target is the current service. It sets the new service, status 5 and action 17, keeps urgency unless changed, and clears ATTENTION unless a new one is given.",
		Legacy: []LegacyRef{
			{routines + "GMRCAFRD.m", 25, `I $P(^GMR(123,GMRCO,0),"^",12)<3 S GMRCMSG="NO ACTION POSSIBLE. This Consult Has Already Been Completed Or Discontinued." D EXAC^GMRCADC(GMRCMSG),END S GMRCQUT=1 Q`},
			{routines + "GMRCAFRD.m", 57, `S GMRCFF=$P(^GMR(123,GMRCO,0),"^",5) I GMRCFF=+GMRCDG S GMRCMSG="The Forwarding Service Cannot Forward A Consult To Itself!" D EXAC^GMRCADC(GMRCMSG),END S GMRCQUT=1 Q`},
			{routines + "GMRCGUIA.m", 163, `S GMRCA=17,GMRCSTS=5`},
			{routines + "GMRCGUIA.m", 173, `S DR=DR_"1////^S X=$G(GMRCSS);5////^S X=$G(GMRCURGI);8////^S X=$G(GMRCSTS);9////^S X=$G(GMRCA);.1///@"_$S($L($G(GMRCATTN)):";7////^S X=GMRCATTN",1:";7///@")`},
			{routines + "GMRCASV.m", 50, `. I GMRCEXCL=9 S GMRCMSG="You have selected a disabled service!" D EXAC^GMRCADC(GMRCMSG) K GMRCMSG Q`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: decideForward"},
	},
	{
		ID: "R-FORWARD-IFC", Title: "Forward to a remote (IFC) service", Kind: KindLegacy,
		Behavior: "Forwarding to a service with an IFC routing facility records action 25 FWD TO REMOTE SERVICE and makes this facility the placer. An inter-facility consult may not be forwarded to another IFC service.",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIA.m", 171, `. S GMRCA=25,GMRCIROL="P"`},
			{routines + "GMRCASV.m", 51, `. I GMRCEXCL=3 S GMRCMSG="You may not forward this Inter-facility Consult to another inter-facility consult service." D EXAC^GMRCADC(GMRCMSG) K GMRCMSG Q`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: decideForward"},
	},
	{
		ID: "R-PARTIAL-BLOCKS", Title: "Partial results / unsigned notes block forward, DC and cancel", Kind: KindLegacy,
		Behavior: "With status 9 (partial results) or an unsigned note linked, forward is refused; discontinue and cancel are refused until the results are removed.",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIA.m", 142, `I $P(^GMR(123,+GMRCO,0),U,12)=9 S GMRCERR=1,GMRCERMS="Invalid action. This consult has partial results."`},
			{routines + "GMRCGUIA.m", 147, `. . S GMRCERMS="Invalid Action. This consult has an unsigned note.",GMRCERR=1`},
			{routines + "GMRCADC.m", 40, `I $P(GMRC(0),"^",12)=9 S GMRCMSG="Action invalid. This consult has partial results!",GMRCMSG(1)="Remove the associated results and then discontinue." D EXAC(.GMRCMSG) S GMRCQUT=1 Q`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: guard, statusConflict", "backend/internal/workflow/attention.go: UNSIGNED_NOTE flag"},
	},
	{
		ID: "R-DC-CANCEL", Title: "Discontinue / cancel (deny) need a comment", Kind: KindLegacy,
		Behavior: "Discontinue (action 6, status 1) and cancel/deny (action 19, status 13) require a comment. The List Manager path (GMRCADC) refuses consults already discontinued, completed, cancelled or with partial results; the GUI path checks only discontinued and completed, and the CPRS order-DC path checks nothing. ConsultFlow applies the List Manager guards to every role. The ordering side can discontinue through CPRS (DC order control).",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIA.m", 93, `I '$D(GMRCOM) S GMRCERR=1,GMRCERMS="Comments are required for this action." D EXIT Q GMRCERR_"^"_GMRCERMS`},
			{routines + "GMRCGUIA.m", 98, `S GMRCA=$S(GMRCACTM="DC":6,1:19),GMRCSTS=$S(GMRCA=6:1,1:13)`},
			{routines + "GMRCADC.m", 41, `I $P(GMRC(0),"^",12)=13 S GMRCMSG="This consult has already been cancelled!" D EXAC(GMRCMSG) S GMRCQUT=1 Q`},
			{routines + "GMRCHL7A.m", 115, `I $S(GMRCTRLC="CA":1,GMRCTRLC="DC":1,1:0) D DC^GMRCHL7B(GMRCO,GMRCTRLC),RETURN^GMRCHL7U(GMRCO,GMRCTRLC)`},
		},
		Modern: []string{"backend/internal/workflow/model.go: actionDefs[Discontinue,Cancel]"},
	},
	{
		ID: "R-RESUBMIT", Title: "Only a cancelled consult can be edited and resubmitted", Kind: KindLegacy,
		Behavior: "Edit/resubmit is allowed only in status 13, for the ordering provider or a service update user. It sets status 5, records action 11 and logs previous values of edited fields in the activity comment.",
		Legacy: []LegacyRef{
			{routines + "GMRCEDT2.m", 10, `I $S($P(^GMR(123,GMRCO,0),"^",12)'=13:1,$D(GMRCRSUB):1,1:0) D  Q`},
			{routines + "GMRCEDT3.m", 10, `S DIE="^GMR(123,",DA=GMRCDA,DR="8////^S X=5" D ^DIE K DIE,DA,DR`},
			{routines + "GMRCEDIT.m", 52, `I DUZ=$P(^GMR(123,+GMRCIEN,0),"^",14) Q 1`},
			{routines + "GMRCA1.m", 42, `. S GMRCMSG="0^This consult is no longer editable."`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: decideResubmit"},
	},
	{
		ID: "R-RESUBMIT-ROUTING", Title: "Resubmitting to another service re-routes IFC", Kind: KindAssumption,
		Behavior: "When edit/resubmit changes the destination, ConsultFlow routes it like a new order: an IFC service makes this site the placer, and moving a placed IFC to a local service makes it local. The GMRC edit routines read here do not show how legacy handles this case.",
		Legacy:   []LegacyRef{{routines + "GMRCGUIA.m", 171, `. S GMRCA=25,GMRCIROL="P"`}},
		Modern:   []string{"backend/internal/workflow/decide.go: decideResubmit"},
	},
	{
		ID: "R-NOTE-STATUS", Title: "Notes drive PARTIAL RESULTS and COMPLETE", Kind: KindLegacy,
		Behavior: "An incomplete (unsigned) note records action 9 and status 9; a signed note records action 10 and status 2. A completed consult never drops back to partial results. A new note on a completed consult that already has linked results is action 14; with no linked results (e.g. after administrative complete) it is 10.",
		Legacy: []LegacyRef{
			{routines + "GMRCTIU.m", 12, `S GMRCA=$S($G(GMRCTUST)["INCOMPLETE":9,1:10),GMRCSTS=$S(GMRCA=10:2,1:9)`},
			{routines + "GMRCTIU1.m", 89, `I GMRCOSTS=2,GMRCSTS=9 S GMRCSTS=2`},
			{routines + "GMRCTIU1.m", 154, `I '$D(^GMR(123,+GMRCO,50)) Q 10`},
			{routines + "GMRCTIU1.m", 156, `I '$D(^GMR(123,+GMRCO,50,"B",GMRCRSLT)) Q 14`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: decideAddNote, decideSignNote"},
	},
	{
		ID: "R-NOTE-GUARD", Title: "No notes on discontinued or cancelled consults", Kind: KindLegacy,
		Behavior: "A result note cannot be entered for a discontinued or cancelled consult.",
		Legacy:   []LegacyRef{{routines + "GMRCTIUE.m", 128, `I $S(STATUS=1:1,STATUS=13:1,1:0) D`}},
		Modern:   []string{"backend/internal/workflow/model.go: actionDefs[AddNote,SignNote].From"},
	},
	{
		ID: "R-ADMIN-COMPLETE", Title: "Administrative complete", Kind: KindLegacy,
		Behavior: "Administrative users can complete without a note unless the consult is discontinued, completed or cancelled. It sets status 2 with action 10, and the GUI requires comment text.",
		Legacy: []LegacyRef{
			{routines + "GMRCAAC.m", 14, `I $S(GMRCSTS<3:1,GMRCSTS=13:1,1:0) D  Q`},
			{routines + "GMRCAAC.m", 31, `S GMRCSTS=2,GMRCA=10`},
			{routines + "GMRCGUIB.m", 101, `I GMRCA=10 D  I GMRCERR=1 S GMRCERMS="Comment field must contain a text value!" Q GMRCERR_"^"_GMRCERMS`},
			{routines + "GMRCTIUE.m", 30, `I GMRCAU=3 D  Q`},
		},
		Modern: []string{"backend/internal/workflow/model.go: actionDefs[AdminComplete]"},
	},
	{
		ID: "R-COMMENT", Title: "Comments never change status", Kind: KindLegacy,
		Behavior: "Adding a comment records action 20 and updates LAST ACTION only; the status is untouched and there is no status guard.",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIB.m", 47, `S GMRCA=20,GMRCAD=GMRCWHN S:$G(GMRCWHO) GMRCORNP=GMRCWHO`},
			{routines + "GMRCGUIB.m", 55, `. S GMRCSTS="",GMRCDR="9////20"`},
		},
		Modern: []string{"backend/internal/workflow/model.go: actionDefs[Comment]"},
	},
	{
		ID: "R-SIG-FINDINGS", Title: "Significant findings Y / N / U", Kind: KindLegacy,
		Behavior: "Significant findings are Y, N or U. Updating them records action 4 and does not change status.",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIB.m", 79, `;                                : 'N'= no significant finding`},
			{routines + "GMRCGUIB.m", 118, `I $L(GMRCA),GMRCA=4 S DR=DR_$S($L(DR):";",1:"")_"9////^S X=GMRCA;15////^S X=GMRCSF" D`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: Decide (SigFindings)"},
	},
	{
		ID: "R-IFC-PLACER", Title: "Requesting facility cannot work an IFC", Kind: KindLegacy,
		Behavior: "When this facility is the IFC placer (node 12 piece 5 = P), service-side actions (receive, schedule, forward, cancel, discontinue from the service screens, notes, completion, significant findings) are refused. Comments and edit/resubmit remain, and the ordering provider can still discontinue the order through CPRS (DC^GMRCHL7B has no IFC check; the IFC event handler covers an IFC discontinued before it was sent).",
		Legacy: []LegacyRef{
			{routines + "GMRCA1.m", 59, `. W !,"The requesting facility may not take this action on an "`},
			{routines + "GMRCTIUE.m", 15, `. W !,"The requesting facility may not complete an inter-facility "`},
			{routines + "GMRCHL7B.m", 72, `DC(GMRCO,ACTRL) ;Discontinue request from OERR`},
			{routines + "GMRCIEVT.m", 22, `.. ;complete all transactions if IFC DC'd before request ever sent`},
			{routines + "GMRCACTM.m", 37, `.I $P($G(^GMR(123,GMRCIEN,12)),U,5)="P" D  Q  ;IFC placer so only ED/RES`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: guard"},
	},
	{
		ID: "R-IFC-FILLER", Title: "Remote requests arrive as REMOTE REQUEST RECEIVED", Kind: KindLegacy,
		Behavior: "When another facility places an IFC here, the new consult's last action is 23 REMOTE REQUEST RECEIVED (24 when it is part of a forward). This facility is the filler and works the consult normally.",
		Legacy:   []LegacyRef{{routines + "GMRCIACT.m", 59, `S GMRCFDA(9)=$S($P(GMRCORC,"|",16)["FI":24,1:23),GMRCLAC=GMRCFDA(9)`}},
		Modern:   []string{"backend/internal/workflow/model.go: CreateLegacyAction", "backend/internal/store/seed.go: IFC filler scenario"},
	},
	{
		ID: "R-AUTO-DC", Title: "Cancelled consults auto-discontinue after 31 days", Kind: KindDerived,
		Behavior: "Legacy: an overnight TaskMan job, when enabled by the CSLT CANCELLED TO DISCONTINUED parameter, discontinues consults cancelled within a configured window of days, through the normal DC API with an ADC comment and the cancelling provider as responsible person. ConsultFlow assumes a fixed 31 days (the routine title), no upper window, and records the job itself as the actor.",
		Legacy: []LegacyRef{
			{routines + "GMRCCXDC.m", 1, `GMRCCXDC ;ABV/MKN - Convert cancelled consults to discontinued after 31 days ;8/14/2018 9:35`},
			{routines + "GMRCCXDC.m", 9, `S X=$E($$GET^XPAR("PKG.CONSULT/REQUEST TRACKING","CSLT CANCELLED TO DISCONTINUED","Is the overnight cancelled to discontinued job active?","E"))`},
			{routines + "GMRCCXDC.m", 18, `S GMRCDT1=$$GET^XPAR("PKG.CONSULT/REQUEST TRACKING","CSLT CANCELLED TO DISCONTINUED","How many days back to start with?","E")`},
			{routines + "GMRCCXDC.m", 24, `S GMRCCOM(1)="ADC:Consult automatically discontinued "_GMRCDT1_" days after cancellation"`},
			{routines + "GMRCCXDC.m", 35, `...S Y=$$DC^GMRCGUIA(GMRCIEN,GMRCPROV,GMRCNOW,"DC",.GMRCCOM)`},
		},
		Modern: []string{"backend/internal/workflow/decide.go: AutoDiscontinue", "backend/internal/store/store.go: RunAutoDiscontinue", "POST /api/jobs/auto-discontinue"},
	},
	{
		ID: "R-CANC-QUIRK", Title: "Legacy quirk: DC guard misses cancelled", Kind: KindLegacy,
		Behavior: "DC^GMRCGUIA compares the status abbreviation with \"ca\", but ORDER STATUS uses \"canc\", so the GUI DC path does not block cancelled consults. The auto-DC job relies on this. ConsultFlow blocks users and allows only the system job.",
		Legacy: []LegacyRef{
			{routines + "GMRCGUIA.m", 96, `I GMRCSTS="ca" S GMRCERR=1,GMRCERMS="Order Has Already Been Cancelled." D EXIT Q GMRCERR_"^"_GMRCERMS`},
			{"Packages/Order Entry Results Reporting/Globals/100.01+ORDER STATUS.zwr", 143, `^ORD(100.01,13,0)="CANCELLED^canc"`},
		},
		Modern: []string{"backend/internal/workflow/model.go: actionDefs[Discontinue].From excludes CANCELLED"},
	},
	{
		ID: "R-AUDIT", Title: "Every action is an audit activity", Kind: KindLegacy,
		Behavior: "Each action adds a REQUEST PROCESSING ACTIVITY entry (node 40): entered date/time, action type, actual date/time, responsible person, entered by, and optional comment.",
		Legacy:   []LegacyRef{{routines + "GMRCP.m", 41, `. S DR=".01////^S X=GMRCDT;1////^S X=GMRCA;2////^S X=GMRCAD;3////^S X=GMRCORNP"`}},
		Modern:   []string{"backend/internal/store/schema.sql: consult_activities (append-only trigger)", "backend/internal/store/store.go: ApplyAction"},
	},
	{
		ID: "R-LOCK", Title: "Record lock during status update", Kind: KindLegacy,
		Behavior: "Status updates lock the consult record and fail if another user holds it.",
		Legacy:   []LegacyRef{{routines + "GMRCP.m", 91, `L +^GMR(123,GMRCO):2 I '$T S GMRCQUT=1,GMRCERR=1,GMRCERMS="Unable to update status and last action - Consult In Use By Another User." Q`}},
		Modern:   []string{"backend/internal/store/store.go: ApplyAction (SELECT ... FOR UPDATE)"},
	},
	{
		ID: "R-COMPLETION-WINDOW", Title: "30 / 60-day completion window", Kind: KindDerived,
		Behavior: "The consult performance monitor counts completion within 30 or 60 days of the clinically indicated date, or the date of request when that is empty. ConsultFlow flags open consults that are past those windows.",
		Legacy: []LegacyRef{
			{routines + "GMRCSTL8.m", 175, `CHKRNG ;check if request is complete within 30/60 days of Desired Date or Date of Request`},
			{routines + "GMRCSTL8.m", 179, `S:$G(DTOR)="" DTOR=$P(^GMR(123,+$G(GMRCPT),0),U,7)`},
		},
		Modern: []string{"backend/internal/workflow/attention.go: Assess (PAST_30_DAYS, PAST_60_DAYS)"},
	},
	{
		ID: "R-URGENCY-WINDOW", Title: "Not received within the urgency window", Kind: KindAssumption,
		Behavior: "Urgency values come from the source. Windows for 24/48/72 hours, 1 week and 1 month come from the names. STAT, EMERGENCY and TODAY = 24h, and ROUTINE / NEXT AVAILABLE = 7 days, are assumptions. Legacy has no such alert. The window restarts when the consult is forwarded, because the new service has not seen it.",
		Legacy:   []LegacyRef{{routines + "GMRCHL7A.m", 9, `S X=$S(X="S":"STAT",X="R":"ROUTINE",X="ZT":"TODAY",X="Z24":"WITHIN 24 HOURS",X="Z48":"WITHIN 48 HOURS",X="Z72":"WITHIN 72 HOURS",X="ZW":"WITHIN 1 WEEK",X="ZM":"WITHIN 1 MONTH",X="ZNA":"NEXT AVAILABLE",1:X)`}},
		Modern:   []string{"backend/internal/workflow/model.go: urgencyWindows", "backend/internal/workflow/attention.go: NOT_RECEIVED"},
	},
	{
		ID: "R-ROLES", Title: "Simplified user roles", Kind: KindAssumption,
		Behavior: "GMRC grants review, update or administrative authority per service. ConsultFlow reduces this to Requester, Service user, Service admin and System, with no authentication. Simplifications: any service user may forward to a tracking-only service (legacy limits that to the tracking service's update users), and a service administrator may also link notes (legacy routes a pure administrative user to administrative complete).",
		Legacy:   []LegacyRef{{routines + "GMRCASV.m", 41, `I $G(GMRCTO)=1 S DIC("S")="I ($$VALID^GMRCAU(Y,DUZ)&($P($G(^GMR(123.5,Y,0)),U,2)=2))!($P($G(^GMR(123.5,Y,0)),U,2)="""")!($P($G(^GMR(123.5,Y,0)),U,2)=1)"`}, {routines + "GMRCTIUE.m", 30, `I GMRCAU=3 D  Q`}, {routines + "GMRCACTM.m", 17, `;       3 - user has administrative update capabilities`}},
		Modern:   []string{"backend/internal/workflow/model.go: Role, actionDefs.Roles"},
	},
	{
		ID: "R-FIELDS", Title: "Consult fields as displayed by CPRS", Kind: KindLegacy,
		Behavior: "Detail display: To Service (piece 5), From Service (piece 6, a hospital location), Requesting Provider (14), Urgency (9), Place (10), Clinically Ind. Date (24), Inpatient/Outpatient (18).",
		Legacy: []LegacyRef{
			{routines + "GMRCSLM2.m", 62, `S ^TMP("GMRCR",$J,"DT",GMRCCT,0)="From Service:"_$E(TAB,1,10)_$P($G(^SC(+$P(GMRCO(0),"^",6),0)),"^"),GMRCCT=GMRCCT+1`},
			{routines + "GMRCSLM2.m", 69, `S ^TMP("GMRCR",$J,"DT",GMRCCT,0)="Clinically Ind. Date:"_$E(TAB,1,2)_$$FMTE^XLFDT($P($G(GMRCO(0)),"^",24),1),GMRCCT=GMRCCT+1 ;WAT/66/81`},
		},
		Modern: []string{"backend/internal/store/schema.sql: consults", "frontend/src/pages/ConsultDetail.tsx"},
	},
}

// Rules returns the traceability registry.
func Rules() []Rule { return append([]Rule(nil), rules...) }

// RuleByID returns the rule with the given id.
func RuleByID(id string) (Rule, bool) {
	for _, r := range rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}
