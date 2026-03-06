package conversation

import (
	"strings"
	"time"

	"ai-gf/internal/llm"
)

// Intent 表示本轮用户输入的主意图分类。
type Intent string

const (
	IntentEmotionalSupport Intent = "emotional_support"
	IntentAdviceSolving    Intent = "advice_problem_solving"
	IntentSmallTalk        Intent = "small_talk"
	IntentStorySharing     Intent = "story_sharing"
	IntentPlanningEvent    Intent = "planning_event"
	IntentRelationship     Intent = "relationship_intimacy"
	IntentBoundarySafety   Intent = "boundary_safety"
	IntentMetaProduct      Intent = "meta_product"
)

// State 表示对话状态机中的阶段。
type State string

const (
	StateOpen    State = "OPEN"
	StateExplore State = "EXPLORE"
	StateSupport State = "SUPPORT"
	StateSolve   State = "SOLVE"
	StateCommit  State = "COMMIT"
	StateClose   State = "CLOSE"
)

// ToneConfig 是风格控制器给出的可控语气参数。
type ToneConfig struct {
	Warmth      float64 `json:"warmth"`
	Playfulness float64 `json:"playfulness"`
	Directness  float64 `json:"directness"`
	Length      string  `json:"length"` // short / medium / long
	EmojiLevel  int     `json:"emoji_level"`
}

// Action 表示 CE 建议触发的动作（由外部 worker 执行）。
type Action struct {
	Type   string            `json:"type"`
	Params map[string]string `json:"params,omitempty"`
	Reason string            `json:"reason,omitempty"`
}

// MemorySnapshot 是 CE 可见的记忆快照（轻量摘要）。
type MemorySnapshot struct {
	UserProfile      string
	UserPreferences  string
	UserBoundaries   string
	ImportantEvents  string
	RelevantMemories string
}

// ConversationRequest 是 CE 的输入上下文。
type ConversationRequest struct {
	UserID            string
	SessionID         string
	UserMessage       string
	LastTurns         []llm.Message
	MemorySnapshot    MemorySnapshot
	Emotion           string
	EmotionIntensity  float64
	Now               time.Time
	RelationshipState string
}

// Normalize 对输入做兜底与归一化，保证同输入稳定输出。
func (r ConversationRequest) Normalize() ConversationRequest {
	r.UserID = strings.TrimSpace(r.UserID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		r.SessionID = "default"
	}
	r.UserMessage = strings.TrimSpace(r.UserMessage)
	r.Emotion = strings.ToLower(strings.TrimSpace(r.Emotion))
	if r.Emotion == "" {
		r.Emotion = "neutral"
	}
	if r.EmotionIntensity < 0 {
		r.EmotionIntensity = 0
	}
	if r.EmotionIntensity > 1 {
		r.EmotionIntensity = 1
	}
	if r.Now.IsZero() {
		r.Now = time.Now()
	}
	r.RelationshipState = strings.ToLower(strings.TrimSpace(r.RelationshipState))
	if r.RelationshipState == "" {
		r.RelationshipState = "close"
	}
	return r
}

// ConversationPlan 是 CE 输出给 PromptBuilder 的策略计划。
type ConversationPlan struct {
	Intent           Intent     `json:"intent"`
	IntentConfidence float64    `json:"intent_confidence"`
	Mode             string     `json:"mode"`
	CurrentState     State      `json:"current_state"`
	NextState        State      `json:"next_state"`
	Tone             ToneConfig `json:"tone"`

	// ResponseStructure 表示建议段落顺序（mirror/ask/add_value/close_softly）。
	ResponseStructure []string `json:"response_structure"`
	Questions         []string `json:"questions"`
	Actions           []Action `json:"actions"`
	StopRules         []string `json:"stop_rules"`
}
