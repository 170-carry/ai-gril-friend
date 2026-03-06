package proactive

import (
	"context"
	"log"
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
	repo   DispatcherRepository
	cfg    DispatcherConfig
	stopCh chan struct{}
	wg     sync.WaitGroup
	start  atomic.Bool
}

// NewDispatcher 创建主动消息调度器。
func NewDispatcher(repository DispatcherRepository, cfg DispatcherConfig) *Dispatcher {
	if repository == nil {
		return nil
	}
	return &Dispatcher{
		repo:   repository,
		cfg:    cfg.Normalize(),
		stopCh: make(chan struct{}),
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
		state, err := d.repo.GetState(ctx, task.UserID)
		if err != nil {
			return staged, err
		}

		if !state.Enabled {
			if err := d.repo.CancelTask(ctx, task.ID, "用户已关闭主动消息"); err != nil {
				return staged, err
			}
			continue
		}

		if deferQuiet, nextAt := ShouldDeferForQuietHours(state, now); deferQuiet {
			if err := d.repo.RescheduleTask(ctx, task.ID, nextAt, "命中免打扰时段"); err != nil {
				return staged, err
			}
			continue
		}

		if deferCooldown, nextAt := ShouldDeferForCooldown(state, task, now); deferCooldown {
			if err := d.repo.RescheduleTask(ctx, task.ID, nextAt, "命中主动冷却时间"); err != nil {
				return staged, err
			}
			continue
		}

		_, created, err := d.repo.EnqueueOutbound(ctx, repo.OutboundQueueItem{
			UserID:        task.UserID,
			SessionID:     task.SessionID,
			TaskID:        task.ID,
			Reason:        task.Reason,
			DedupKey:      task.DedupKey,
			Payload:       BuildOutboundPayload(task),
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
