-- 结构化记忆：用户稳定画像。
CREATE TABLE IF NOT EXISTS user_profile (
    user_id TEXT PRIMARY KEY,
    nickname TEXT NOT NULL DEFAULT '',
    birthday DATE NULL,
    timezone TEXT NOT NULL DEFAULT '',
    occupation TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 结构化记忆：用户偏好条目。
CREATE TABLE IF NOT EXISTS user_preferences (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    category TEXT NOT NULL,
    value TEXT NOT NULL,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0.8,
    last_used_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, category, value)
);
CREATE INDEX IF NOT EXISTS idx_user_preferences_user_last_used
    ON user_preferences (user_id, COALESCE(last_used_at, created_at) DESC, id DESC);

-- 结构化记忆：用户边界/雷区。
CREATE TABLE IF NOT EXISTS user_boundaries (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    topic TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, topic)
);
CREATE INDEX IF NOT EXISTS idx_user_boundaries_user_updated
    ON user_boundaries (user_id, updated_at DESC, id DESC);

-- 结构化记忆：未来重要事件。
CREATE TABLE IF NOT EXISTS life_events (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    title TEXT NOT NULL,
    event_time TIMESTAMPTZ NOT NULL,
    importance INT NOT NULL DEFAULT 3,
    source_message_id BIGINT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, title, event_time)
);
CREATE INDEX IF NOT EXISTS idx_life_events_user_event_time
    ON life_events (user_id, event_time ASC);

-- 语义记忆：可检索 chunk + pgvector 向量。
CREATE TABLE IF NOT EXISTS memory_chunks (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    content TEXT NOT NULL,
    content_short TEXT NOT NULL,
    topic TEXT NOT NULL DEFAULT '',
    memory_type TEXT NOT NULL DEFAULT '',
    importance INT NOT NULL DEFAULT 3,
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0.8,
    pinned BOOLEAN NOT NULL DEFAULT FALSE,
    embedding VECTOR(1536) NULL,
    access_count INT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMPTZ NULL,
    superseded BOOLEAN NOT NULL DEFAULT FALSE,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, content_short)
);
CREATE INDEX IF NOT EXISTS idx_memory_chunks_user_recent
    ON memory_chunks (user_id, COALESCE(last_used_at, created_at) DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_memory_chunks_user_flags
    ON memory_chunks (user_id, superseded, pinned);
CREATE INDEX IF NOT EXISTS idx_memory_chunks_embedding_hnsw
    ON memory_chunks USING hnsw (embedding vector_cosine_ops);
