package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	"ai-gf/internal/signals"
)

// TestBuildPlan_PreferenceAndQuestion 验证偏好输入会生成记忆动作且追问不超过 1 个。
func TestBuildPlan_PreferenceAndQuestion(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "记住我喜欢喝拿铁，最近有点焦虑",
		Now:         time.Date(2026, 3, 5, 20, 0, 0, 0, time.FixedZone("CST", 8*3600)),
	})

	if plan.Intent != IntentEmotionalSupport {
		t.Fatalf("expected emotional support intent, got %s", plan.Intent)
	}
	if len(plan.Questions) > 1 {
		t.Fatalf("expected <=1 question, got %d", len(plan.Questions))
	}
	foundPreference := false
	for _, action := range plan.Actions {
		if action.Type == "SAVE_PREFERENCE" {
			foundPreference = true
			break
		}
	}
	if !foundPreference {
		t.Fatalf("expected SAVE_PREFERENCE action")
	}
}

// TestBuildPlan_StopAskingWhenUserRefuses 验证用户拒绝时会停止追问。
func TestBuildPlan_StopAskingWhenUserRefuses(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我有点累，先这样，别问了",
	})

	if len(plan.Questions) != 0 {
		t.Fatalf("expected no questions when user refuses, got %v", plan.Questions)
	}
	stopText := strings.Join(plan.StopRules, " | ")
	if !strings.Contains(stopText, "停止追问") {
		t.Fatalf("expected stop rules contain refusal handling, got %s", stopText)
	}
}

// TestBuildPlan_EventActions 验证事件语句会触发事件写入与提醒动作。
func TestBuildPlan_EventActions(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我明天下午三点有面试，提醒我别迟到",
	})

	hasSaveEvent := false
	hasReminder := false
	for _, action := range plan.Actions {
		if action.Type == "SAVE_EVENT" {
			hasSaveEvent = true
		}
		if action.Type == "SCHEDULE_EVENT_REMINDER" {
			hasReminder = true
		}
	}
	if !hasSaveEvent || !hasReminder {
		t.Fatalf("expected SAVE_EVENT and SCHEDULE_EVENT_REMINDER, actions=%+v", plan.Actions)
	}
}

// TestBuildPlan_NoFalseEventOnYouDian 验证“有点焦虑”不会误判成时间事件。
func TestBuildPlan_NoFalseEventOnYouDian(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我今天有点焦虑，想找个人陪我聊聊",
	})

	for _, action := range plan.Actions {
		if action.Type == "SAVE_EVENT" || action.Type == "SCHEDULE_EVENT_REMINDER" {
			t.Fatalf("did not expect event actions for non-event sentence, actions=%+v", plan.Actions)
		}
	}
}

// TestBuildPlan_SafetyGate 验证高风险词命中时进入 safety_support。
func TestBuildPlan_SafetyGate(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我有点想轻生",
	})

	if plan.Mode != "safety_support" {
		t.Fatalf("expected safety_support mode, got %s", plan.Mode)
	}
	if len(plan.Questions) != 0 {
		t.Fatalf("expected no questions in safety mode, got %v", plan.Questions)
	}
}

func TestBuildPlan_NegatedEmotionDoesNotRouteToSupport(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我已经不焦虑了，今天轻松多了",
	})

	if plan.Intent == IntentEmotionalSupport {
		t.Fatalf("expected negated emotion to avoid emotional support routing, got %s", plan.Intent)
	}
}

func TestBuildPlan_EmbeddingFallbackRoutesEmotionalSupport(t *testing.T) {
	t.Parallel()

	engine := NewEngine(signalsStubEmbedder{
		vectors: map[string][]float32{
			"心里像被抽空了一样": {1, 0},
			"心里空落落的":    {1, 0},
		},
	})
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "心里像被抽空了一样",
	})

	if plan.Intent != IntentEmotionalSupport {
		t.Fatalf("expected embedding fallback to route emotional support, got %s", plan.Intent)
	}
}

func TestBuildPlan_LLMFallbackRoutesEmotionalSupport(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	engine.UseSignalAnalyzer(stubSignalAnalyzer{
		scores: map[string]float64{
			signals.SignalVulnerability: 0.82,
		},
	})
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "心里像被掏空了一块",
	})

	if plan.Intent != IntentEmotionalSupport {
		t.Fatalf("expected llm fallback to route emotional support, got %s", plan.Intent)
	}
}

func TestBuildPlan_GentleRecallForGenericReconnect(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	lastDiscussed := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	nextRecall := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "在吗",
		Now:         time.Date(2026, 3, 6, 11, 0, 0, 0, time.UTC),
		MemorySnapshot: MemorySnapshot{
			ActiveTopics: []TopicSnapshot{
				{
					TopicKey:        "interview_prep",
					Label:           "面试准备",
					Summary:         "昨天你说最担心系统设计部分，还没决定先练哪块。",
					Status:          "active",
					Importance:      5,
					LastDiscussedAt: &lastDiscussed,
					NextRecallAt:    &nextRecall,
				},
			},
		},
	})

	if plan.Topic.Mode != "gentle_recall" {
		t.Fatalf("expected gentle_recall topic mode, got %+v", plan.Topic)
	}
	if len(plan.Questions) == 0 || !strings.Contains(plan.Questions[0], "面试准备") {
		t.Fatalf("expected topic recall question, got %v", plan.Questions)
	}
	if action := findAction(plan.Actions, "SCHEDULE_TOPIC_REENGAGE"); action == nil {
		t.Fatalf("expected topic reengage action, got %+v", plan.Actions)
	}
}

func TestBuildPlan_ResolvedTopicDoesNotScheduleRecall(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	lastDiscussed := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "面试那件事已经搞定了，谢谢你",
		MemorySnapshot: MemorySnapshot{
			ActiveTopics: []TopicSnapshot{
				{
					TopicKey:        "interview_prep",
					Label:           "面试准备",
					Summary:         "昨天你说最担心系统设计部分，还没决定先练哪块。",
					Status:          "active",
					Importance:      5,
					LastDiscussedAt: &lastDiscussed,
				},
			},
		},
	})

	track := findAction(plan.Actions, "TRACK_TOPIC")
	if track == nil {
		t.Fatalf("expected track topic action, got %+v", plan.Actions)
	}
	if got := track.Params["status"]; got != "resolved" {
		t.Fatalf("expected resolved topic status, got %q", got)
	}
	if action := findAction(plan.Actions, "SCHEDULE_TOPIC_REENGAGE"); action != nil {
		t.Fatalf("did not expect reengage action for resolved topic, got %+v", action)
	}
}

func TestBuildPlan_LongNarrativeCreatesParallelTopics(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "今天开会被老板怼了，回家后又和对象吵了一架，结果现在完全睡不着。",
	})

	if plan.Topic.Mode != "open_new" {
		t.Fatalf("expected open_new topic mode, got %+v", plan.Topic)
	}
	if plan.Topic.TopicLabel != "工作冲突" {
		t.Fatalf("expected primary topic to focus work conflict, got %+v", plan.Topic)
	}
	if len(plan.Topic.Related) == 0 {
		t.Fatalf("expected parallel related topics for long narrative, got %+v", plan.Topic)
	}
	if action := findAction(plan.Actions, "LINK_TOPICS"); action == nil {
		t.Fatalf("expected LINK_TOPICS action for parallel topics, got %+v", plan.Actions)
	}
}

func TestBuildPlan_TopicSummarizerRefinesLongNarrative(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	engine.UseTopicSummarizer(stubTopicSummarizer{
		result: TopicSummaryResult{
			Threads: []TopicSummaryThread{
				{
					Label:         "职场挫败",
					Summary:       "开会被阴阳怪气后整个人一直绷着。",
					CallbackHint:  "会上被一通阴阳",
					AliasTerms:    []string{"阴阳怪气", "开会受气"},
					SourceClauses: []string{"白天在会上被人一通阴阳怪气"},
					Importance:    5,
				},
				{
					Label:         "职业选择",
					Summary:       "凌晨还在反复想要不要辞职。",
					SourceClauses: []string{"凌晨还在想要不要辞职"},
					Importance:    4,
				},
			},
		},
	})

	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "白天在会上被人一通阴阳怪气，回家还得装没事，凌晨脑子停不下来，一直在想要不要辞职。",
	})

	if plan.Topic.TopicLabel != "职场挫败" {
		t.Fatalf("expected LLM summarizer label to win for long narrative, got %+v", plan.Topic)
	}
	if len(plan.Topic.Related) == 0 || plan.Topic.Related[0].TopicLabel != "职业选择" {
		t.Fatalf("expected summarizer to keep second thread, got %+v", plan.Topic.Related)
	}
}

func TestBuildPlan_LinkTopicsUsesCauseEffect(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "今天开会被老板怼了，结果现在完全睡不着。",
	})

	action := findAction(plan.Actions, "LINK_TOPICS")
	if action == nil {
		t.Fatalf("expected LINK_TOPICS action, got %+v", plan.Actions)
	}
	if got := action.Params["relation_type"]; got != "cause_effect" {
		t.Fatalf("expected cause_effect relation, got %+v", action)
	}
	schedule := findAction(plan.Actions, "SCHEDULE_TOPIC_REENGAGE")
	if schedule == nil {
		t.Fatalf("expected SCHEDULE_TOPIC_REENGAGE action, got %+v", plan.Actions)
	}
	if got := schedule.Params["secondary_topic_key"]; got != "睡眠状态" {
		t.Fatalf("expected secondary topic to be scheduled for strong relation, got %+v", schedule.Params)
	}
	if got := schedule.Params["secondary_relation_type"]; got != "cause_effect" {
		t.Fatalf("expected secondary relation type cause_effect, got %+v", schedule.Params)
	}
}

func TestBuildPlan_CallbackClusterMatchesVariantJoke(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	lastDiscussed := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "今天这个破班也太离谱了吧",
		Now:         time.Date(2026, 3, 6, 11, 0, 0, 0, time.UTC),
		MemorySnapshot: MemorySnapshot{
			ActiveTopics: []TopicSnapshot{
				{
					TopicKey:        "work_conflict",
					Label:           "工作冲突",
					Summary:         "昨天你吐槽开会像打仗。",
					CallbackHint:    "这个破班像闯关",
					ClusterKey:      "工作冲突_这个破班像闯关",
					AliasTerms:      []string{"这个破班", "离谱开会"},
					Status:          "active",
					Importance:      4,
					LastDiscussedAt: &lastDiscussed,
				},
			},
		},
	})

	if plan.Topic.Mode != "continue_existing" {
		t.Fatalf("expected clustered callback to continue existing topic, got %+v", plan.Topic)
	}
}

func TestBuildPlan_TopicGraphBringsRelatedThread(t *testing.T) {
	t.Parallel()

	engine := NewEngine()
	lastDiscussed := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	plan := engine.BuildPlan(context.Background(), ConversationRequest{
		UserID:      "u1",
		SessionID:   "s1",
		UserMessage: "我今天又有点睡不着",
		Now:         time.Date(2026, 3, 6, 11, 0, 0, 0, time.UTC),
		MemorySnapshot: MemorySnapshot{
			ActiveTopics: []TopicSnapshot{
				{
					TopicKey:         "sleep_state",
					Label:            "睡眠状态",
					Summary:          "你最近总是因为脑子停不下来而睡不着。",
					Status:           "active",
					Importance:       4,
					RelatedTopicKeys: []string{"work_pressure"},
					LastDiscussedAt:  &lastDiscussed,
				},
				{
					TopicKey:         "work_pressure",
					Label:            "工作压力",
					Summary:          "老板最近压得很紧，你一想到明天开会就焦虑。",
					Status:           "active",
					Importance:       4,
					RelatedTopicKeys: []string{"sleep_state"},
					LastDiscussedAt:  &lastDiscussed,
				},
			},
			TopicGraph: []TopicEdgeSnapshot{
				{
					FromTopicKey: "work_pressure",
					ToTopicKey:   "sleep_state",
					RelationType: "cause_effect",
					Weight:       1.4,
				},
			},
		},
	})

	if plan.Topic.TopicLabel != "睡眠状态" {
		t.Fatalf("expected sleep topic as primary, got %+v", plan.Topic)
	}
	if len(plan.Topic.Related) == 0 || plan.Topic.Related[0].TopicLabel != "工作压力" {
		t.Fatalf("expected topic graph to surface related work thread, got %+v", plan.Topic.Related)
	}
	if got := plan.Topic.Related[0].RelationType; got != "cause_effect" {
		t.Fatalf("expected cause_effect relation from graph, got %+v", plan.Topic.Related[0])
	}
}

// TestRenderPlanForPrompt 验证计划渲染文本包含关键字段。
func TestRenderPlanForPrompt(t *testing.T) {
	t.Parallel()

	text := RenderPlanForPrompt(ConversationPlan{
		Intent:           IntentAdviceSolving,
		IntentConfidence: 0.88,
		Mode:             "solve_then_commit",
		CurrentState:     StateSolve,
		NextState:        StateCommit,
		Tone: ToneConfig{
			Warmth:      0.7,
			Playfulness: 0.2,
			Directness:  0.8,
			Length:      "short",
			EmojiLevel:  0,
		},
		ResponseStructure: []string{"mirror", "add_value", "ask", "close_softly"},
		Questions:         []string{"你更想先做哪一步？"},
		StopRules:         []string{"每轮最多提 1 个问题。"},
		Actions: []Action{
			{Type: "SAVE_SEMANTIC_MEMORY", Params: map[string]string{"sentence": "用户提到面试"}},
		},
	})

	if !strings.Contains(text, "intent: advice_problem_solving") {
		t.Fatalf("unexpected render text: %s", text)
	}
	if !strings.Contains(text, "structure: mirror -> add_value -> ask -> close_softly") {
		t.Fatalf("unexpected structure render: %s", text)
	}
}

func findAction(actions []Action, actionType string) *Action {
	for i := range actions {
		if actions[i].Type == actionType {
			return &actions[i]
		}
	}
	return nil
}

type signalsStubEmbedder struct {
	vectors map[string][]float32
}

func (s signalsStubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if vec, ok := s.vectors[text]; ok {
		return vec, nil
	}
	return []float32{0, 1}, nil
}

type stubSignalAnalyzer struct {
	scores map[string]float64
}

func (s stubSignalAnalyzer) Analyze(ctx context.Context, text string, catalog signals.Catalog) (map[string]float64, error) {
	out := make(map[string]float64, len(catalog.Signals))
	for key := range catalog.Signals {
		out[key] = s.scores[key]
	}
	return out, nil
}

type stubTopicSummarizer struct {
	result TopicSummaryResult
	err    error
}

func (s stubTopicSummarizer) Summarize(ctx context.Context, req TopicSummaryRequest) (TopicSummaryResult, error) {
	return s.result, s.err
}
