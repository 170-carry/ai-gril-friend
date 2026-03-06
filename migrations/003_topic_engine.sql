-- 话题线程：记录未完话题、可回钩线索和下一次适合主动提起的时间。
CREATE TABLE IF NOT EXISTS conversation_topics (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT 'default',
    topic_key TEXT NOT NULL,
    topic_label TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    callback_hint TEXT NOT NULL DEFAULT '',
    cluster_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'resolved')),
    importance INT NOT NULL DEFAULT 3,
    mention_count INT NOT NULL DEFAULT 1,
    recall_count INT NOT NULL DEFAULT 0,
    source_message_id BIGINT NULL REFERENCES chat_messages(id) ON DELETE SET NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_discussed_at TIMESTAMPTZ NULL,
    next_recall_at TIMESTAMPTZ NULL,
    last_recalled_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, session_id, topic_key)
);

CREATE INDEX IF NOT EXISTS idx_conversation_topics_user_session
    ON conversation_topics (
        user_id,
        session_id,
        status,
        COALESCE(next_recall_at, last_discussed_at, created_at) DESC,
        id DESC
    );

CREATE TABLE IF NOT EXISTS conversation_topic_edges (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT 'default',
    from_topic_key TEXT NOT NULL,
    to_topic_key TEXT NOT NULL,
    relation_type TEXT NOT NULL DEFAULT 'co_occurs',
    weight DOUBLE PRECISION NOT NULL DEFAULT 1,
    evidence_count INT NOT NULL DEFAULT 1,
    last_linked_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, session_id, from_topic_key, to_topic_key, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_conversation_topic_edges_user_session
    ON conversation_topic_edges (
        user_id,
        session_id,
        from_topic_key,
        to_topic_key,
        updated_at DESC,
        id DESC
    );
