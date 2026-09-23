-- ConsultFlow schema. Idempotent; applied at startup.
-- Column comments name the GMRC field each column models (file #123).

CREATE TABLE IF NOT EXISTS services (                -- REQUEST SERVICES #123.5
    id               BIGINT PRIMARY KEY,             -- legacy IEN where real; >= 1000 synthetic
    name             TEXT NOT NULL UNIQUE,
    usage            TEXT NOT NULL DEFAULT ''
                     CHECK (usage IN ('', 'GROUPER', 'TRACKING', 'DISABLED')),
    ifc_routing_site TEXT,                           -- "IFC" node routing facility
    synthetic        BOOLEAN NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS consults (                -- REQUEST/CONSULTATION #123
    id                        BIGSERIAL PRIMARY KEY,
    patient_name              TEXT NOT NULL,         -- .02 (synthetic only)
    patient_ref               TEXT NOT NULL,         -- synthetic identifier
    to_service_id             BIGINT NOT NULL REFERENCES services(id),  -- 1 TO SERVICE
    from_location             TEXT NOT NULL,         -- 2 FROM (piece 6)
    requesting_provider       TEXT NOT NULL,         -- 10 SENDING PROVIDER
    attention                 TEXT NOT NULL DEFAULT '',  -- 7 ATTENTION
    urgency                   TEXT NOT NULL,         -- 5 URGENCY
    place                     TEXT NOT NULL DEFAULT '',  -- 6 PLACE OF CONSULTATION
    inpatient_outpatient      CHAR(1) NOT NULL DEFAULT 'O' CHECK (inpatient_outpatient IN ('I', 'O')),  -- 14
    request_type              TEXT NOT NULL DEFAULT 'CONSULT' CHECK (request_type IN ('CONSULT', 'PROCEDURE')),  -- 13
    procedure_name            TEXT NOT NULL DEFAULT '',  -- 4 PROCEDURE
    reason_for_request        TEXT NOT NULL,         -- 20 REASON FOR REQUEST
    provisional_diagnosis     TEXT NOT NULL DEFAULT '',  -- 30
    date_of_request           TIMESTAMPTZ NOT NULL,  -- 3 DATE OF REQUEST
    clinically_indicated_date TIMESTAMPTZ,           -- 17 (piece 24)
    status                    TEXT NOT NULL CHECK (status IN
                              ('PENDING', 'ACTIVE', 'SCHEDULED', 'PARTIAL_RESULTS', 'COMPLETE', 'DISCONTINUED', 'CANCELLED')),  -- 8 CPRS STATUS
    status_changed_at         TIMESTAMPTZ NOT NULL,
    last_action               TEXT NOT NULL,         -- 9 LAST ACTION TAKEN
    significant_findings      CHAR(1) CHECK (significant_findings IN ('Y', 'N', 'U')),  -- 15
    ifc_role                  CHAR(1) CHECK (ifc_role IN ('P', 'F')),  -- node 12 piece 5
    ifc_remote_site           TEXT NOT NULL DEFAULT '',
    ifc_remote_consult_id     TEXT NOT NULL DEFAULT '',
    created_at                TIMESTAMPTZ NOT NULL,
    updated_at                TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS consults_status_idx ON consults (status);
CREATE INDEX IF NOT EXISTS consults_duplicate_idx ON consults (patient_ref, to_service_id, procedure_name);

CREATE TABLE IF NOT EXISTS consult_notes (           -- node 50 associated results (TIU notes)
    id         BIGSERIAL PRIMARY KEY,
    consult_id BIGINT NOT NULL REFERENCES consults(id),
    title      TEXT NOT NULL,
    author     TEXT NOT NULL,
    signed     BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    signed_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS consult_notes_consult_idx ON consult_notes (consult_id);

CREATE TABLE IF NOT EXISTS consult_activities (      -- node 40 REQUEST PROCESSING ACTIVITY
    id                 BIGSERIAL PRIMARY KEY,
    consult_id         BIGINT NOT NULL REFERENCES consults(id),
    action             TEXT NOT NULL,
    legacy_action_ien  INT NOT NULL,                 -- 1 ACTIVITY (-> #123.1)
    legacy_action_name TEXT NOT NULL,
    previous_status    TEXT,                         -- NULL for the creating activity
    new_status         TEXT NOT NULL,
    actor              TEXT NOT NULL,                -- 3 RESPONSIBLE PERSON / 4 ENTERED BY
    actor_role         TEXT NOT NULL,
    occurred_at        TIMESTAMPTZ NOT NULL,         -- 2 DATE/TIME OF ACTUAL ACTIVITY
    recorded_at        TIMESTAMPTZ NOT NULL DEFAULT now(),  -- .01 DATE/TIME OF ACTION ENTRY
    summary            TEXT NOT NULL,
    comment            TEXT NOT NULL DEFAULT '',     -- 5 COMMENT (word processing)
    details            JSONB NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS consult_activities_consult_idx ON consult_activities (consult_id, occurred_at, id);

-- The activity log is append-only: it is the audit trail.
CREATE OR REPLACE FUNCTION consult_activities_append_only() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'consult_activities is append-only (% refused)', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS consult_activities_append_only ON consult_activities;
CREATE TRIGGER consult_activities_append_only
    BEFORE UPDATE OR DELETE ON consult_activities
    FOR EACH ROW EXECUTE FUNCTION consult_activities_append_only();
