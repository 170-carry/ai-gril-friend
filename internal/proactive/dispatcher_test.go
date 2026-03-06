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
	topic                   repo.ConversationTopic
	topicsByKey             map[string]repo.ConversationTopic
	cancelledTaskIDs        []int64
	rescheduledTaskIDs      []int64
	rescheduledOutboundIDs  []int64
	permanentFailedQueueIDs []int64
	queuedTaskIDs           []int64
	enqueuedOutboundCount   int
	enqueuedOutboundItems   []repo.OutboundQueueItem
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

func (f *fakeDispatcherRepo) GetConversationTopic(ctx context.Context, userID, sessionID, topicKey string) (repo.ConversationTopic, error) {
	if len(f.topicsByKey) > 0 {
		if topic, ok := f.topicsByKey[topicKey]; ok {
			return topic, nil
		}
		return repo.ConversationTopic{}, nil
	}
	return f.topic, nil
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
	f.enqueuedOutboundItems = append(f.enqueuedOutboundItems, item)
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

func TestDispatcher_StageDueTasksCancelsResolvedTopicRecall(t *testing.T) {
	t.Parallel()

	repository := &fakeDispatcherRepo{
		state: repo.ProactiveState{
			Enabled: true,
		},
		topic: repo.ConversationTopic{
			TopicKey: "interview_prep",
			Status:   "resolved",
		},
		tasks: []repo.ProactiveTask{
			{
				ID:        41,
				UserID:    "u1",
				SessionID: "s1",
				TaskType:  "topic_reengage",
				Reason:    "未完话题适合稍后自然回钩",
				DedupKey:  "topic_reengage_x",
				RunAt:     time.Now(),
				Payload: map[string]any{
					"topic_key":   "interview_prep",
					"topic_label": "面试准备",
				},
			},
		},
	}
	dispatcher := NewDispatcher(repository, DispatcherConfig{Enabled: true})

	staged, err := dispatcher.stageDueTasks(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("stageDueTasks returned error: %v", err)
	}
	if staged != 0 {
		t.Fatalf("expected resolved topic recall not staged, got %d", staged)
	}
	if len(repository.cancelledTaskIDs) != 1 || repository.cancelledTaskIDs[0] != 41 {
		t.Fatalf("expected resolved topic task cancelled, got %+v", repository.cancelledTaskIDs)
	}
}

func TestDispatcher_StageDueTasksStripsStaleSecondaryTopic(t *testing.T) {
	t.Parallel()

	repository := &fakeDispatcherRepo{
		state: repo.ProactiveState{
			Enabled: true,
		},
		topicsByKey: map[string]repo.ConversationTopic{
			"work_conflict": {
				TopicKey: "work_conflict",
				Status:   "active",
			},
			"sleep_state": {
				TopicKey: "sleep_state",
				Status:   "resolved",
			},
		},
		tasks: []repo.ProactiveTask{
			{
				ID:        51,
				UserID:    "u1",
				SessionID: "s1",
				TaskType:  "topic_reengage",
				Reason:    "未完话题适合稍后自然回钩",
				DedupKey:  "topic_reengage_secondary_x",
				RunAt:     time.Now(),
				Payload: map[string]any{
					"topic_key":               "work_conflict",
					"topic_label":             "工作冲突",
					"secondary_topic_key":     "sleep_state",
					"secondary_topic_label":   "睡眠状态",
					"secondary_relation_type": "cause_effect",
					"secondary_callback_hint": "结果现在完全睡不着",
				},
			},
		},
	}
	dispatcher := NewDispatcher(repository, DispatcherConfig{Enabled: true})

	staged, err := dispatcher.stageDueTasks(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("stageDueTasks returned error: %v", err)
	}
	if staged != 1 {
		t.Fatalf("expected task staged, got %d", staged)
	}
	if len(repository.enqueuedOutboundItems) != 1 {
		t.Fatalf("expected one outbound item, got %d", len(repository.enqueuedOutboundItems))
	}
	payload := repository.enqueuedOutboundItems[0].Payload
	if _, ok := payload["secondary_topic_key"]; ok {
		t.Fatalf("expected stale secondary topic to be stripped, got %+v", payload)
	}
}
