package proactive

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ai-gf/internal/repo"
)

// SchedulerRepository 是主动任务写入所需的最小仓库接口。
type SchedulerRepository interface {
	EnsureState(ctx context.Context, userID string) (repo.ProactiveState, error)
	UpsertTask(ctx context.Context, task repo.ProactiveTask) (int64, bool, error)
}

// DispatcherRepository 是主动运行时消费所需的仓库接口。
type DispatcherRepository interface {
	BackfillLegacyLifeEventTasks(ctx context.Context, limit int) (int, error)
	ClaimDueTasks(ctx context.Context, now time.Time, limit int) ([]repo.ProactiveTask, error)
	GetConversationTopic(ctx context.Context, userID, sessionID, topicKey string) (repo.ConversationTopic, error)
	GetState(ctx context.Context, userID string) (repo.ProactiveState, error)
	RescheduleTask(ctx context.Context, taskID int64, nextAttemptAt time.Time, lastError string) error
	CancelTask(ctx context.Context, taskID int64, lastError string) error
	MarkTaskQueued(ctx context.Context, taskID int64, queuedAt time.Time) error
	MarkTaskFailed(ctx context.Context, taskID int64, lastError string) error
	EnqueueOutbound(ctx context.Context, item repo.OutboundQueueItem) (int64, bool, error)
	ClaimDueOutbound(ctx context.Context, now time.Time, limit int) ([]repo.OutboundQueueItem, error)
	RescheduleOutbound(ctx context.Context, queueID int64, nextAttemptAt time.Time, lastError string) error
	MarkOutboundFailedPermanently(ctx context.Context, queueID, taskID int64, lastError string) error
	MarkOutboundDelivered(ctx context.Context, item repo.OutboundQueueItem, content string, clientPayload map[string]any, deliveredAt time.Time) error
}

// DebugRepository 暴露主动系统调试与配置能力。
type DebugRepository interface {
	GetState(ctx context.Context, userID string) (repo.ProactiveState, error)
	UpsertState(ctx context.Context, state repo.ProactiveState) error
	ListTasks(ctx context.Context, userID, sessionID string, limit int) ([]repo.ProactiveTask, error)
	ListOutbound(ctx context.Context, userID, sessionID string, limit int) ([]repo.OutboundQueueItem, error)
}

// Config 是主动系统的默认参数。
type Config struct {
	DefaultCooldownSeconds int
	QueueMaxAttempts       int
}

// DefaultConfig 返回主动系统的默认配置。
func DefaultConfig() Config {
	return Config{
		DefaultCooldownSeconds: 12 * 60 * 60,
		QueueMaxAttempts:       3,
	}
}

// Normalize 对主动系统配置做边界修正。
func (c Config) Normalize() Config {
	def := DefaultConfig()
	if c.DefaultCooldownSeconds < 0 {
		c.DefaultCooldownSeconds = def.DefaultCooldownSeconds
	}
	if c.QueueMaxAttempts <= 0 {
		c.QueueMaxAttempts = def.QueueMaxAttempts
	}
	return c
}

// ScheduleRequest 是从 CE Action 落到 proactive_tasks 的标准输入。
type ScheduleRequest struct {
	UserID            string
	SessionID         string
	TaskType          string
	Reason            string
	DedupKey          string
	SourceMessageID   *int64
	SourceLifeEventID *int64
	RunAt             time.Time
	CooldownSeconds   int
	Payload           map[string]any
}

// Scheduler 是主动任务调度接口，供 memory worker 调用。
type Scheduler interface {
	Schedule(ctx context.Context, req ScheduleRequest) error
}

// Service 负责把 CE 的主动钩子写成真正的主动任务。
type Service struct {
	repo SchedulerRepository
	cfg  Config
}

// NewService 创建主动任务调度服务。
func NewService(repository SchedulerRepository, cfg Config) *Service {
	if repository == nil {
		return nil
	}
	return &Service{
		repo: repository,
		cfg:  cfg.Normalize(),
	}
}

// Schedule 把一次“未来主动”的意图写入 proactive_tasks。
func (s *Service) Schedule(ctx context.Context, req ScheduleRequest) error {
	if s == nil || s.repo == nil {
		return nil
	}
	req.UserID = strings.TrimSpace(req.UserID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.TaskType = strings.TrimSpace(req.TaskType)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.UserID == "" || req.TaskType == "" {
		return fmt.Errorf("user_id and task_type are required")
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	if req.RunAt.IsZero() {
		return fmt.Errorf("run_at is required")
	}
	if req.CooldownSeconds <= 0 {
		req.CooldownSeconds = s.cfg.DefaultCooldownSeconds
	}

	payload := clonePayload(req.Payload)
	payload["task_type"] = req.TaskType
	payload["reason"] = req.Reason
	payload["run_at"] = req.RunAt.UTC().Format(time.RFC3339)
	if req.SourceMessageID != nil {
		payload["source_message_id"] = *req.SourceMessageID
	}
	if req.SourceLifeEventID != nil {
		payload["source_life_event_id"] = *req.SourceLifeEventID
	}

	if _, err := s.repo.EnsureState(ctx, req.UserID); err != nil {
		return err
	}

	dedupKey, err := BuildDedupKey(req)
	if err != nil {
		return err
	}
	if _, _, err := s.repo.UpsertTask(ctx, repo.ProactiveTask{
		UserID:            req.UserID,
		SessionID:         req.SessionID,
		TaskType:          req.TaskType,
		Reason:            req.Reason,
		DedupKey:          dedupKey,
		SourceMessageID:   req.SourceMessageID,
		SourceLifeEventID: req.SourceLifeEventID,
		RunAt:             req.RunAt,
		NextAttemptAt:     req.RunAt,
		CooldownSeconds:   req.CooldownSeconds,
		Payload:           payload,
	}); err != nil {
		return err
	}
	return nil
}

// BuildDedupKey 为主动任务生成稳定的去重键，保证同一输入重复写入不会重复提醒。
func BuildDedupKey(req ScheduleRequest) (string, error) {
	if strings.TrimSpace(req.DedupKey) != "" {
		return strings.TrimSpace(req.DedupKey), nil
	}

	payloadBytes, err := json.Marshal(clonePayload(req.Payload))
	if err != nil {
		return "", fmt.Errorf("marshal proactive dedup payload: %w", err)
	}
	raw := struct {
		TaskType          string `json:"task_type"`
		SessionID         string `json:"session_id"`
		RunAt             string `json:"run_at"`
		SourceMessageID   int64  `json:"source_message_id"`
		SourceLifeEventID int64  `json:"source_life_event_id"`
		Payload           string `json:"payload"`
	}{
		TaskType:  strings.TrimSpace(req.TaskType),
		SessionID: strings.TrimSpace(req.SessionID),
		RunAt:     req.RunAt.UTC().Format(time.RFC3339),
		Payload:   string(payloadBytes),
	}
	if req.SourceMessageID != nil {
		raw.SourceMessageID = *req.SourceMessageID
	}
	if req.SourceLifeEventID != nil {
		raw.SourceLifeEventID = *req.SourceLifeEventID
	}

	buf, err := json.Marshal(raw)
	if err != nil {
		return "", fmt.Errorf("marshal proactive dedup raw: %w", err)
	}
	sum := sha1.Sum(buf)
	return raw.TaskType + "_" + hex.EncodeToString(sum[:8]), nil
}

// BuildOutboundMessage 根据任务类型和 payload 生成最终主动消息内容与客户端可读元数据。
func BuildOutboundMessage(item repo.OutboundQueueItem) (string, map[string]any) {
	payload := clonePayload(item.Payload)
	taskType := strings.TrimSpace(toString(payload["task_type"]))
	reason := strings.TrimSpace(item.Reason)
	runAt := strings.TrimSpace(toString(payload["run_at"]))

	clientPayload := clonePayload(payload)
	clientPayload["reason"] = reason
	clientPayload["queue_id"] = item.ID
	clientPayload["task_id"] = item.TaskID

	switch taskType {
	case "event_reminder":
		target := strings.TrimSpace(toString(payload["target_title"]))
		if target == "" {
			target = strings.TrimSpace(toString(payload["event_title"]))
		}
		if target == "" {
			target = "这件事"
		}
		clientPayload["kind"] = "event_reminder"
		clientPayload["target_title"] = target
		return fmt.Sprintf("小提醒：你之前提到「%s」，现在差不多到时间啦。要我陪你快速过一遍吗？", target), clientPayload
	case "care_followup":
		clientPayload["kind"] = "care_followup"
		return "我来回访一下你刚才的状态，现在感觉怎么样了？如果你愿意，我还在这陪你。", clientPayload
	case "topic_reengage":
		label := strings.TrimSpace(toString(payload["topic_label"]))
		if label == "" {
			label = strings.TrimSpace(toString(payload["topic_key"]))
		}
		if label == "" {
			label = "昨天那件事"
		}
		callbackHint := strings.TrimSpace(toString(payload["callback_hint"]))
		secondaryLabel := strings.TrimSpace(toString(payload["secondary_topic_label"]))
		secondaryRelation := normalizeOutboundTopicRelationType(toString(payload["secondary_relation_type"]))
		clientPayload["kind"] = "topic_reengage"
		clientPayload["topic_label"] = label
		if secondaryLabel != "" {
			clientPayload["secondary_topic_label"] = secondaryLabel
			clientPayload["secondary_relation_type"] = secondaryRelation
			switch secondaryRelation {
			case "cause_effect":
				return fmt.Sprintf("突然想起你昨天提到的「%s」，好像还牵着「%s」那条线。后来有新进展吗？", label, secondaryLabel), clientPayload
			case "progression":
				return fmt.Sprintf("昨天聊到的「%s」，后来有接着走到「%s」那一步吗？", label, secondaryLabel), clientPayload
			case "context":
				return fmt.Sprintf("突然想起你昨天提到的「%s」，连着「%s」那条线我也记着。后来怎么样了？", label, secondaryLabel), clientPayload
			case "contrast":
				return fmt.Sprintf("昨天那条「%s」和「%s」我都还记得。后来哪边变化更大？", label, secondaryLabel), clientPayload
			}
		}
		if callbackHint != "" {
			clientPayload["callback_hint"] = callbackHint
			return fmt.Sprintf("突然想起你昨天提到的「%s」，%s。后来有新进展吗？", label, callbackHint), clientPayload
		}
		return fmt.Sprintf("昨天没讲完的「%s」，我又想起来了。后来推进得怎么样啦？", label), clientPayload
	default:
		title := strings.TrimSpace(toString(payload["event_title"]))
		if title == "" {
			title = strings.TrimSpace(toString(payload["title"]))
		}
		if title == "" {
			title = "这件事"
		}
		clientPayload["kind"] = "generic_proactive"
		if runAt != "" {
			clientPayload["scheduled_at"] = runAt
		}
		return fmt.Sprintf("你之前提到「%s」，我来轻轻提醒你一下。", title), clientPayload
	}
}

// ShouldDeferForQuietHours 判断当前是否命中免打扰时间段，并给出下一次可发送时间。
func ShouldDeferForQuietHours(state repo.ProactiveState, now time.Time) (bool, time.Time) {
	if !state.QuietHoursEnabled {
		return false, time.Time{}
	}
	loc := resolveLocation(state.Timezone)
	localNow := now.In(loc)
	start := normalizeMinute(state.QuietStartMinute)
	end := normalizeMinute(state.QuietEndMinute)
	if start == end {
		return false, time.Time{}
	}

	currentMinute := localNow.Hour()*60 + localNow.Minute()
	inQuiet := false
	if start < end {
		inQuiet = currentMinute >= start && currentMinute < end
	} else {
		inQuiet = currentMinute >= start || currentMinute < end
	}
	if !inQuiet {
		return false, time.Time{}
	}

	nextLocal := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), end/60, end%60, 0, 0, loc)
	if !nextLocal.After(localNow) {
		nextLocal = nextLocal.Add(24 * time.Hour)
	}
	return true, nextLocal
}

// ShouldDeferForCooldown 判断是否命中用户冷却时间，若命中则返回下一次允许主动的时间。
func ShouldDeferForCooldown(state repo.ProactiveState, task repo.ProactiveTask, now time.Time) (bool, time.Time) {
	if state.LastProactiveAt == nil {
		return false, time.Time{}
	}
	cooldownSeconds := task.CooldownSeconds
	if state.CooldownSeconds > cooldownSeconds {
		cooldownSeconds = state.CooldownSeconds
	}
	if cooldownSeconds <= 0 {
		return false, time.Time{}
	}

	nextAllowed := state.LastProactiveAt.Add(time.Duration(cooldownSeconds) * time.Second)
	if now.Before(nextAllowed) {
		return true, nextAllowed
	}
	return false, time.Time{}
}

// NextRetryDelay 为 outbound_queue 提供简单指数式退避。
func NextRetryDelay(attempt int) time.Duration {
	switch {
	case attempt <= 1:
		return 1 * time.Minute
	case attempt == 2:
		return 5 * time.Minute
	case attempt == 3:
		return 15 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// BuildOutboundPayload 把任务转换成队列 payload，后续发送时无需再查一次任务详情。
func BuildOutboundPayload(task repo.ProactiveTask) map[string]any {
	payload := clonePayload(task.Payload)
	payload["task_type"] = task.TaskType
	payload["reason"] = task.Reason
	payload["run_at"] = task.RunAt.UTC().Format(time.RFC3339)
	payload["task_id"] = task.ID
	if task.SourceMessageID != nil {
		payload["source_message_id"] = *task.SourceMessageID
	}
	if task.SourceLifeEventID != nil {
		payload["source_life_event_id"] = *task.SourceLifeEventID
	}
	return payload
}

// clonePayload 复制一份 payload，避免调用方之间共享底层 map。
func clonePayload(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeOutboundTopicRelationType(relationType string) string {
	switch strings.TrimSpace(relationType) {
	case "cause_effect":
		return "cause_effect"
	case "progression":
		return "progression"
	case "contrast":
		return "contrast"
	case "context":
		return "context"
	default:
		return "co_occurs"
	}
}

// toString 把任意 JSON 值安全转成字符串。
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	case float64:
		return fmt.Sprintf("%.0f", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	default:
		return ""
	}
}

// resolveLocation 优先使用用户配置时区，解析失败则回退到服务端本地时区。
func resolveLocation(tz string) *time.Location {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Local
	}
	return loc
}

// normalizeMinute 把分钟数压到 0~1439，避免非法配置影响调度。
func normalizeMinute(v int) int {
	if v < 0 {
		return 0
	}
	if v > 1439 {
		return 1439
	}
	return v
}
