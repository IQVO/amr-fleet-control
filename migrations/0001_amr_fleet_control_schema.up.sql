-- fleets: one row per fleet; every write locks it (SELECT ... FOR UPDATE), so reservations,
-- robots and missions change serially (decision D18). reservations is fleetmap.State.
CREATE TABLE fleets (
    fleet_id     TEXT        PRIMARY KEY,
    map_version  TEXT        NOT NULL,
    reservations JSONB       NOT NULL,
    capacity     JSONB,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- robots: the registry (decision D13); snapshot is robot.Snapshot.
CREATE TABLE robots (
    robot_key     TEXT        PRIMARY KEY,
    manufacturer  TEXT        NOT NULL,
    serial_number TEXT        NOT NULL,
    state         TEXT        NOT NULL,
    snapshot      JSONB       NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- missions: legs and internal CHARGE/PARK missions; state is mission.State.
CREATE TABLE missions (
    mission_id   TEXT        PRIMARY KEY,
    seq          BIGINT      GENERATED ALWAYS AS IDENTITY UNIQUE,
    kind         TEXT        NOT NULL CHECK (kind IN ('LEG', 'CHARGE', 'PARK')),
    status       TEXT        NOT NULL,
    open         BOOLEAN     NOT NULL,
    container_id TEXT        NOT NULL DEFAULT '',
    map_version  TEXT        NOT NULL,
    state        JSONB       NOT NULL,
    accepted_at  TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_missions_open ON missions (seq) WHERE open;
-- one open leg per container (ports.ErrOpenLegExists -> 422 container-busy)
CREATE UNIQUE INDEX uq_missions_open_leg_container ON missions (container_id) WHERE kind = 'LEG' AND open;

-- outbox_events: the fleet's transactional outbox shape.
CREATE TABLE outbox_events (
    id           BIGSERIAL   PRIMARY KEY,
    event_id     TEXT        NOT NULL,
    topic        TEXT        NOT NULL,
    event_type   TEXT        NOT NULL,
    subject      TEXT        NOT NULL,
    key          BYTEA,
    dataschema   TEXT        NOT NULL,
    value        BYTEA       NOT NULL,
    headers      JSONB       NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ,
    attempts     INTEGER     NOT NULL DEFAULT 0,
    last_error   TEXT,
    CONSTRAINT outbox_events_event_id_topic_key UNIQUE (event_id, topic)
);
CREATE INDEX idx_outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- idempotency_keys: stored answers of POSTs (2xx and FINAL 4xx only; never RETRYABLE, never 5xx).
CREATE TABLE idempotency_keys (
    key          TEXT PRIMARY KEY,
    fingerprint  TEXT        NOT NULL,
    status       INTEGER     NOT NULL CHECK (status >= 200 AND status < 500),
    content_type TEXT        NOT NULL DEFAULT '',
    body         BYTEA       NOT NULL DEFAULT ''::bytea,
    created_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_idempotency_keys_created_at ON idempotency_keys (created_at);
