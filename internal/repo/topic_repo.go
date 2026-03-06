package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ConversationTopic 表示一个可以被延续或主动回钩的话题线程。
type ConversationTopic struct {
	ID              int64          `json:"id"`
	UserID          string         `json:"user_id"`
	SessionID       string         `json:"session_id"`
	TopicKey        string         `json:"topic_key"`
	TopicLabel      string         `json:"topic_label"`
	Summary         string         `json:"summary"`
	CallbackHint    string         `json:"callback_hint"`
	ClusterKey      string         `json:"cluster_key"`
	Status          string         `json:"status"`
	Importance      int            `json:"importance"`
	MentionCount    int            `json:"mention_count"`
	RecallCount     int            `json:"recall_count"`
	SourceMessageID *int64         `json:"source_message_id,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	LastDiscussedAt *time.Time     `json:"last_discussed_at,omitempty"`
	NextRecallAt    *time.Time     `json:"next_recall_at,omitempty"`
	LastRecalledAt  *time.Time     `json:"last_recalled_at,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// ConversationTopicEdge 表示两个话题线程之间的关系边。
type ConversationTopicEdge struct {
	ID            int64      `json:"id"`
	UserID        string     `json:"user_id"`
	SessionID     string     `json:"session_id"`
	FromTopicKey  string     `json:"from_topic_key"`
	ToTopicKey    string     `json:"to_topic_key"`
	RelationType  string     `json:"relation_type"`
	Weight        float64    `json:"weight"`
	EvidenceCount int        `json:"evidence_count"`
	LastLinkedAt  *time.Time `json:"last_linked_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ListConversationTopics 返回当前 session 下最近仍有价值的话题线程。
func (r *PGMemoryRepository) ListConversationTopics(ctx context.Context, userID, sessionID string, limit int) ([]ConversationTopic, error) {
	userID = strings.TrimSpace(userID)
	sessionID = normalizeTopicSessionID(sessionID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if limit <= 0 {
		limit = 8
	}

	const query = `SELECT id, user_id, session_id, topic_key, topic_label, summary, callback_hint,
		cluster_key, status, importance, mention_count, recall_count, source_message_id, metadata,
		last_discussed_at, next_recall_at, last_recalled_at, created_at, updated_at
		FROM conversation_topics
		WHERE user_id = $1
		  AND session_id = $2
		  AND (
			status = 'active'
			OR COALESCE(last_recalled_at, last_discussed_at, created_at) >= $3
		  )
		ORDER BY
			CASE WHEN status = 'active' THEN 0 ELSE 1 END,
			COALESCE(next_recall_at, last_discussed_at, created_at) DESC,
			importance DESC,
			id DESC
		LIMIT $4`

	rows, err := r.pool.Query(ctx, query, userID, sessionID, time.Now().Add(-7*24*time.Hour), limit)
	if err != nil {
		return nil, fmt.Errorf("list conversation_topics: %w", err)
	}
	defer rows.Close()

	out := make([]ConversationTopic, 0, limit)
	for rows.Next() {
		item, err := scanConversationTopic(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation_topics: %w", err)
	}
	return out, nil
}

// ListConversationTopicEdges 返回话题图中和指定 topics 相关的边。
func (r *PGMemoryRepository) ListConversationTopicEdges(ctx context.Context, userID, sessionID string, topicKeys []string, limit int) ([]ConversationTopicEdge, error) {
	userID = strings.TrimSpace(userID)
	sessionID = normalizeTopicSessionID(sessionID)
	if userID == "" || len(topicKeys) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 16
	}
	normalized := make([]string, 0, len(topicKeys))
	seen := map[string]struct{}{}
	for _, key := range topicKeys {
		key = normalizeTopicKey(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, key)
	}
	if len(normalized) == 0 {
		return nil, nil
	}

	const query = `SELECT id, user_id, session_id, from_topic_key, to_topic_key, relation_type,
		weight, evidence_count, last_linked_at, created_at, updated_at
		FROM conversation_topic_edges
		WHERE user_id = $1
		  AND session_id = $2
		  AND (from_topic_key = ANY($3) OR to_topic_key = ANY($3))
		ORDER BY weight DESC, evidence_count DESC, updated_at DESC
		LIMIT $4`
	rows, err := r.pool.Query(ctx, query, userID, sessionID, normalized, limit)
	if err != nil {
		return nil, fmt.Errorf("list conversation_topic_edges: %w", err)
	}
	defer rows.Close()

	out := make([]ConversationTopicEdge, 0, limit)
	for rows.Next() {
		item, err := scanConversationTopicEdge(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate conversation_topic_edges: %w", err)
	}
	return out, nil
}

// GetConversationTopic 按 key 读取单个话题；不存在时返回空结构体。
func (r *PGMemoryRepository) GetConversationTopic(ctx context.Context, userID, sessionID, topicKey string) (ConversationTopic, error) {
	return getConversationTopic(ctx, r.pool, userID, sessionID, topicKey)
}

// GetConversationTopic 供 proactive runtime 在发送前读取最新的话题状态。
func (r *PGProactiveRepository) GetConversationTopic(ctx context.Context, userID, sessionID, topicKey string) (ConversationTopic, error) {
	return getConversationTopic(ctx, r.pool, userID, sessionID, topicKey)
}

func getConversationTopic(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, userID, sessionID, topicKey string) (ConversationTopic, error) {
	userID = strings.TrimSpace(userID)
	sessionID = normalizeTopicSessionID(sessionID)
	topicKey = normalizeTopicKey(topicKey)
	if userID == "" || topicKey == "" {
		return ConversationTopic{}, nil
	}

	const query = `SELECT id, user_id, session_id, topic_key, topic_label, summary, callback_hint,
		cluster_key, status, importance, mention_count, recall_count, source_message_id, metadata,
		last_discussed_at, next_recall_at, last_recalled_at, created_at, updated_at
		FROM conversation_topics
		WHERE user_id = $1
		  AND session_id = $2
		  AND topic_key = $3`

	var (
		out         ConversationTopic
		metadataRaw []byte
	)
	if err := q.QueryRow(ctx, query, userID, sessionID, topicKey).Scan(
		&out.ID,
		&out.UserID,
		&out.SessionID,
		&out.TopicKey,
		&out.TopicLabel,
		&out.Summary,
		&out.CallbackHint,
		&out.ClusterKey,
		&out.Status,
		&out.Importance,
		&out.MentionCount,
		&out.RecallCount,
		&out.SourceMessageID,
		&metadataRaw,
		&out.LastDiscussedAt,
		&out.NextRecallAt,
		&out.LastRecalledAt,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return ConversationTopic{}, nil
		}
		return ConversationTopic{}, fmt.Errorf("get conversation_topic: %w", err)
	}
	out.Metadata = decodeTopicMetadata(metadataRaw)
	return out, nil
}

// UpsertConversationTopic 新增或刷新一个话题线程。
func (r *PGMemoryRepository) UpsertConversationTopic(ctx context.Context, topic ConversationTopic) (int64, error) {
	topic = normalizeConversationTopic(topic)
	if topic.UserID == "" || topic.TopicKey == "" || topic.TopicLabel == "" {
		return 0, fmt.Errorf("user_id, topic_key and topic_label are required")
	}

	metadata, err := json.Marshal(topic.Metadata)
	if err != nil {
		return 0, fmt.Errorf("marshal topic metadata: %w", err)
	}
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}

	const query = `INSERT INTO conversation_topics (
			user_id, session_id, topic_key, topic_label, summary, callback_hint, cluster_key,
			status, importance, mention_count, recall_count, source_message_id, metadata,
			last_discussed_at, next_recall_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, 0, $10, $11::jsonb, $12, $13)
		ON CONFLICT (user_id, session_id, topic_key) DO UPDATE SET
			topic_label = CASE
				WHEN EXCLUDED.topic_label <> '' THEN EXCLUDED.topic_label
				ELSE conversation_topics.topic_label
			END,
			summary = CASE
				WHEN EXCLUDED.summary <> '' THEN EXCLUDED.summary
				ELSE conversation_topics.summary
			END,
			callback_hint = CASE
				WHEN EXCLUDED.callback_hint <> '' THEN EXCLUDED.callback_hint
				ELSE conversation_topics.callback_hint
			END,
			cluster_key = CASE
				WHEN EXCLUDED.cluster_key <> '' THEN EXCLUDED.cluster_key
				ELSE conversation_topics.cluster_key
			END,
			status = EXCLUDED.status,
			importance = GREATEST(conversation_topics.importance, EXCLUDED.importance),
			mention_count = conversation_topics.mention_count + 1,
			source_message_id = COALESCE(EXCLUDED.source_message_id, conversation_topics.source_message_id),
			metadata = conversation_topics.metadata || EXCLUDED.metadata,
			last_discussed_at = COALESCE(EXCLUDED.last_discussed_at, conversation_topics.last_discussed_at),
			next_recall_at = CASE
				WHEN EXCLUDED.status = 'resolved' THEN NULL
				ELSE COALESCE(EXCLUDED.next_recall_at, conversation_topics.next_recall_at)
			END,
			updated_at = NOW()
		RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, query,
		topic.UserID,
		topic.SessionID,
		topic.TopicKey,
		topic.TopicLabel,
		topic.Summary,
		topic.CallbackHint,
		topic.ClusterKey,
		topic.Status,
		topic.Importance,
		topic.SourceMessageID,
		metadata,
		topic.LastDiscussedAt,
		topic.NextRecallAt,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert conversation_topic: %w", err)
	}
	return id, nil
}

// UpsertConversationTopicEdge 记录两个 topic 之间的共现/关联关系。
func (r *PGMemoryRepository) UpsertConversationTopicEdge(ctx context.Context, edge ConversationTopicEdge) error {
	edge = normalizeConversationTopicEdge(edge)
	if edge.UserID == "" || edge.FromTopicKey == "" || edge.ToTopicKey == "" {
		return nil
	}
	const query = `INSERT INTO conversation_topic_edges (
			user_id, session_id, from_topic_key, to_topic_key, relation_type,
			weight, evidence_count, last_linked_at
		) VALUES ($1, $2, $3, $4, $5, $6, 1, $7)
		ON CONFLICT (user_id, session_id, from_topic_key, to_topic_key, relation_type) DO UPDATE SET
			weight = conversation_topic_edges.weight + EXCLUDED.weight,
			evidence_count = conversation_topic_edges.evidence_count + 1,
			last_linked_at = COALESCE(EXCLUDED.last_linked_at, conversation_topic_edges.last_linked_at),
			updated_at = NOW()`
	if _, err := r.pool.Exec(ctx, query,
		edge.UserID,
		edge.SessionID,
		edge.FromTopicKey,
		edge.ToTopicKey,
		edge.RelationType,
		edge.Weight,
		edge.LastLinkedAt,
	); err != nil {
		return fmt.Errorf("upsert conversation_topic_edge: %w", err)
	}
	return nil
}

// MarkConversationTopicRecalled 记录一次真正送达的主动回钩。
func (r *PGMemoryRepository) MarkConversationTopicRecalled(ctx context.Context, userID, sessionID, topicKey string, recalledAt time.Time) error {
	return markConversationTopicRecalled(ctx, r.pool, userID, sessionID, topicKey, recalledAt)
}

// MarkConversationTopicRecalled 供 proactive delivery 写回话题状态。
func (r *PGProactiveRepository) MarkConversationTopicRecalled(ctx context.Context, userID, sessionID, topicKey string, recalledAt time.Time) error {
	return markConversationTopicRecalled(ctx, r.pool, userID, sessionID, topicKey, recalledAt)
}

func markConversationTopicRecalled(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, userID, sessionID, topicKey string, recalledAt time.Time) error {
	userID = strings.TrimSpace(userID)
	sessionID = normalizeTopicSessionID(sessionID)
	topicKey = normalizeTopicKey(topicKey)
	if userID == "" || topicKey == "" {
		return nil
	}
	if recalledAt.IsZero() {
		recalledAt = time.Now()
	}

	const query = `UPDATE conversation_topics
		SET last_recalled_at = $4,
			next_recall_at = NULL,
			recall_count = recall_count + 1,
			updated_at = NOW()
		WHERE user_id = $1
		  AND session_id = $2
		  AND topic_key = $3`
	if _, err := q.Exec(ctx, query, userID, sessionID, topicKey, recalledAt); err != nil {
		return fmt.Errorf("mark conversation_topic recalled: %w", err)
	}
	return nil
}

func scanConversationTopic(rows pgx.Rows) (ConversationTopic, error) {
	var (
		out         ConversationTopic
		metadataRaw []byte
	)
	if err := rows.Scan(
		&out.ID,
		&out.UserID,
		&out.SessionID,
		&out.TopicKey,
		&out.TopicLabel,
		&out.Summary,
		&out.CallbackHint,
		&out.ClusterKey,
		&out.Status,
		&out.Importance,
		&out.MentionCount,
		&out.RecallCount,
		&out.SourceMessageID,
		&metadataRaw,
		&out.LastDiscussedAt,
		&out.NextRecallAt,
		&out.LastRecalledAt,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return ConversationTopic{}, fmt.Errorf("scan conversation_topic: %w", err)
	}
	out.Metadata = decodeTopicMetadata(metadataRaw)
	return out, nil
}

func scanConversationTopicEdge(rows pgx.Rows) (ConversationTopicEdge, error) {
	var out ConversationTopicEdge
	if err := rows.Scan(
		&out.ID,
		&out.UserID,
		&out.SessionID,
		&out.FromTopicKey,
		&out.ToTopicKey,
		&out.RelationType,
		&out.Weight,
		&out.EvidenceCount,
		&out.LastLinkedAt,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		return ConversationTopicEdge{}, fmt.Errorf("scan conversation_topic_edge: %w", err)
	}
	return out, nil
}

func normalizeConversationTopic(topic ConversationTopic) ConversationTopic {
	topic.UserID = strings.TrimSpace(topic.UserID)
	topic.SessionID = normalizeTopicSessionID(topic.SessionID)
	topic.TopicKey = normalizeTopicKey(topic.TopicKey)
	topic.TopicLabel = strings.TrimSpace(topic.TopicLabel)
	topic.Summary = strings.TrimSpace(topic.Summary)
	topic.CallbackHint = strings.TrimSpace(topic.CallbackHint)
	topic.ClusterKey = normalizeTopicKey(topic.ClusterKey)
	if topic.ClusterKey == "" {
		topic.ClusterKey = normalizeTopicKey(topic.TopicLabel)
	}
	topic.Status = normalizeTopicStatus(topic.Status)
	if topic.Importance <= 0 {
		topic.Importance = 3
	}
	if topic.Importance > 5 {
		topic.Importance = 5
	}
	if topic.Metadata == nil {
		topic.Metadata = map[string]any{}
	}
	return topic
}

func normalizeConversationTopicEdge(edge ConversationTopicEdge) ConversationTopicEdge {
	edge.UserID = strings.TrimSpace(edge.UserID)
	edge.SessionID = normalizeTopicSessionID(edge.SessionID)
	edge.FromTopicKey = normalizeTopicKey(edge.FromTopicKey)
	edge.ToTopicKey = normalizeTopicKey(edge.ToTopicKey)
	edge.RelationType = strings.TrimSpace(strings.ToLower(edge.RelationType))
	if edge.RelationType == "" {
		edge.RelationType = "co_occurs"
	}
	if edge.Weight <= 0 {
		edge.Weight = 1
	}
	keys := []string{edge.FromTopicKey, edge.ToTopicKey}
	sort.Strings(keys)
	edge.FromTopicKey = keys[0]
	edge.ToTopicKey = keys[1]
	return edge
}

func normalizeTopicSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "default"
	}
	return sessionID
}

func normalizeTopicStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "resolved", "closed", "done":
		return "resolved"
	default:
		return "active"
	}
}

func normalizeTopicKey(topicKey string) string {
	topicKey = strings.TrimSpace(strings.ToLower(topicKey))
	if topicKey == "" {
		return ""
	}
	replacer := strings.NewReplacer(
		" ", "_",
		"\n", "_",
		"\t", "_",
		"，", "_",
		"。", "_",
		"、", "_",
		",", "_",
		".", "_",
		"？", "_",
		"?", "_",
		"！", "_",
		"!", "_",
		"：", "_",
		":", "_",
		"；", "_",
		";", "_",
		"“", "",
		"”", "",
		"「", "",
		"」", "",
		"（", "",
		"）", "",
		"(", "",
		")", "",
	)
	topicKey = replacer.Replace(topicKey)
	for strings.Contains(topicKey, "__") {
		topicKey = strings.ReplaceAll(topicKey, "__", "_")
	}
	return strings.Trim(topicKey, "_")
}

func decodeTopicMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}
