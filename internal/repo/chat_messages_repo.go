package repo

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ChatMessage 对应 chat_messages 表的一条记录。
type ChatMessage struct {
	ID        int64
	UserID    string
	SessionID string
	Role      string
	Content   string
	CreatedAt time.Time
}

// ChatMessageRepository 定义聊天消息存取接口，便于测试替换。
type ChatMessageRepository interface {
	Insert(ctx context.Context, msg ChatMessage) (int64, error)
	ListRecent(ctx context.Context, userID, sessionID string, limit int) ([]ChatMessage, error)
}

// PGChatMessageRepository 是基于 pgx 连接池的仓库实现。
type PGChatMessageRepository struct {
	pool *pgxpool.Pool
}

// NewPGChatMessageRepository 创建 Postgres 仓库实例。
func NewPGChatMessageRepository(pool *pgxpool.Pool) *PGChatMessageRepository {
	return &PGChatMessageRepository{pool: pool}
}

// Insert 写入一条聊天消息并返回数据库生成的主键。
func (r *PGChatMessageRepository) Insert(ctx context.Context, msg ChatMessage) (int64, error) {
	const query = `INSERT INTO chat_messages (user_id, session_id, role, content)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, query, msg.UserID, msg.SessionID, msg.Role, msg.Content).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert chat_message: %w", err)
	}

	return id, nil
}

// ListRecent 查询最近消息并按时间正序返回，便于直接拼接对话历史。
func (r *PGChatMessageRepository) ListRecent(ctx context.Context, userID, sessionID string, limit int) ([]ChatMessage, error) {
	const query = `SELECT id, user_id, session_id, role, content, created_at
		FROM chat_messages
		WHERE user_id = $1 AND session_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3`

	rows, err := r.pool.Query(ctx, query, userID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent chat_messages: %w", err)
	}
	defer rows.Close()

	reverse := make([]ChatMessage, 0, limit)
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.UserID, &m.SessionID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan chat_message: %w", err)
		}
		reverse = append(reverse, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chat_messages: %w", err)
	}

	// SQL 是倒序查询，出参转换为时间正序，保持对话阅读顺序。
	messages := make([]ChatMessage, len(reverse))
	for i := range reverse {
		messages[len(reverse)-1-i] = reverse[i]
	}

	return messages, nil
}
