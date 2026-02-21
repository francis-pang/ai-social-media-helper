CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE IF NOT EXISTS triage_decisions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    media_key       TEXT NOT NULL,
    filename        TEXT,
    media_type      TEXT,
    saveable        BOOLEAN NOT NULL,
    reason          TEXT,
    media_metadata  JSONB,
    embedding       vector(1024),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, media_key)
);

CREATE INDEX IF NOT EXISTS triage_decisions_embedding_idx
    ON triage_decisions USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS triage_decisions_user_idx
    ON triage_decisions (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS selection_decisions (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id          TEXT NOT NULL,
    user_id             TEXT NOT NULL,
    media_key           TEXT NOT NULL,
    filename            TEXT,
    media_type          TEXT,
    selected            BOOLEAN NOT NULL,
    exclusion_category  TEXT,
    exclusion_reason    TEXT,
    scene_group         TEXT,
    media_metadata      JSONB,
    embedding           vector(1024),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, media_key)
);

CREATE INDEX IF NOT EXISTS selection_decisions_embedding_idx
    ON selection_decisions USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS selection_decisions_user_idx
    ON selection_decisions (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS override_decisions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    media_key       TEXT NOT NULL,
    filename        TEXT,
    media_type      TEXT,
    action          TEXT NOT NULL,
    ai_verdict      TEXT,
    ai_reason       TEXT,
    is_finalized    BOOLEAN NOT NULL DEFAULT FALSE,
    media_metadata  JSONB,
    embedding       vector(1024),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS override_decisions_embedding_idx
    ON override_decisions USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS override_decisions_user_idx
    ON override_decisions (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS override_decisions_finalized_idx
    ON override_decisions (user_id, is_finalized, created_at DESC);

CREATE TABLE IF NOT EXISTS caption_decisions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    caption_text    TEXT NOT NULL,
    hashtags        TEXT[],
    location_tag    TEXT,
    media_keys      TEXT[],
    post_group_name TEXT,
    media_metadata  JSONB,
    embedding       vector(1024),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, post_group_name)
);

CREATE INDEX IF NOT EXISTS caption_decisions_embedding_idx
    ON caption_decisions USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS caption_decisions_user_idx
    ON caption_decisions (user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS publish_decisions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id      TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    platform        TEXT NOT NULL,
    post_group_name TEXT,
    caption_text    TEXT,
    hashtags        TEXT[],
    location_tag    TEXT,
    media_keys      TEXT[],
    media_metadata  JSONB,
    embedding       vector(1024),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, post_group_name, platform)
);

CREATE INDEX IF NOT EXISTS publish_decisions_embedding_idx
    ON publish_decisions USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS publish_decisions_user_idx
    ON publish_decisions (user_id, created_at DESC);
