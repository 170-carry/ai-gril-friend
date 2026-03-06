package repo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationStatements 保存启动时需要确保存在的基础表结构。
var migrationStatements = []string{
	`CREATE EXTENSION IF NOT EXISTS vector;`,
	`CREATE TABLE IF NOT EXISTS chat_messages (
		id BIGSERIAL PRIMARY KEY,
		user_id TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT 'default',
		role TEXT NOT NULL CHECK (role IN ('system', 'user', 'assistant')),
		content TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);`,
	`CREATE INDEX IF NOT EXISTS idx_chat_messages_user_session_created_at
			ON chat_messages (user_id, session_id, created_at DESC, id DESC);`,
	`CREATE TABLE IF NOT EXISTS user_profile (
		user_id TEXT PRIMARY KEY,
		nickname TEXT NOT NULL DEFAULT '',
		birthday DATE NULL,
		timezone TEXT NOT NULL DEFAULT '',
		occupation TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);`,
	`CREATE TABLE IF NOT EXISTS user_preferences (
		id BIGSERIAL PRIMARY KEY,
		user_id TEXT NOT NULL,
		category TEXT NOT NULL,
		value TEXT NOT NULL,
		confidence DOUBLE PRECISION NOT NULL DEFAULT 0.8,
		last_used_at TIMESTAMPTZ NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (user_id, category, value)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_user_preferences_user_last_used
			ON user_preferences (user_id, COALESCE(last_used_at, created_at) DESC, id DESC);`,
	`CREATE TABLE IF NOT EXISTS user_boundaries (
		id BIGSERIAL PRIMARY KEY,
		user_id TEXT NOT NULL,
		topic TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (user_id, topic)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_user_boundaries_user_updated
			ON user_boundaries (user_id, updated_at DESC, id DESC);`,
	`CREATE TABLE IF NOT EXISTS life_events (
		id BIGSERIAL PRIMARY KEY,
		user_id TEXT NOT NULL,
		title TEXT NOT NULL,
		event_time TIMESTAMPTZ NOT NULL,
		importance INT NOT NULL DEFAULT 3,
		source_message_id BIGINT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (user_id, title, event_time)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_life_events_user_event_time
			ON life_events (user_id, event_time ASC);`,
	`CREATE TABLE IF NOT EXISTS proactive_state (
		user_id TEXT PRIMARY KEY,
		enabled BOOLEAN NOT NULL DEFAULT TRUE,
		quiet_hours_enabled BOOLEAN NOT NULL DEFAULT FALSE,
		quiet_start_minute INT NOT NULL DEFAULT 1380,
		quiet_end_minute INT NOT NULL DEFAULT 480,
		timezone TEXT NOT NULL DEFAULT '',
		cooldown_seconds INT NOT NULL DEFAULT 43200,
		last_proactive_at TIMESTAMPTZ NULL,
		last_proactive_reason TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);`,
	`CREATE TABLE IF NOT EXISTS proactive_tasks (
		id BIGSERIAL PRIMARY KEY,
		user_id TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT 'default',
		task_type TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending', 'staging', 'queued', 'sent', 'failed', 'cancelled')),
		reason TEXT NOT NULL DEFAULT '',
		dedup_key TEXT NOT NULL,
		source_message_id BIGINT NULL REFERENCES chat_messages(id) ON DELETE SET NULL,
		source_life_event_id BIGINT NULL REFERENCES life_events(id) ON DELETE SET NULL,
		run_at TIMESTAMPTZ NOT NULL,
		next_attempt_at TIMESTAMPTZ NOT NULL,
		cooldown_seconds INT NOT NULL DEFAULT 43200,
		payload JSONB NOT NULL DEFAULT '{}'::jsonb,
		last_error TEXT NOT NULL DEFAULT '',
		queued_at TIMESTAMPTZ NULL,
		sent_at TIMESTAMPTZ NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		UNIQUE (user_id, dedup_key)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_proactive_tasks_due
			ON proactive_tasks (status, next_attempt_at ASC, run_at ASC, id ASC);`,
	`CREATE TABLE IF NOT EXISTS outbound_queue (
		id BIGSERIAL PRIMARY KEY,
		user_id TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT 'default',
		task_id BIGINT NOT NULL REFERENCES proactive_tasks(id) ON DELETE CASCADE,
		status TEXT NOT NULL DEFAULT 'pending'
			CHECK (status IN ('pending', 'sending', 'delivered', 'failed', 'cancelled')),
		reason TEXT NOT NULL DEFAULT '',
		dedup_key TEXT NOT NULL,
		payload JSONB NOT NULL DEFAULT '{}'::jsonb,
		attempts INT NOT NULL DEFAULT 0,
		max_attempts INT NOT NULL DEFAULT 3,
		next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		last_error TEXT NOT NULL DEFAULT '',
		chat_message_id BIGINT NULL REFERENCES chat_messages(id) ON DELETE SET NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		delivered_at TIMESTAMPTZ NULL,
		UNIQUE (user_id, dedup_key)
	);`,
	`CREATE INDEX IF NOT EXISTS idx_outbound_queue_due
			ON outbound_queue (status, next_attempt_at ASC, id ASC);`,
	`CREATE TABLE IF NOT EXISTS chat_outbox (
		id BIGSERIAL PRIMARY KEY,
		user_id TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT 'default',
		source_event_id BIGINT NULL REFERENCES life_events(id) ON DELETE SET NULL,
		chat_message_id BIGINT NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
		payload JSONB NOT NULL DEFAULT '{}'::jsonb,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		delivered_at TIMESTAMPTZ NULL
	);`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_chat_outbox_source_event_unique
			ON chat_outbox (source_event_id)
			WHERE source_event_id IS NOT NULL;`,
	`CREATE INDEX IF NOT EXISTS idx_chat_outbox_user_session_id
			ON chat_outbox (user_id, session_id, id ASC);`,
	`ALTER TABLE chat_outbox
		ADD COLUMN IF NOT EXISTS source_task_id BIGINT NULL;`,
	`ALTER TABLE chat_outbox
		ADD COLUMN IF NOT EXISTS source_queue_id BIGINT NULL;`,
	`CREATE INDEX IF NOT EXISTS idx_chat_outbox_source_task
			ON chat_outbox (source_task_id);`,
	`CREATE TABLE IF NOT EXISTS memory_chunks (
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
	);`,
	`CREATE INDEX IF NOT EXISTS idx_memory_chunks_user_recent
			ON memory_chunks (user_id, COALESCE(last_used_at, created_at) DESC, id DESC);`,
	`CREATE INDEX IF NOT EXISTS idx_memory_chunks_user_flags
			ON memory_chunks (user_id, superseded, pinned);`,
	`CREATE INDEX IF NOT EXISTS idx_memory_chunks_embedding_hnsw
			ON memory_chunks USING hnsw (embedding vector_cosine_ops);`,
}

// RunMigrations 逐条执行迁移语句；任一失败即返回错误并停止启动。
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	for _, statement := range migrationStatements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("run migration failed: %w", err)
		}
	}
	return nil
}
