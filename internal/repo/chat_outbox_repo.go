package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ChatOutboxItem 是前端轮询主动消息时返回的数据结构。
type ChatOutboxItem struct {
	ID            int64          `json:"id"`
	UserID        string         `json:"user_id"`
	SessionID     string         `json:"session_id"`
	SourceEventID *int64         `json:"source_event_id,omitempty"`
	SourceTaskID  *int64         `json:"source_task_id,omitempty"`
	ChatMessageID int64          `json:"chat_message_id"`
	Content       string         `json:"content"`
	Payload       map[string]any `json:"payload"`
	CreatedAt     time.Time      `json:"created_at"`
}

// PGChatOutboxRepository 是主动消息 outbox 的 Postgres 实现。
type PGChatOutboxRepository struct {
	pool *pgxpool.Pool
}

// NewPGChatOutboxRepository 创建 outbox 仓库实例。
func NewPGChatOutboxRepository(pool *pgxpool.Pool) *PGChatOutboxRepository {
	return &PGChatOutboxRepository{pool: pool}
}

// PullAfter 按游标拉取主动消息，供前端短轮询使用。
func (r *PGChatOutboxRepository) PullAfter(ctx context.Context, userID, sessionID string, afterID int64, limit int) ([]ChatOutboxItem, error) {
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if sessionID == "" {
		sessionID = "default"
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if afterID < 0 {
		afterID = 0
	}

	const query = `SELECT o.id, o.user_id, o.session_id, o.source_event_id, o.chat_message_id,
		o.source_task_id, o.payload, o.created_at, m.content
		FROM chat_outbox o
		INNER JOIN chat_messages m ON m.id = o.chat_message_id
		WHERE o.user_id = $1
		  AND o.session_id = $2
		  AND o.id > $3
		ORDER BY o.id ASC
		LIMIT $4`

	rows, err := r.pool.Query(ctx, query, userID, sessionID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("pull chat_outbox: %w", err)
	}
	defer rows.Close()

	out := make([]ChatOutboxItem, 0, limit)
	for rows.Next() {
		var item ChatOutboxItem
		var payloadRaw []byte
		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.SessionID,
			&item.SourceEventID,
			&item.ChatMessageID,
			&item.SourceTaskID,
			&payloadRaw,
			&item.CreatedAt,
			&item.Content,
		); err != nil {
			return nil, fmt.Errorf("scan chat_outbox: %w", err)
		}
		if len(payloadRaw) > 0 {
			if err := json.Unmarshal(payloadRaw, &item.Payload); err != nil {
				item.Payload = map[string]any{}
			}
		}
		if item.Payload == nil {
			item.Payload = map[string]any{}
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chat_outbox: %w", err)
	}
	return out, nil
}
