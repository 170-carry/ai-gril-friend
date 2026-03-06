package proactive

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ai-gf/internal/repo"
)

// DispatcherConfig 是主动运行时扫描参数。
type DispatcherConfig struct {
	Enabled      bool
	ScanInterval time.Duration
	BatchSize    int
	JobTimeout   time.Duration
	MaxAttempts  int
}

// Normalize 对调度配置做边界修正。
func (c DispatcherConfig) Normalize() DispatcherConfig {
	if c.ScanInterval <= 0 {
		c.ScanInterval = 10 * time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 20
	}
	if c.JobTimeout <= 0 {
		c.JobTimeout = 6 * time.Second
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	return c
}

// Dispatcher 负责把 proactive_tasks 推进到 outbound_queue，再送入 chat_messages/chat_outbox。
type Dispatcher struct {
	repo     DispatcherRepository
	cfg      DispatcherConfig
	decision *DecisionEngine
	stopCh   chan struct{}
	wg       sync.WaitGroup
	start    atomic.Bool
}

// NewDispatcher 创建主动消息调度器。
func NewDispatcher(repository DispatcherRepository, cfg DispatcherConfig) *Dispatcher {
	if repository == nil {
		return nil
	}
	return &Dispatcher{
		repo:     repository,
		cfg:      cfg.Normalize(),
		decision: NewDecisionEngine(DefaultDecisionConfig()),
		stopCh:   make(chan struct{}),
	}
}

// Start 启动后台扫描协程，并先执行一次扫描。
func (d *Dispatcher) Start() {
	if d == nil || !d.cfg.Enabled {
		return
	}
	if !d.start.CompareAndSwap(false, true) {
		return
	}

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()

		d.runOnce()

		ticker := time.NewTicker(d.cfg.ScanInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				d.runOnce()
			case <-d.stopCh:
				return
			}
		}
	}()
}

// Stop 停止调度器并等待退出。
func (d *Dispatcher) Stop() {
	if d == nil {
		return
	}
	if !d.start.CompareAndSwap(true, false) {
		return
	}
	close(d.stopCh)
	d.wg.Wait()
}

// runOnce 执行一轮主动任务推进：兼容迁移 -> 任务入队 -> 队列发送。
func (d *Dispatcher) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), d.cfg.JobTimeout)
	defer cancel()

	legacyCount, err := d.repo.BackfillLegacyLifeEventTasks(ctx, d.cfg.BatchSize)
	if err != nil {
		log.Printf("proactive dispatcher legacy backfill failed: %v", err)
		return
	}

	now := time.Now()
	stagedCount, err := d.stageDueTasks(ctx, now)
	if err != nil {
		log.Printf("proactive dispatcher stage tasks failed: %v", err)
		return
	}

	deliveredCount, err := d.dispatchOutbound(ctx, now)
	if err != nil {
		log.Printf("proactive dispatcher dispatch outbound failed: %v", err)
		return
	}

	if legacyCount > 0 || stagedCount > 0 || deliveredCount > 0 {
		log.Printf("proactive dispatcher cycle: legacy=%d staged=%d delivered=%d", legacyCount, stagedCount, deliveredCount)
	}
}

// stageDueTasks 把真正到点且允许发送的任务推进到 outbound_queue。
func (d *Dispatcher) stageDueTasks(ctx context.Context, now time.Time) (int, error) {
	tasks, err := d.repo.ClaimDueTasks(ctx, now, d.cfg.BatchSize)
	if err != nil {
		return 0, err
	}

	staged := 0
	for _, task := range tasks {
		if action, nextAt, reason, err := d.evaluateTopicTask(ctx, task, now); err != nil {
			return staged, err
		} else {
			switch action {
			case DecisionCancel:
				if err := d.repo.CancelTask(ctx, task.ID, reason); err != nil {
					return staged, err
				}
				continue
			case DecisionDefer:
				if nextAt.IsZero() {
					nextAt = now.Add(30 * time.Minute)
				}
				if err := d.repo.RescheduleTask(ctx, task.ID, nextAt, reason); err != nil {
					return staged, err
				}
				continue
			}
		}

		state, err := d.repo.GetState(ctx, task.UserID)
		if err != nil {
			return staged, err
		}
		decision := d.decision.Evaluate(task, state, now)
		switch decision.Action {
		case DecisionCancel:
			if err := d.repo.CancelTask(ctx, task.ID, decision.Summary); err != nil {
				return staged, err
			}
			continue
		case DecisionDefer:
			nextAt := now.Add(10 * time.Minute)
			if decision.NextAttemptAt != nil && !decision.NextAttemptAt.IsZero() {
				nextAt = *decision.NextAttemptAt
			}
			if err := d.repo.RescheduleTask(ctx, task.ID, nextAt, decision.Summary); err != nil {
				return staged, err
			}
			continue
		}

		payload, err := d.buildOutboundPayload(ctx, task, now)
		if err != nil {
			return staged, err
		}

		_, created, err := d.repo.EnqueueOutbound(ctx, repo.OutboundQueueItem{
			UserID:        task.UserID,
			SessionID:     task.SessionID,
			TaskID:        task.ID,
			Reason:        task.Reason,
			DedupKey:      task.DedupKey,
			Payload:       payload,
			MaxAttempts:   d.cfg.MaxAttempts,
			NextAttemptAt: now,
		})
		if err != nil {
			if resErr := d.repo.RescheduleTask(ctx, task.ID, now.Add(1*time.Minute), err.Error()); resErr != nil {
				return staged, resErr
			}
			continue
		}

		if !created {
			if err := d.repo.CancelTask(ctx, task.ID, "命中主动任务去重，已有待发送记录"); err != nil {
				return staged, err
			}
			continue
		}

		if err := d.repo.MarkTaskQueued(ctx, task.ID, now); err != nil {
			return staged, err
		}
		staged++
	}
	return staged, nil
}

func (d *Dispatcher) evaluateTopicTask(ctx context.Context, task repo.ProactiveTask, now time.Time) (DecisionAction, time.Time, string, error) {
	if strings.TrimSpace(task.TaskType) != "topic_reengage" {
		return "", time.Time{}, "", nil
	}
	topicKey := strings.TrimSpace(toString(task.Payload["topic_key"]))
	if topicKey == "" {
		return DecisionCancel, time.Time{}, "缺少 topic_key，取消本次话题回钩", nil
	}
	topic, err := d.repo.GetConversationTopic(ctx, task.UserID, task.SessionID, topicKey)
	if err != nil {
		return "", time.Time{}, "", err
	}
	if strings.TrimSpace(topic.TopicKey) == "" {
		return DecisionCancel, time.Time{}, "对应话题不存在，取消本次回钩", nil
	}
	if strings.TrimSpace(topic.Status) != "active" {
		return DecisionCancel, time.Time{}, "话题已收束，不再主动回钩", nil
	}
	if topic.LastDiscussedAt != nil && topic.LastDiscussedAt.After(task.CreatedAt.Add(30*time.Minute)) {
		return DecisionCancel, time.Time{}, "话题在此后已被重新聊过，本次回钩已过时", nil
	}
	if topic.LastRecalledAt != nil && topic.LastRecalledAt.After(task.CreatedAt) {
		return DecisionCancel, time.Time{}, "该话题已经主动回钩过，跳过重复触达", nil
	}
	if topic.NextRecallAt != nil && now.Before(*topic.NextRecallAt) {
		return DecisionDefer, *topic.NextRecallAt, "话题仍在冷却，顺延到下一次适合回钩的时间", nil
	}
	return "", time.Time{}, "", nil
}

func (d *Dispatcher) buildOutboundPayload(ctx context.Context, task repo.ProactiveTask, now time.Time) (map[string]any, error) {
	payload := BuildOutboundPayload(task)
	if strings.TrimSpace(task.TaskType) != "topic_reengage" {
		return payload, nil
	}
	secondaryKey := strings.TrimSpace(toString(payload["secondary_topic_key"]))
	if secondaryKey == "" {
		return payload, nil
	}
	switch normalizeOutboundTopicRelationType(toString(payload["secondary_relation_type"])) {
	case "cause_effect", "progression", "context":
	default:
		delete(payload, "secondary_topic_key")
		delete(payload, "secondary_topic_label")
		delete(payload, "secondary_callback_hint")
		delete(payload, "secondary_relation_type")
		return payload, nil
	}

	secondaryTopic, err := d.repo.GetConversationTopic(ctx, task.UserID, task.SessionID, secondaryKey)
	if err != nil {
		return nil, err
	}
	if !isSecondaryTopicRecallable(task, secondaryTopic) {
		delete(payload, "secondary_topic_key")
		delete(payload, "secondary_topic_label")
		delete(payload, "secondary_callback_hint")
		delete(payload, "secondary_relation_type")
		return payload, nil
	}

	if label := strings.TrimSpace(secondaryTopic.TopicLabel); label != "" {
		payload["secondary_topic_label"] = label
	}
	if hint := strings.TrimSpace(secondaryTopic.CallbackHint); hint != "" {
		payload["secondary_callback_hint"] = hint
	}
	return payload, nil
}

func isSecondaryTopicRecallable(task repo.ProactiveTask, topic repo.ConversationTopic) bool {
	if strings.TrimSpace(topic.TopicKey) == "" {
		return false
	}
	if strings.TrimSpace(topic.Status) != "active" {
		return false
	}
	if !task.CreatedAt.IsZero() && topic.LastDiscussedAt != nil && topic.LastDiscussedAt.After(task.CreatedAt.Add(30*time.Minute)) {
		return false
	}
	if !task.CreatedAt.IsZero() && topic.LastRecalledAt != nil && topic.LastRecalledAt.After(task.CreatedAt) {
		return false
	}
	return true
}

// dispatchOutbound 把 outbound_queue 真正发送成 assistant 消息，并处理失败重试。
func (d *Dispatcher) dispatchOutbound(ctx context.Context, now time.Time) (int, error) {
	items, err := d.repo.ClaimDueOutbound(ctx, now, d.cfg.BatchSize)
	if err != nil {
		return 0, err
	}

	delivered := 0
	for _, item := range items {
		content, payload := BuildOutboundMessage(item)
		if content == "" {
			if err := d.repo.MarkOutboundFailedPermanently(ctx, item.ID, item.TaskID, "主动消息内容为空"); err != nil {
				return delivered, err
			}
			continue
		}

		if err := d.repo.MarkOutboundDelivered(ctx, item, content, payload, now); err != nil {
			if item.Attempts >= item.MaxAttempts {
				if failErr := d.repo.MarkOutboundFailedPermanently(ctx, item.ID, item.TaskID, err.Error()); failErr != nil {
					return delivered, failErr
				}
				continue
			}

			nextAttemptAt := now.Add(NextRetryDelay(item.Attempts))
			if retryErr := d.repo.RescheduleOutbound(ctx, item.ID, nextAttemptAt, err.Error()); retryErr != nil {
				return delivered, retryErr
			}
			continue
		}

		delivered++
	}
	return delivered, nil
}
