-- 开启 pgvector 扩展（后续向量检索能力依赖该扩展）。
CREATE EXTENSION IF NOT EXISTS vector;

-- 聊天消息主表：保存用户与助手消息历史。
CREATE TABLE IF NOT EXISTS chat_messages (
    id BIGSERIAL PRIMARY KEY,
    user_id TEXT NOT NULL,
    session_id TEXT NOT NULL DEFAULT 'default',
    role TEXT NOT NULL CHECK (role IN ('system', 'user', 'assistant')),
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 复合索引：按 user/session 查询最近消息时走索引。
CREATE INDEX IF NOT EXISTS idx_chat_messages_user_session_created_at
    ON chat_messages (user_id, session_id, created_at DESC, id DESC);
