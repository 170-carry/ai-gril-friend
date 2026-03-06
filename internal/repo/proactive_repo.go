package repo

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ProactiveState 表示用户主动消息开关、免打扰与冷却状态。
type ProactiveState struct {
	UserID              string
	Enabled             bool
	QuietHoursEnabled   bool
	QuietStartMinute    int
	QuietEndMinute      int
	Timezone            string
	CooldownSeconds     int
	LastProactiveAt     *time.Time
	LastProactiveReason string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// ProactiveTask 表示“未来某时要不要主动”的任务实体。
type ProactiveTask struct {
	ID                int64          `json:"id"`
	UserID            string         `json:"user_id"`
	SessionID         string         `json:"session_id"`
	TaskType          string         `json:"task_type"`
	Status            string         `json:"status"`
	Reason            string         `json:"reason"`
	DedupKey          string         `json:"dedup_key"`
	SourceMessageID   *int64         `json:"source_message_id,omitempty"`
	SourceLifeEventID *int64         `json:"source_life_event_id,omitempty"`
	RunAt             time.Time      `json:"run_at"`
	NextAttemptAt     time.Time      `json:"next_attempt_at"`
	CooldownSeconds   int            `json:"cooldown_seconds"`
	Payload           map[string]any `json:"payload"`
	LastError         string         `json:"last_error"`
	QueuedAt          *time.Time     `json:"queued_at,omitempty"`
	SentAt            *time.Time     `json:"sent_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// OutboundQueueItem 表示准备投递到 chat_messages/chat_outbox 的发送任务。
type OutboundQueueItem struct {
	ID            int64          `json:"id"`
	UserID        string         `json:"user_id"`
	SessionID     string         `json:"session_id"`
	TaskID        int64          `json:"task_id"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason"`
	DedupKey      string         `json:"dedup_key"`
	Payload       map[string]any `json:"payload"`
	Attempts      int            `json:"attempts"`
	MaxAttempts   int            `json:"max_attempts"`
	NextAttemptAt time.Time      `json:"next_attempt_at"`
	LastError     string         `json:"last_error"`
	ChatMessageID *int64         `json:"chat_message_id,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeliveredAt   *time.Time     `json:"delivered_at,omitempty"`
}

// PGProactiveRepository 是主动任务系统的 Postgres 实现。
type PGProactiveRepository struct {
	pool *pgxpool.Pool
}

// NewPGProactiveRepository 创建主动任务仓库实例。
func NewPGProactiveRepository(pool *pgxpool.Pool) *PGProactiveRepository {
	return &PGProactiveRepository{pool: pool}
}

// EnsureState 保证用户存在 proactive_state；若不存在则写入默认配置。
func (r *PGProactiveRepository) EnsureState(ctx context.Context, userID string) (ProactiveState, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ProactiveState{}, fmt.Errorf("user_id is required")
	}

	const ensureQuery = `INSERT INTO proactive_state (user_id)
		VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING`
	if _, err := r.pool.Exec(ctx, ensureQuery, userID); err != nil {
		return ProactiveState{}, fmt.Errorf("ensure proactive_state: %w", err)
	}
	return r.GetState(ctx, userID)
}

// GetState 读取用户主动消息状态；不存在时自动创建默认值。
func (r *PGProactiveRepository) GetState(ctx context.Context, userID string) (ProactiveState, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ProactiveState{}, fmt.Errorf("user_id is required")
	}

	const query = `SELECT user_id, enabled, quiet_hours_enabled, quiet_start_minute, quiet_end_minute,
		timezone, cooldown_seconds, last_proactive_at, last_proactive_reason, created_at, updated_at
		FROM proactive_state
		WHERE user_id = $1`

	var out ProactiveState
	if err := r.pool.QueryRow(ctx, query, userID).Scan(
		&out.UserID,
		&out.Enabled,
		&out.QuietHoursEnabled,
		&out.QuietStartMinute,
		&out.QuietEndMinute,
		&out.Timezone,
		&out.CooldownSeconds,
		&out.LastProactiveAt,
		&out.LastProactiveReason,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if err == pgx.ErrNoRows {
			return r.EnsureState(ctx, userID)
		}
		return ProactiveState{}, fmt.Errorf("get proactive_state: %w", err)
	}
	return out, nil
}

// UpsertState 更新主动消息设置，便于用户开关、冷却和免打扰生效。
func (r *PGProactiveRepository) UpsertState(ctx context.Context, state ProactiveState) error {
	state.UserID = strings.TrimSpace(state.UserID)
	if state.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if state.QuietStartMinute < 0 {
		state.QuietStartMinute = 0
	}
	if state.QuietStartMinute > 1439 {
		state.QuietStartMinute = 1439
	}
	if state.QuietEndMinute < 0 {
		state.QuietEndMinute = 0
	}
	if state.QuietEndMinute > 1439 {
		state.QuietEndMinute = 1439
	}
	if state.CooldownSeconds < 0 {
		state.CooldownSeconds = 0
	}

	const query = `INSERT INTO proactive_state (
			user_id, enabled, quiet_hours_enabled, quiet_start_minute, quiet_end_minute,
			timezone, cooldown_seconds, last_proactive_at, last_proactive_reason
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			quiet_hours_enabled = EXCLUDED.quiet_hours_enabled,
			quiet_start_minute = EXCLUDED.quiet_start_minute,
			quiet_end_minute = EXCLUDED.quiet_end_minute,
			timezone = EXCLUDED.timezone,
			cooldown_seconds = EXCLUDED.cooldown_seconds,
			last_proactive_at = COALESCE(EXCLUDED.last_proactive_at, proactive_state.last_proactive_at),
			last_proactive_reason = CASE
				WHEN EXCLUDED.last_proactive_reason <> '' THEN EXCLUDED.last_proactive_reason
				ELSE proactive_state.last_proactive_reason
			END,
			updated_at = NOW()`
	if _, err := r.pool.Exec(ctx, query,
		state.UserID,
		state.Enabled,
		state.QuietHoursEnabled,
		state.QuietStartMinute,
		state.QuietEndMinute,
		strings.TrimSpace(state.Timezone),
		state.CooldownSeconds,
		state.LastProactiveAt,
		strings.TrimSpace(state.LastProactiveReason),
	); err != nil {
		return fmt.Errorf("upsert proactive_state: %w", err)
	}
	return nil
}

// UpsertTask 写入一条主动任务；同 dedup_key 只保留一条，避免重复提醒。
func (r *PGProactiveRepository) UpsertTask(ctx context.Context, task ProactiveTask) (int64, bool, error) {
	task.UserID = strings.TrimSpace(task.UserID)
	task.SessionID = strings.TrimSpace(task.SessionID)
	task.TaskType = strings.TrimSpace(task.TaskType)
	task.Reason = strings.TrimSpace(task.Reason)
	task.DedupKey = strings.TrimSpace(task.DedupKey)
	if task.UserID == "" || task.TaskType == "" || task.DedupKey == "" {
		return 0, false, fmt.Errorf("user_id, task_type and dedup_key are required")
	}
	if task.SessionID == "" {
		task.SessionID = "default"
	}
	if task.RunAt.IsZero() {
		return 0, false, fmt.Errorf("run_at is required")
	}
	if task.NextAttemptAt.IsZero() {
		task.NextAttemptAt = task.RunAt
	}
	if task.CooldownSeconds < 0 {
		task.CooldownSeconds = 0
	}

	payloadBytes, err := json.Marshal(task.Payload)
	if err != nil {
		return 0, false, fmt.Errorf("marshal proactive task payload: %w", err)
	}
	if len(payloadBytes) == 0 {
		payloadBytes = []byte("{}")
	}

	const insertQuery = `INSERT INTO proactive_tasks (
			user_id, session_id, task_type, status, reason, dedup_key, source_message_id,
			source_life_event_id, run_at, next_attempt_at, cooldown_seconds, payload, last_error
		) VALUES ($1, $2, $3, 'pending', $4, $5, $6, $7, $8, $9, $10, $11::jsonb, '')
		ON CONFLICT (user_id, dedup_key) DO NOTHING
		RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, insertQuery,
		task.UserID,
		task.SessionID,
		task.TaskType,
		task.Reason,
		task.DedupKey,
		task.SourceMessageID,
		task.SourceLifeEventID,
		task.RunAt,
		task.NextAttemptAt,
		task.CooldownSeconds,
		payloadBytes,
	).Scan(&id); err == nil {
		return id, true, nil
	} else if err != pgx.ErrNoRows {
		return 0, false, fmt.Errorf("insert proactive task: %w", err)
	}

	const existingQuery = `SELECT id FROM proactive_tasks WHERE user_id = $1 AND dedup_key = $2`
	if err := r.pool.QueryRow(ctx, existingQuery, task.UserID, task.DedupKey).Scan(&id); err != nil {
		return 0, false, fmt.Errorf("load existing proactive task: %w", err)
	}
	return id, false, nil
}

// BackfillLegacyLifeEventTasks 把旧版写进 life_events 的提醒/回访迁移成真正的主动任务。
func (r *PGProactiveRepository) BackfillLegacyLifeEventTasks(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 20
	}

	const query = `SELECT e.id, e.user_id, COALESCE(src.session_id, 'default') AS session_id,
		e.title, e.event_time, e.source_message_id
		FROM life_events e
		LEFT JOIN chat_messages src ON src.id = e.source_message_id AND src.user_id = e.user_id
		WHERE (e.title LIKE '提醒：%%' OR e.title = '情绪回访')
		  AND NOT EXISTS (
			SELECT 1 FROM proactive_tasks t WHERE t.source_life_event_id = e.id
		  )
		ORDER BY e.event_time ASC, e.id ASC
		LIMIT $1`

	rows, err := r.pool.Query(ctx, query, limit)
	if err != nil {
		return 0, fmt.Errorf("query legacy proactive life_events: %w", err)
	}
	defer rows.Close()

	created := 0
	for rows.Next() {
		var (
			eventID         int64
			userID          string
			sessionID       string
			title           string
			eventTime       time.Time
			sourceMessageID sql.NullInt64
		)
		if err := rows.Scan(&eventID, &userID, &sessionID, &title, &eventTime, &sourceMessageID); err != nil {
			return 0, fmt.Errorf("scan legacy proactive life_event: %w", err)
		}

		taskType := "event_reminder"
		cooldownSeconds := 12 * 60 * 60
		payload := map[string]any{
			"legacy_life_event_id": eventID,
			"event_title":          strings.TrimSpace(title),
		}
		reason := "旧版 life_events 迁移"
		if strings.TrimSpace(title) == "情绪回访" {
			taskType = "care_followup"
			cooldownSeconds = 24 * 60 * 60
			reason = "旧版情绪回访迁移"
		} else {
			payload["target_title"] = strings.TrimSpace(strings.TrimPrefix(title, "提醒："))
			reason = "旧版事件提醒迁移"
		}

		var sourceID *int64
		if sourceMessageID.Valid {
			sourceID = &sourceMessageID.Int64
		}

		dedupKey := fmt.Sprintf("legacy|%d|%s|%s", eventID, taskType, eventTime.UTC().Format(time.RFC3339))
		if _, createdOne, err := r.UpsertTask(ctx, ProactiveTask{
			UserID:            userID,
			SessionID:         sessionID,
			TaskType:          taskType,
			Reason:            reason,
			DedupKey:          dedupKey,
			SourceMessageID:   sourceID,
			SourceLifeEventID: &eventID,
			RunAt:             eventTime,
			NextAttemptAt:     eventTime,
			CooldownSeconds:   cooldownSeconds,
			Payload:           payload,
		}); err != nil {
			return 0, err
		} else if createdOne {
			created++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate legacy proactive life_events: %w", err)
	}
	return created, nil
}

// ClaimDueTasks 把到期未处理的主动任务标记为 staging，避免多实例重复消费。
func (r *PGProactiveRepository) ClaimDueTasks(ctx context.Context, now time.Time, limit int) ([]ProactiveTask, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 {
		limit = 20
	}

	const query = `WITH picked AS (
			SELECT id
			FROM proactive_tasks
			WHERE status = 'pending'
			  AND run_at <= $1
			  AND next_attempt_at <= $1
			ORDER BY run_at ASC, id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE proactive_tasks t
		SET status = 'staging',
			updated_at = NOW()
		FROM picked
		WHERE t.id = picked.id
		RETURNING t.id, t.user_id, t.session_id, t.task_type, t.status, t.reason, t.dedup_key,
			t.source_message_id, t.source_life_event_id, t.run_at, t.next_attempt_at, t.cooldown_seconds,
			t.payload, t.last_error, t.queued_at, t.sent_at, t.created_at, t.updated_at`

	rows, err := r.pool.Query(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due proactive tasks: %w", err)
	}
	defer rows.Close()

	out := make([]ProactiveTask, 0, limit)
	for rows.Next() {
		item, err := scanProactiveTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due proactive tasks: %w", err)
	}
	return out, nil
}

// RescheduleTask 把任务重新放回 pending，并设置下一次允许尝试的时间。
func (r *PGProactiveRepository) RescheduleTask(ctx context.Context, taskID int64, nextAttemptAt time.Time, lastError string) error {
	const query = `UPDATE proactive_tasks
		SET status = 'pending',
			next_attempt_at = $2,
			last_error = $3,
			updated_at = NOW()
		WHERE id = $1`
	if _, err := r.pool.Exec(ctx, query, taskID, nextAttemptAt, strings.TrimSpace(lastError)); err != nil {
		return fmt.Errorf("reschedule proactive task: %w", err)
	}
	return nil
}

// CancelTask 取消任务，不再继续尝试。
func (r *PGProactiveRepository) CancelTask(ctx context.Context, taskID int64, lastError string) error {
	const query = `UPDATE proactive_tasks
		SET status = 'cancelled',
			last_error = $2,
			updated_at = NOW()
		WHERE id = $1`
	if _, err := r.pool.Exec(ctx, query, taskID, strings.TrimSpace(lastError)); err != nil {
		return fmt.Errorf("cancel proactive task: %w", err)
	}
	return nil
}

// MarkTaskQueued 标记任务已进入 outbound_queue，后续由发送器真正发出。
func (r *PGProactiveRepository) MarkTaskQueued(ctx context.Context, taskID int64, queuedAt time.Time) error {
	if queuedAt.IsZero() {
		queuedAt = time.Now()
	}
	const query = `UPDATE proactive_tasks
		SET status = 'queued',
			queued_at = $2,
			last_error = '',
			updated_at = NOW()
		WHERE id = $1`
	if _, err := r.pool.Exec(ctx, query, taskID, queuedAt); err != nil {
		return fmt.Errorf("mark proactive task queued: %w", err)
	}
	return nil
}

// MarkTaskFailed 把任务标记为失败，表示主动链路最终未送达。
func (r *PGProactiveRepository) MarkTaskFailed(ctx context.Context, taskID int64, lastError string) error {
	const query = `UPDATE proactive_tasks
		SET status = 'failed',
			last_error = $2,
			updated_at = NOW()
		WHERE id = $1`
	if _, err := r.pool.Exec(ctx, query, taskID, strings.TrimSpace(lastError)); err != nil {
		return fmt.Errorf("mark proactive task failed: %w", err)
	}
	return nil
}

// EnqueueOutbound 把任务放入发送队列；若 dedup_key 已存在则不会重复排队。
func (r *PGProactiveRepository) EnqueueOutbound(ctx context.Context, item OutboundQueueItem) (int64, bool, error) {
	item.UserID = strings.TrimSpace(item.UserID)
	item.SessionID = strings.TrimSpace(item.SessionID)
	item.Reason = strings.TrimSpace(item.Reason)
	item.DedupKey = strings.TrimSpace(item.DedupKey)
	if item.UserID == "" || item.TaskID <= 0 || item.DedupKey == "" {
		return 0, false, fmt.Errorf("user_id, task_id and dedup_key are required")
	}
	if item.SessionID == "" {
		item.SessionID = "default"
	}
	if item.NextAttemptAt.IsZero() {
		item.NextAttemptAt = time.Now()
	}
	if item.MaxAttempts <= 0 {
		item.MaxAttempts = 3
	}

	payloadBytes, err := json.Marshal(item.Payload)
	if err != nil {
		return 0, false, fmt.Errorf("marshal outbound payload: %w", err)
	}
	if len(payloadBytes) == 0 {
		payloadBytes = []byte("{}")
	}

	const insertQuery = `INSERT INTO outbound_queue (
			user_id, session_id, task_id, status, reason, dedup_key, payload, attempts,
			max_attempts, next_attempt_at, last_error
		) VALUES ($1, $2, $3, 'pending', $4, $5, $6::jsonb, 0, $7, $8, '')
		ON CONFLICT (user_id, dedup_key) DO NOTHING
		RETURNING id`

	var id int64
	if err := r.pool.QueryRow(ctx, insertQuery,
		item.UserID,
		item.SessionID,
		item.TaskID,
		item.Reason,
		item.DedupKey,
		payloadBytes,
		item.MaxAttempts,
		item.NextAttemptAt,
	).Scan(&id); err == nil {
		return id, true, nil
	} else if err != pgx.ErrNoRows {
		return 0, false, fmt.Errorf("insert outbound_queue: %w", err)
	}

	const existingQuery = `SELECT id FROM outbound_queue WHERE user_id = $1 AND dedup_key = $2`
	if err := r.pool.QueryRow(ctx, existingQuery, item.UserID, item.DedupKey).Scan(&id); err != nil {
		return 0, false, fmt.Errorf("load existing outbound_queue row: %w", err)
	}
	return id, false, nil
}

// ClaimDueOutbound 认领待发送的消息，并把 attempts +1。
func (r *PGProactiveRepository) ClaimDueOutbound(ctx context.Context, now time.Time, limit int) ([]OutboundQueueItem, error) {
	if now.IsZero() {
		now = time.Now()
	}
	if limit <= 0 {
		limit = 20
	}

	const query = `WITH picked AS (
			SELECT id
			FROM outbound_queue
			WHERE status IN ('pending', 'failed')
			  AND next_attempt_at <= $1
			  AND attempts < max_attempts
			ORDER BY next_attempt_at ASC, id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE outbound_queue q
		SET status = 'sending',
			attempts = q.attempts + 1,
			updated_at = NOW()
		FROM picked
		WHERE q.id = picked.id
		RETURNING q.id, q.user_id, q.session_id, q.task_id, q.status, q.reason, q.dedup_key,
			q.payload, q.attempts, q.max_attempts, q.next_attempt_at, q.last_error, q.chat_message_id,
			q.created_at, q.updated_at, q.delivered_at`

	rows, err := r.pool.Query(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("claim outbound queue: %w", err)
	}
	defer rows.Close()

	out := make([]OutboundQueueItem, 0, limit)
	for rows.Next() {
		item, err := scanOutboundQueueItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbound_queue items: %w", err)
	}
	return out, nil
}

// RescheduleOutbound 发送失败后重试，采用 failed 状态等待下一次 claim。
func (r *PGProactiveRepository) RescheduleOutbound(ctx context.Context, queueID int64, nextAttemptAt time.Time, lastError string) error {
	const query = `UPDATE outbound_queue
		SET status = 'failed',
			next_attempt_at = $2,
			last_error = $3,
			updated_at = NOW()
		WHERE id = $1`
	if _, err := r.pool.Exec(ctx, query, queueID, nextAttemptAt, strings.TrimSpace(lastError)); err != nil {
		return fmt.Errorf("reschedule outbound_queue row: %w", err)
	}
	return nil
}

// MarkOutboundFailedPermanently 表示发送重试次数已耗尽，不再继续重试。
func (r *PGProactiveRepository) MarkOutboundFailedPermanently(ctx context.Context, queueID, taskID int64, lastError string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin permanent outbound failure tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `UPDATE outbound_queue
		SET status = 'failed',
			last_error = $2,
			updated_at = NOW()
		WHERE id = $1`, queueID, strings.TrimSpace(lastError)); err != nil {
		return fmt.Errorf("mark outbound permanent failure: %w", err)
	}
	if taskID > 0 {
		if _, err := tx.Exec(ctx, `UPDATE proactive_tasks
			SET status = 'failed',
				last_error = $2,
				updated_at = NOW()
			WHERE id = $1`, taskID, strings.TrimSpace(lastError)); err != nil {
			return fmt.Errorf("mark proactive task permanent failure: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit permanent outbound failure tx: %w", err)
	}
	return nil
}

// MarkOutboundDelivered 真正把主动消息写入 chat_messages/chat_outbox，并更新队列与任务状态。
func (r *PGProactiveRepository) MarkOutboundDelivered(
	ctx context.Context,
	item OutboundQueueItem,
	content string,
	clientPayload map[string]any,
	deliveredAt time.Time,
) error {
	if deliveredAt.IsZero() {
		deliveredAt = time.Now()
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("content is required")
	}

	payloadBytes, err := json.Marshal(clientPayload)
	if err != nil {
		return fmt.Errorf("marshal client outbox payload: %w", err)
	}
	if len(payloadBytes) == 0 {
		payloadBytes = []byte("{}")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin outbound delivery tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var chatMessageID int64
	if err := tx.QueryRow(ctx, `INSERT INTO chat_messages (user_id, session_id, role, content)
		VALUES ($1, $2, 'assistant', $3)
		RETURNING id`, item.UserID, item.SessionID, content).Scan(&chatMessageID); err != nil {
		return fmt.Errorf("insert proactive chat_message: %w", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO chat_outbox (
			user_id, session_id, chat_message_id, payload, created_at, delivered_at, source_task_id, source_queue_id
		) VALUES ($1, $2, $3, $4::jsonb, $5, $5, $6, $7)`,
		item.UserID, item.SessionID, chatMessageID, payloadBytes, deliveredAt, item.TaskID, item.ID,
	); err != nil {
		return fmt.Errorf("insert chat_outbox: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE outbound_queue
		SET status = 'delivered',
			chat_message_id = $2,
			delivered_at = $3,
			last_error = '',
			updated_at = NOW()
		WHERE id = $1`, item.ID, chatMessageID, deliveredAt); err != nil {
		return fmt.Errorf("mark outbound delivered: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE proactive_tasks
		SET status = 'sent',
			sent_at = $2,
			last_error = '',
			updated_at = NOW()
		WHERE id = $1`, item.TaskID, deliveredAt); err != nil {
		return fmt.Errorf("mark proactive task sent: %w", err)
	}

	if _, err := tx.Exec(ctx, `INSERT INTO proactive_state (user_id, last_proactive_at, last_proactive_reason)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET
			last_proactive_at = EXCLUDED.last_proactive_at,
			last_proactive_reason = EXCLUDED.last_proactive_reason,
			updated_at = NOW()`, item.UserID, deliveredAt, item.Reason); err != nil {
		return fmt.Errorf("update proactive_state sent timestamp: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit outbound delivery tx: %w", err)
	}
	return nil
}

// ListTasks 读取最近主动任务，供 debug 和验证使用。
func (r *PGProactiveRepository) ListTasks(ctx context.Context, userID, sessionID string, limit int) ([]ProactiveTask, error) {
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if sessionID == "" {
		const query = `SELECT id, user_id, session_id, task_type, status, reason, dedup_key,
			source_message_id, source_life_event_id, run_at, next_attempt_at, cooldown_seconds,
			payload, last_error, queued_at, sent_at, created_at, updated_at
			FROM proactive_tasks
			WHERE user_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
		rows, err := r.pool.Query(ctx, query, userID, limit)
		if err != nil {
			return nil, fmt.Errorf("list proactive tasks: %w", err)
		}
		defer rows.Close()
		return collectProactiveTasks(rows)
	}

	const query = `SELECT id, user_id, session_id, task_type, status, reason, dedup_key,
		source_message_id, source_life_event_id, run_at, next_attempt_at, cooldown_seconds,
		payload, last_error, queued_at, sent_at, created_at, updated_at
		FROM proactive_tasks
		WHERE user_id = $1 AND session_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3`
	rows, err := r.pool.Query(ctx, query, userID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list proactive tasks by session: %w", err)
	}
	defer rows.Close()
	return collectProactiveTasks(rows)
}

// ListOutbound 读取最近发送队列，供 debug 和验证使用。
func (r *PGProactiveRepository) ListOutbound(ctx context.Context, userID, sessionID string, limit int) ([]OutboundQueueItem, error) {
	userID = strings.TrimSpace(userID)
	sessionID = strings.TrimSpace(sessionID)
	if userID == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if limit <= 0 {
		limit = 20
	}
	if sessionID == "" {
		const query = `SELECT id, user_id, session_id, task_id, status, reason, dedup_key,
			payload, attempts, max_attempts, next_attempt_at, last_error, chat_message_id,
			created_at, updated_at, delivered_at
			FROM outbound_queue
			WHERE user_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`
		rows, err := r.pool.Query(ctx, query, userID, limit)
		if err != nil {
			return nil, fmt.Errorf("list outbound queue: %w", err)
		}
		defer rows.Close()
		return collectOutboundItems(rows)
	}

	const query = `SELECT id, user_id, session_id, task_id, status, reason, dedup_key,
		payload, attempts, max_attempts, next_attempt_at, last_error, chat_message_id,
		created_at, updated_at, delivered_at
		FROM outbound_queue
		WHERE user_id = $1 AND session_id = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3`
	rows, err := r.pool.Query(ctx, query, userID, sessionID, limit)
	if err != nil {
		return nil, fmt.Errorf("list outbound queue by session: %w", err)
	}
	defer rows.Close()
	return collectOutboundItems(rows)
}

// scanProactiveTask 负责把一条任务记录解码成结构体。
func scanProactiveTask(rows pgx.Rows) (ProactiveTask, error) {
	var (
		item       ProactiveTask
		payloadRaw []byte
	)
	if err := rows.Scan(
		&item.ID,
		&item.UserID,
		&item.SessionID,
		&item.TaskType,
		&item.Status,
		&item.Reason,
		&item.DedupKey,
		&item.SourceMessageID,
		&item.SourceLifeEventID,
		&item.RunAt,
		&item.NextAttemptAt,
		&item.CooldownSeconds,
		&payloadRaw,
		&item.LastError,
		&item.QueuedAt,
		&item.SentAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return ProactiveTask{}, fmt.Errorf("scan proactive task: %w", err)
	}
	if len(payloadRaw) > 0 {
		if err := json.Unmarshal(payloadRaw, &item.Payload); err != nil {
			item.Payload = map[string]any{}
		}
	}
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	return item, nil
}

// scanOutboundQueueItem 负责把一条发送队列记录解码成结构体。
func scanOutboundQueueItem(rows pgx.Rows) (OutboundQueueItem, error) {
	var (
		item       OutboundQueueItem
		payloadRaw []byte
	)
	if err := rows.Scan(
		&item.ID,
		&item.UserID,
		&item.SessionID,
		&item.TaskID,
		&item.Status,
		&item.Reason,
		&item.DedupKey,
		&payloadRaw,
		&item.Attempts,
		&item.MaxAttempts,
		&item.NextAttemptAt,
		&item.LastError,
		&item.ChatMessageID,
		&item.CreatedAt,
		&item.UpdatedAt,
		&item.DeliveredAt,
	); err != nil {
		return OutboundQueueItem{}, fmt.Errorf("scan outbound queue item: %w", err)
	}
	if len(payloadRaw) > 0 {
		if err := json.Unmarshal(payloadRaw, &item.Payload); err != nil {
			item.Payload = map[string]any{}
		}
	}
	if item.Payload == nil {
		item.Payload = map[string]any{}
	}
	return item, nil
}

// collectProactiveTasks 把查询结果批量收集为任务切片。
func collectProactiveTasks(rows pgx.Rows) ([]ProactiveTask, error) {
	out := make([]ProactiveTask, 0, 16)
	for rows.Next() {
		item, err := scanProactiveTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proactive tasks: %w", err)
	}
	return out, nil
}

// collectOutboundItems 把查询结果批量收集为发送队列切片。
func collectOutboundItems(rows pgx.Rows) ([]OutboundQueueItem, error) {
	out := make([]OutboundQueueItem, 0, 16)
	for rows.Next() {
		item, err := scanOutboundQueueItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbound queue: %w", err)
	}
	return out, nil
}
