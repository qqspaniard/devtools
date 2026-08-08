-- Control Room schema, migration 0001: initial schema.
--
-- Design principles (from the RFC):
--   * `events` is the authoritative, append-only audit history. Application
--     code never UPDATEs or DELETEs a row here.
--   * The other tables are transactional projections used for efficient
--     rendering and, more importantly, for enforcing uniqueness/replay
--     constraints (a claim is single-use because a UNIQUE index says so, not
--     because application code remembers to check).
--   * Foreign keys are declared and enforced (PRAGMA foreign_keys is set by the
--     store on every connection).
--   * Timestamps are stored as RFC3339Nano UTC text, matching the approval
--     digest's expiration encoding, so comparisons are lexicographic and exact.

-- schema_migrations records applied migration versions. The store checks this
-- at startup and refuses to run against a newer schema than it understands
-- (fail closed, no destructive reset).
CREATE TABLE schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT    NOT NULL
);

-- sessions is the projection of session lifecycle state. `state` is one of the
-- policy.State values; transitions are validated in Go and applied inside the
-- same transaction as the event that caused them.
CREATE TABLE sessions (
    id             TEXT    PRIMARY KEY,
    state          TEXT    NOT NULL,
    workspace_id   TEXT    NOT NULL,
    workspace_name TEXT    NOT NULL DEFAULT '',
    current_revision INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
);

-- plan_revisions stores each published plan revision's canonical payload. The
-- payload is the exact validated JSON the agent published; it is the source of
-- truth the review page renders and the approval digest binds. (session_id,
-- revision) is unique so a revision number cannot be published twice.
CREATE TABLE plan_revisions (
    session_id  TEXT    NOT NULL REFERENCES sessions(id),
    revision    INTEGER NOT NULL,
    goal        TEXT    NOT NULL,
    summary     TEXT    NOT NULL DEFAULT '',
    payload     TEXT    NOT NULL,   -- full validated plan JSON
    created_at  TEXT    NOT NULL,
    PRIMARY KEY (session_id, revision)
);

-- decisions records the durable outcome of a user's review of a specific
-- revision: approve, reject, or request_changes. It is what `decision poll`
-- returns. (session_id, revision) is unique: a revision receives exactly one
-- terminal decision. A stale-revision decision fails closed before insertion.
--
-- kind         : 'approve' | 'reject' | 'request_changes'
-- reason       : bounded free text for reject/request_changes (empty for approve)
-- approval_id  : FK into approvals when kind='approve', else NULL
CREATE TABLE decisions (
    session_id  TEXT    NOT NULL REFERENCES sessions(id),
    revision    INTEGER NOT NULL,
    kind        TEXT    NOT NULL,
    reason      TEXT    NOT NULL DEFAULT '',
    approval_id TEXT    REFERENCES approvals(id),
    created_at  TEXT    NOT NULL,
    PRIMARY KEY (session_id, revision)
);

-- approvals is the durable capability produced when a user approves. The digest
-- binds it to the exact revision/selection/workspace/envelope/expiration/max
-- claims via the Phase 0 policy. `digest` is UNIQUE so the same digest cannot be
-- recorded twice. `claims_consumed` is mutated only inside the atomic claim
-- transaction, guarded by max_claims.
CREATE TABLE approvals (
    id                 TEXT    PRIMARY KEY,   -- random opaque approval id
    session_id         TEXT    NOT NULL REFERENCES sessions(id),
    plan_revision      INTEGER NOT NULL,
    digest             TEXT    NOT NULL UNIQUE,
    allowed_action_ids TEXT    NOT NULL,      -- JSON array of selected action ids
    permission_envelope TEXT   NOT NULL,      -- JSON array of sorted risk classes
    expires_at         TEXT    NOT NULL,      -- RFC3339Nano UTC
    max_claims         INTEGER NOT NULL,
    claims_consumed    INTEGER NOT NULL DEFAULT 0,
    superseded         INTEGER NOT NULL DEFAULT 0,  -- 0/1; set when a newer approval lands for the session
    created_at         TEXT    NOT NULL
);

CREATE INDEX idx_approvals_session ON approvals(session_id);

-- claims records each successful, single-use consumption of an approval. A row
-- here is durable proof a claim happened; its existence is what makes replay
-- across a restart impossible. (approval_id, claim_seq) is unique, and the
-- number of rows for an approval can never exceed approvals.max_claims because
-- the claim transaction checks and increments claims_consumed atomically.
CREATE TABLE claims (
    approval_id  TEXT    NOT NULL REFERENCES approvals(id),
    claim_seq    INTEGER NOT NULL,   -- 1-based sequence within this approval
    claimed_at   TEXT    NOT NULL,
    PRIMARY KEY (approval_id, claim_seq)
);

-- events is the authoritative, append-only audit history. Rows are only ever
-- INSERTed. `payload` is redacted/bounded JSON; secrets and full command output
-- must never be written here. `parent_event_id` records causal order.
CREATE TABLE events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id      TEXT    NOT NULL,
    revision        INTEGER,
    actor           TEXT    NOT NULL,   -- 'user' | 'agent' | 'system'
    type            TEXT    NOT NULL,
    payload         TEXT    NOT NULL DEFAULT '{}',
    parent_event_id INTEGER REFERENCES events(id),
    created_at      TEXT    NOT NULL
);

CREATE INDEX idx_events_session ON events(session_id, id);
