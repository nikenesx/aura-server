-- Times are Unix milliseconds; JSON lives in TEXT. Both carry over to Postgres
-- as BIGINT and TEXT/JSONB without changing the code that reads them.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE TABLE appointments (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users (id),
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    filename       TEXT NOT NULL,
    audio_key      TEXT NOT NULL,
    audio_bytes    INTEGER NOT NULL,
    duration_sec   REAL,
    status         TEXT NOT NULL,
    progress       INTEGER NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    transcript     TEXT,
    roles          TEXT,
    waveform       TEXT,
    -- card_generated is what the model wrote; card is what the doctor sees and
    -- edits. Keeping both shows how often and where the model is corrected.
    card_generated TEXT,
    card           TEXT,
    card_edited_at INTEGER,
    card_error     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX appointments_user_created ON appointments (user_id, created_at DESC);
CREATE INDEX appointments_status ON appointments (status);
