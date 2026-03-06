package proactive

import (
	"context"
	"errors"
	"testing"
	"time"

	"ai-gf/internal/repo"
)

// fakeDispatcherRepo 用最小实现承接 dispatcher 单测，验证状态流转是否正确。
type fakeDispatcherRepo struct {
	backfillCount           int
	tasks                   []repo.ProactiveTask
	outbound                []repo.OutboundQueueItem
	state                   repo.ProactiveState
	cancelledTaskIDs        []int64
	rescheduledTaskIDs      []int64
	rescheduledOutboundIDs  []int64
	permanentFailedQueueIDs []int64
	queuedTaskIDs           []int64
	enqueuedOutboundCount   int
	deliverErr              error
}

func (f *fakeDispatcherRepo) BackfillLegacyLifeEventTasks(ctx context.Context, limit int) (int, error) {
	return f.backfillCount, nil
}

func (f *fakeDispatcherRepo) ClaimDueTasks(ctx context.Context, now time.Time, limit int) ([]repo.ProactiveTask, error) {
	return f.tasks, nil
}

func (f *fakeDispatcherRepo) GetState(ctx context.Context, userID string) (repo.ProactiveState, error) {
	return f.state, nil
}

func (f *fakeDispatcherRepo) RescheduleTask(ctx context.Context, taskID int64, nextAttemptAt time.Time, lastError string) error {
	f.rescheduledTaskIDs = append(f.rescheduledTaskIDs, taskID)
	return nil
}

func (f *fakeDispatcherRepo) CancelTask(ctx context.Context, taskID int64, lastError string) error {
	f.cancelledTaskIDs = append(f.cancelledTaskIDs, taskID)
	return nil
}

func (f *fakeDispatcherRepo) MarkTaskQueued(ctx context.Context, taskID int64, queuedAt time.Time) error {
	f.queuedTaskIDs = append(f.queuedTaskIDs, taskID)
	return nil
}

func (f *fakeDispatcherRepo) MarkTaskFailed(ctx context.Context, taskID int64, lastError string) error {
	return nil
}

func (f *fakeDispatcherRepo) EnqueueOutbound(ctx context.Context, item repo.OutboundQueueItem) (int64, bool, error) {
	f.enqueuedOutboundCount++
	return int64(100 + f.enqueuedOutboundCount), true, nil
}

func (f *fakeDispatcherRepo) ClaimDueOutbound(ctx context.Context, now time.Time, limit int) ([]repo.OutboundQueueItem, error) {
	return f.outbound, nil
}

func (f *fakeDispatcherRepo) RescheduleOutbound(ctx context.Context, queueID int64, nextAttemptAt time.Time, lastError string) error {
	f.rescheduledOutboundIDs = append(f.rescheduledOutboundIDs, queueID)
	return nil
}

func (f *fakeDispatcherRepo) MarkOutboundFailedPermanently(ctx context.Context, queueID, taskID int64, lastError string) error {
	f.permanentFailedQueueIDs = append(f.permanentFailedQueueIDs, queueID)
	return nil
}

func (f *fakeDispatcherRepo) MarkOutboundDelivered(ctx context.Context, item repo.OutboundQueueItem, content string, clientPayload map[string]any, deliveredAt time.Time) error {
	return f.deliverErr
}

func TestDispatcher_StageDueTasksCancelsWhenUserDisabled(t *testing.T) {
	t.Parallel()

	repository := &fakeDispatcherRepo{
		state: repo.ProactiveState{
			Enabled: false,
		},
		tasks: []repo.ProactiveTask{
			{
				ID:        11,
				UserID:    "u1",
				SessionID: "s1",
				TaskType:  "care_followup",
				Reason:    "高强度负面情绪回访",
				DedupKey:  "care_followup_x",
				RunAt:     time.Now(),
			},
		},
	}
	dispatcher := NewDispatcher(repository, DispatcherConfig{
		Enabled: true,
	})

	staged, err := dispatcher.stageDueTasks(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("stageDueTasks returned error: %v", err)
	}
	if staged != 0 {
		t.Fatalf("expected no tasks staged when proactive disabled, got %d", staged)
	}
	if len(repository.cancelledTaskIDs) != 1 || repository.cancelledTaskIDs[0] != 11 {
		t.Fatalf("expected task cancelled, got %+v", repository.cancelledTaskIDs)
	}
}

func TestDispatcher_DispatchOutboundRetriesOnFailure(t *testing.T) {
	t.Parallel()

	repository := &fakeDispatcherRepo{
		deliverErr: errors.New("db write failed"),
		outbound: []repo.OutboundQueueItem{
			{
				ID:          21,
				TaskID:      31,
				Reason:      "高强度负面情绪回访",
				DedupKey:    "care_followup_x",
				Attempts:    1,
				MaxAttempts: 3,
				Payload: map[string]any{
					"task_type": "care_followup",
				},
			},
		},
	}
	dispatcher := NewDispatcher(repository, DispatcherConfig{
		Enabled: true,
	})

	delivered, err := dispatcher.dispatchOutbound(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("dispatchOutbound returned error: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("expected no delivered message on failure, got %d", delivered)
	}
	if len(repository.rescheduledOutboundIDs) != 1 || repository.rescheduledOutboundIDs[0] != 21 {
		t.Fatalf("expected outbound queue retry scheduled, got %+v", repository.rescheduledOutboundIDs)
	}
}

func TestDispatcher_DispatchOutboundMarksPermanentFailureAfterMaxAttempts(t *testing.T) {
	t.Parallel()

	repository := &fakeDispatcherRepo{
		deliverErr: errors.New("db write failed"),
		outbound: []repo.OutboundQueueItem{
			{
				ID:          22,
				TaskID:      32,
				Reason:      "事件前提醒（主动钩子）",
				DedupKey:    "event_reminder_x",
				Attempts:    3,
				MaxAttempts: 3,
				Payload: map[string]any{
					"task_type": "event_reminder",
				},
			},
		},
	}
	dispatcher := NewDispatcher(repository, DispatcherConfig{
		Enabled: true,
	})

	delivered, err := dispatcher.dispatchOutbound(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("dispatchOutbound returned error: %v", err)
	}
	if delivered != 0 {
		t.Fatalf("expected no delivered message on permanent failure, got %d", delivered)
	}
	if len(repository.permanentFailedQueueIDs) != 1 || repository.permanentFailedQueueIDs[0] != 22 {
		t.Fatalf("expected permanent failure marked, got %+v", repository.permanentFailedQueueIDs)
	}
}
