package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserProfile 是用户稳定画像信息。
type UserProfile struct {
	UserID     string
	Nickname   string
	Birthday   *time.Time
	Timezone   string
	Occupation string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// UserPreference 是用户偏好条目。
type UserPreference struct {
	ID         int64
	UserID     string
	Category   string
	Value      string
	Confidence float64
	LastUsedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// UserBoundary 是用户边界/雷区条目。
type UserBoundary struct {
	ID          int64
	UserID      string
	Topic       string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// LifeEvent 是用户未来事件条目。
type LifeEvent struct {
	ID              int64
	UserID          string
	Title           string
	EventTime       time.Time
	Importance      int
	SourceMessageID *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MemoryChunk 是语义记忆条目。
type MemoryChunk struct {
	ID           int64
	UserID       string
	Content      string
	ContentShort string
	Topic        string
	MemoryType   string
	Importance   int
	Confidence   float64
	Pinned       bool
	Embedding    []float32
	Similarity   float64
	AccessCount  int
	LastUsedAt   *time.Time
	Superseded   bool
	Metadata     map[string]any
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// MemoryRepository 定义记忆系统所需的数据访问能力。
type MemoryRepository interface {
	GetUserProfile(ctx context.Context, userID string) (UserProfile, error)
	UpsertUserProfile(ctx context.Context, profile UserProfile) error
	ListUserPreferences(ctx context.Context, userID string, limit int) ([]UserPreference, error)
	UpsertUserPreference(ctx context.Context, pref UserPreference) (int64, error)
	DeleteUserPreferencesByKeyword(ctx context.Context, userID, keyword string) (int64, error)
	ListUserBoundaries(ctx context.Context, userID string) ([]UserBoundary, error)
	UpsertUserBoundary(ctx context.Context, boundary UserBoundary) (int64, error)
	DeleteUserBoundariesByKeyword(ctx context.Context, userID, keyword string) (int64, error)
	ListUpcomingEvents(ctx context.Context, userID string, now time.Time, days, limit int) ([]LifeEvent, error)
	UpsertLifeEvent(ctx context.Context, event LifeEvent) (int64, error)
	SearchMemoryChunks(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]MemoryChunk, error)
	ListRecentMemoryChunks(ctx context.Context, userID string, limit int) ([]MemoryChunk, error)
	FindSimilarMemoryChunk(ctx context.Context, userID string, queryEmbedding []float32, threshold float64) (*MemoryChunk, error)
	UpsertMemoryChunk(ctx context.Context, chunk MemoryChunk) (int64, error)
	TouchMemoryChunks(ctx context.Context, userID string, ids []int64, touchedAt time.Time) error
	SupersedeMemoryChunksByKeyword(ctx context.Context, userID, memoryType, keyword string) (int64, error)
}

// PGMemoryRepository 是基于 pgxpool 的记忆仓库实现。
type PGMemoryRepository struct {
	pool *pgxpool.Pool
}

// NewPGMemoryRepository 创建 Postgres 记忆仓库。
func NewPGMemoryRepository(pool *pgxpool.Pool) *PGMemoryRepository {
	return &PGMemoryRepository{pool: pool}
}

// GetUserProfile 读取用户画像；不存在时返回空结构体。
func (r *PGMemoryRepository) GetUserProfile(ctx context.Context, userID string) (UserProfile, error) {
	const query = `SELECT user_id, nickname, birthday, timezone, occupation, created_at, updated_at
		FROM user_profile
		WHERE user_id = $1`

	var out UserProfile
	if err := r.pool.QueryRow(ctx, query, userID).Scan(
		&out.UserID,
		&out.Nickname,
		&out.Birthday,
		&out.Timezone,
		&out.Occupation,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return UserProfile{UserID: userID}, nil
		}
		return UserProfile{}, fmt.Errorf("get user_profile: %w", err)
	}
	return out, nil
}

// UpsertUserProfile 新增或更新用户画像。
func (r *PGMemoryRepository) UpsertUserProfile(ctx context.Context, profile UserProfile) error {
	const query = `INSERT INTO user_profile (user_id, nickname, birthday, timezone, occupation)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id) DO UPDATE SET
			nickname = COALESCE(NULLIF(EXCLUDED.nickname, ''), user_profile.nickname),
			birthday = COALESCE(EXCLUDED.birthday, user_profile.birthday),
			timezone = COALESCE(NULLIF(EXCLUDED.timezone, ''), user_profile.timezone),
			occupation = COALESCE(NULLIF(EXCLUDED.occupation, ''), user_profile.occupation),
			updated_at = NOW()`

	if _, err := r.pool.Exec(ctx, query,
		profile.UserID,
		strings.TrimSpace(profile.Nickname),
		profile.Birthday,
		strings.TrimSpace(profile.Timezone),
		strings.TrimSpace(profile.Occupation),
	); err != nil {
		return fmt.Errorf("upsert user_profile: %w", err)
	}
	return nil
}

// ListUserPreferences 读取用户偏好，按最近使用和置信度排序。
func (r *PGMemoryRepository) ListUserPreferences(ctx context.Context, userID string, limit int) ([]UserPreference, error) {
	if limit <= 0 {
		limit = 5
	}
	const query = `SELECT id, user_id, category, value, confidence, last_used_at, created_at, updated_at
		FROM user_preferences
		WHERE user_id = $1
		ORDER BY COALESCE(last_used_at, created_at) DESC, confidence DESC, id DESC
		LIMIT $2`

	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list user_preferences: %w", err)
	}
	defer rows.Close()

	out := make([]UserPreference, 0, limit)
	for rows.Next() {
		var p UserPreference
		if err := rows.Scan(&p.ID, &p.UserID, &p.Category, &p.Value, &p.Confidence, &p.LastUsedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user_preferences: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user_preferences: %w", err)
	}
	return out, nil
}

// UpsertUserPreference 新增或更新偏好条目。
func (r *PGMemoryRepository) UpsertUserPreference(ctx context.Context, pref UserPreference) (int64, error) {
	const query = `INSERT INTO user_preferences (user_id, category, value, confidence, last_used_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (user_id, category, value) DO UPDATE SET
			confidence = GREATEST(user_preferences.confidence, EXCLUDED.confidence),
			last_used_at = NOW(),
			updated_at = NOW()
		RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, query,
		pref.UserID,
		strings.TrimSpace(pref.Category),
		strings.TrimSpace(pref.Value),
		pref.Confidence,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert user_preferences: %w", err)
	}
	return id, nil
}

// DeleteUserPreferencesByKeyword 根据关键词删除冲突偏好（如“我不喜欢xxx”覆盖旧喜好）。
func (r *PGMemoryRepository) DeleteUserPreferencesByKeyword(ctx context.Context, userID, keyword string) (int64, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return 0, nil
	}
	const query = `DELETE FROM user_preferences
		WHERE user_id = $1
		  AND (
			LOWER(value) LIKE ('%%' || LOWER($2) || '%%')
			OR LOWER(category) = LOWER($2)
		  )`
	cmd, err := r.pool.Exec(ctx, query, userID, keyword)
	if err != nil {
		return 0, fmt.Errorf("delete user_preferences by keyword: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// ListUserBoundaries 读取用户边界条目。
func (r *PGMemoryRepository) ListUserBoundaries(ctx context.Context, userID string) ([]UserBoundary, error) {
	const query = `SELECT id, user_id, topic, description, created_at, updated_at
		FROM user_boundaries
		WHERE user_id = $1
		ORDER BY updated_at DESC, id DESC`

	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("list user_boundaries: %w", err)
	}
	defer rows.Close()

	out := make([]UserBoundary, 0, 8)
	for rows.Next() {
		var b UserBoundary
		if err := rows.Scan(&b.ID, &b.UserID, &b.Topic, &b.Description, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user_boundaries: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user_boundaries: %w", err)
	}
	return out, nil
}

// UpsertUserBoundary 新增或更新边界条目。
func (r *PGMemoryRepository) UpsertUserBoundary(ctx context.Context, boundary UserBoundary) (int64, error) {
	const query = `INSERT INTO user_boundaries (user_id, topic, description)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, topic) DO UPDATE SET
			description = COALESCE(NULLIF(EXCLUDED.description, ''), user_boundaries.description),
			updated_at = NOW()
		RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, query,
		boundary.UserID,
		strings.TrimSpace(boundary.Topic),
		strings.TrimSpace(boundary.Description),
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert user_boundaries: %w", err)
	}
	return id, nil
}

// DeleteUserBoundariesByKeyword 根据关键词删除冲突边界（如“我现在喜欢xxx”覆盖旧禁忌）。
func (r *PGMemoryRepository) DeleteUserBoundariesByKeyword(ctx context.Context, userID, keyword string) (int64, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return 0, nil
	}
	const query = `DELETE FROM user_boundaries
		WHERE user_id = $1
		  AND (
			LOWER(topic) LIKE ('%%' || LOWER($2) || '%%')
			OR LOWER(description) LIKE ('%%' || LOWER($2) || '%%')
		  )`
	cmd, err := r.pool.Exec(ctx, query, userID, keyword)
	if err != nil {
		return 0, fmt.Errorf("delete user_boundaries by keyword: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// ListUpcomingEvents 读取未来 N 天内的重要事件。
func (r *PGMemoryRepository) ListUpcomingEvents(ctx context.Context, userID string, now time.Time, days, limit int) ([]LifeEvent, error) {
	if days <= 0 {
		days = 7
	}
	if limit <= 0 {
		limit = 8
	}
	const query = `SELECT id, user_id, title, event_time, importance, source_message_id, created_at, updated_at
		FROM life_events
		WHERE user_id = $1
		  AND event_time >= $2
		  AND title NOT LIKE '提醒：%%'
		  AND title <> '情绪回访'
		  -- 使用整数天数与 interval 相乘，避免把 int 参数隐式当成 text 拼接导致编码错误。
		  AND event_time <= ($2 + ($3::int * interval '1 day'))
		ORDER BY event_time ASC
		LIMIT $4`

	rows, err := r.pool.Query(ctx, query, userID, now, days, limit)
	if err != nil {
		return nil, fmt.Errorf("list life_events: %w", err)
	}
	defer rows.Close()

	out := make([]LifeEvent, 0, limit)
	for rows.Next() {
		var e LifeEvent
		if err := rows.Scan(&e.ID, &e.UserID, &e.Title, &e.EventTime, &e.Importance, &e.SourceMessageID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan life_events: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate life_events: %w", err)
	}
	return out, nil
}

// UpsertLifeEvent 新增或更新事件条目。
func (r *PGMemoryRepository) UpsertLifeEvent(ctx context.Context, event LifeEvent) (int64, error) {
	const query = `INSERT INTO life_events (user_id, title, event_time, importance, source_message_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, title, event_time) DO UPDATE SET
			importance = GREATEST(life_events.importance, EXCLUDED.importance),
			source_message_id = COALESCE(EXCLUDED.source_message_id, life_events.source_message_id),
			updated_at = NOW()
		RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, query,
		event.UserID,
		strings.TrimSpace(event.Title),
		event.EventTime,
		event.Importance,
		event.SourceMessageID,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert life_events: %w", err)
	}
	return id, nil
}

// SearchMemoryChunks 通过 pgvector 相似度检索语义记忆。
func (r *PGMemoryRepository) SearchMemoryChunks(ctx context.Context, userID string, queryEmbedding []float32, limit int) ([]MemoryChunk, error) {
	if len(queryEmbedding) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 30
	}
	vectorText := vectorLiteral(queryEmbedding)

	const query = `SELECT id, user_id, content, content_short, topic, memory_type,
		importance, confidence, pinned, access_count, last_used_at, superseded,
		created_at, updated_at, (1 - (embedding <=> $2::vector)) AS sim
		FROM memory_chunks
		WHERE user_id = $1
		  AND superseded = FALSE
		  AND embedding IS NOT NULL
		ORDER BY embedding <=> $2::vector
		LIMIT $3`

	rows, err := r.pool.Query(ctx, query, userID, vectorText, limit)
	if err != nil {
		return nil, fmt.Errorf("search memory_chunks: %w", err)
	}
	defer rows.Close()

	out := make([]MemoryChunk, 0, limit)
	for rows.Next() {
		item, err := scanMemoryChunk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory_chunks: %w", err)
	}
	return out, nil
}

// ListRecentMemoryChunks 回退读取最近记忆（用于无 embedding 场景）。
func (r *PGMemoryRepository) ListRecentMemoryChunks(ctx context.Context, userID string, limit int) ([]MemoryChunk, error) {
	if limit <= 0 {
		limit = 30
	}
	const query = `SELECT id, user_id, content, content_short, topic, memory_type,
		importance, confidence, pinned, access_count, last_used_at, superseded,
		created_at, updated_at, 0::float8 AS sim
		FROM memory_chunks
		WHERE user_id = $1
		  AND superseded = FALSE
		ORDER BY COALESCE(last_used_at, created_at) DESC, id DESC
		LIMIT $2`

	rows, err := r.pool.Query(ctx, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list recent memory_chunks: %w", err)
	}
	defer rows.Close()

	out := make([]MemoryChunk, 0, limit)
	for rows.Next() {
		item, err := scanMemoryChunk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent memory_chunks: %w", err)
	}
	return out, nil
}

// FindSimilarMemoryChunk 查找高相似度记忆，用于去重合并。
func (r *PGMemoryRepository) FindSimilarMemoryChunk(ctx context.Context, userID string, queryEmbedding []float32, threshold float64) (*MemoryChunk, error) {
	if len(queryEmbedding) == 0 {
		return nil, nil
	}
	vectorText := vectorLiteral(queryEmbedding)

	const query = `SELECT id, user_id, content, content_short, topic, memory_type,
		importance, confidence, pinned, access_count, last_used_at, superseded,
		created_at, updated_at, (1 - (embedding <=> $2::vector)) AS sim
		FROM memory_chunks
		WHERE user_id = $1
		  AND superseded = FALSE
		  AND embedding IS NOT NULL
		ORDER BY embedding <=> $2::vector
		LIMIT 1`

	rows, err := r.pool.Query(ctx, query, userID, vectorText)
	if err != nil {
		return nil, fmt.Errorf("find similar memory_chunk: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, nil
	}
	item, err := scanMemoryChunk(rows)
	if err != nil {
		return nil, err
	}
	if item.Similarity < threshold {
		return nil, nil
	}
	return &item, nil
}

// UpsertMemoryChunk 新增或更新语义记忆条目。
func (r *PGMemoryRepository) UpsertMemoryChunk(ctx context.Context, chunk MemoryChunk) (int64, error) {
	metadata, err := json.Marshal(chunk.Metadata)
	if err != nil {
		return 0, fmt.Errorf("marshal chunk metadata: %w", err)
	}
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}

	if strings.TrimSpace(chunk.ContentShort) == "" {
		chunk.ContentShort = strings.TrimSpace(chunk.Content)
	}
	if strings.TrimSpace(chunk.ContentShort) == "" {
		return 0, fmt.Errorf("content_short is required")
	}

	if len(chunk.Embedding) > 0 {
		const query = `INSERT INTO memory_chunks (
			user_id, content, content_short, topic, memory_type, importance, confidence, pinned, embedding, metadata
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::vector, $10::jsonb)
		ON CONFLICT (user_id, content_short) DO UPDATE SET
			content = EXCLUDED.content,
			topic = COALESCE(NULLIF(EXCLUDED.topic, ''), memory_chunks.topic),
			memory_type = COALESCE(NULLIF(EXCLUDED.memory_type, ''), memory_chunks.memory_type),
			importance = GREATEST(memory_chunks.importance, EXCLUDED.importance),
			confidence = GREATEST(memory_chunks.confidence, EXCLUDED.confidence),
			pinned = memory_chunks.pinned OR EXCLUDED.pinned,
			embedding = COALESCE(EXCLUDED.embedding, memory_chunks.embedding),
			metadata = memory_chunks.metadata || EXCLUDED.metadata,
			updated_at = NOW()
		RETURNING id`

		var id int64
		if err := r.pool.QueryRow(ctx, query,
			chunk.UserID,
			strings.TrimSpace(chunk.Content),
			strings.TrimSpace(chunk.ContentShort),
			strings.TrimSpace(chunk.Topic),
			strings.TrimSpace(chunk.MemoryType),
			safeImportance(chunk.Importance),
			safeConfidence(chunk.Confidence),
			chunk.Pinned,
			vectorLiteral(chunk.Embedding),
			metadata,
		).Scan(&id); err != nil {
			return 0, fmt.Errorf("upsert memory_chunk(with embedding): %w", err)
		}
		return id, nil
	}

	const query = `INSERT INTO memory_chunks (
		user_id, content, content_short, topic, memory_type, importance, confidence, pinned, metadata
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)
	ON CONFLICT (user_id, content_short) DO UPDATE SET
		content = EXCLUDED.content,
		topic = COALESCE(NULLIF(EXCLUDED.topic, ''), memory_chunks.topic),
		memory_type = COALESCE(NULLIF(EXCLUDED.memory_type, ''), memory_chunks.memory_type),
		importance = GREATEST(memory_chunks.importance, EXCLUDED.importance),
		confidence = GREATEST(memory_chunks.confidence, EXCLUDED.confidence),
		pinned = memory_chunks.pinned OR EXCLUDED.pinned,
		metadata = memory_chunks.metadata || EXCLUDED.metadata,
		updated_at = NOW()
	RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, query,
		chunk.UserID,
		strings.TrimSpace(chunk.Content),
		strings.TrimSpace(chunk.ContentShort),
		strings.TrimSpace(chunk.Topic),
		strings.TrimSpace(chunk.MemoryType),
		safeImportance(chunk.Importance),
		safeConfidence(chunk.Confidence),
		chunk.Pinned,
		metadata,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert memory_chunk: %w", err)
	}
	return id, nil
}

// TouchMemoryChunks 更新命中记忆的访问计数和最近使用时间。
func (r *PGMemoryRepository) TouchMemoryChunks(ctx context.Context, userID string, ids []int64, touchedAt time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	const query = `UPDATE memory_chunks
		SET access_count = access_count + 1,
			last_used_at = $3,
			updated_at = NOW()
		WHERE user_id = $1
		  AND id = ANY($2)`
	if _, err := r.pool.Exec(ctx, query, userID, ids, touchedAt); err != nil {
		return fmt.Errorf("touch memory_chunks: %w", err)
	}
	return nil
}

// SupersedeMemoryChunksByKeyword 按关键词把冲突语义记忆标记为 superseded。
func (r *PGMemoryRepository) SupersedeMemoryChunksByKeyword(ctx context.Context, userID, memoryType, keyword string) (int64, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return 0, nil
	}
	memoryType = strings.TrimSpace(memoryType)
	const query = `UPDATE memory_chunks
		SET superseded = TRUE,
			updated_at = NOW()
		WHERE user_id = $1
		  AND superseded = FALSE
		  AND ($2 = '' OR memory_type = $2)
		  AND (
			LOWER(content) LIKE ('%%' || LOWER($3) || '%%')
			OR LOWER(content_short) LIKE ('%%' || LOWER($3) || '%%')
			OR LOWER(topic) LIKE ('%%' || LOWER($3) || '%%')
		  )`
	cmd, err := r.pool.Exec(ctx, query, userID, memoryType, keyword)
	if err != nil {
		return 0, fmt.Errorf("supersede memory_chunks by keyword: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// scanMemoryChunk 从查询游标扫描单条语义记忆记录。
func scanMemoryChunk(rows pgx.Rows) (MemoryChunk, error) {
	var out MemoryChunk
	if err := rows.Scan(
		&out.ID,
		&out.UserID,
		&out.Content,
		&out.ContentShort,
		&out.Topic,
		&out.MemoryType,
		&out.Importance,
		&out.Confidence,
		&out.Pinned,
		&out.AccessCount,
		&out.LastUsedAt,
		&out.Superseded,
		&out.CreatedAt,
		&out.UpdatedAt,
		&out.Similarity,
	); err != nil {
		return MemoryChunk{}, fmt.Errorf("scan memory_chunk: %w", err)
	}
	return out, nil
}

// vectorLiteral 把 embedding 序列转换为 pgvector 可识别的字符串形式。
func vectorLiteral(embedding []float32) string {
	if len(embedding) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(embedding))
	for _, v := range embedding {
		parts = append(parts, strconv.FormatFloat(float64(v), 'f', -1, 32))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// safeConfidence 把置信度限制在 0~1。
func safeConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// safeImportance 把重要度限制在 1~5。
func safeImportance(v int) int {
	if v < 1 {
		return 1
	}
	if v > 5 {
		return 5
	}
	return v
}
