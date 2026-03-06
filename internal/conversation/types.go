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

// RelationshipSnapshot 是 CE 可见的关系连续状态。
type RelationshipSnapshot struct {
	Stage           string  `json:"stage"`
	Familiarity     float64 `json:"familiarity"`
	Intimacy        float64 `json:"intimacy"`
	Trust           float64 `json:"trust"`
	Flirt           float64 `json:"flirt"`
	BoundaryRisk    float64 `json:"boundary_risk"`
	SupportNeed     float64 `json:"support_need"`
	Playfulness     float64 `json:"playfulness"`
	InteractionHeat float64 `json:"interaction_heat"`
	Summary         string  `json:"summary,omitempty"`
}

// TopicSnapshot 是 CE 可见的话题线程摘要。
type TopicSnapshot struct {
	TopicKey         string     `json:"topic_key"`
	Label            string     `json:"label"`
	Summary          string     `json:"summary"`
	CallbackHint     string     `json:"callback_hint,omitempty"`
	ClusterKey       string     `json:"cluster_key,omitempty"`
	AliasTerms       []string   `json:"alias_terms,omitempty"`
	RelatedTopicKeys []string   `json:"related_topic_keys,omitempty"`
	Status           string     `json:"status"`
	Importance       int        `json:"importance"`
	MentionCount     int        `json:"mention_count"`
	RecallCount      int        `json:"recall_count"`
	LastDiscussedAt  *time.Time `json:"last_discussed_at,omitempty"`
	NextRecallAt     *time.Time `json:"next_recall_at,omitempty"`
	LastRecalledAt   *time.Time `json:"last_recalled_at,omitempty"`
}

// TopicEdgeSnapshot 是 CE 可见的话题图轻量边。
type TopicEdgeSnapshot struct {
	FromTopicKey  string  `json:"from_topic_key"`
	ToTopicKey    string  `json:"to_topic_key"`
	RelationType  string  `json:"relation_type"`
	Weight        float64 `json:"weight"`
	EvidenceCount int     `json:"evidence_count"`
}

// TopicReference 是主话题之外需要一起感知的并行线程。
type TopicReference struct {
	TopicKey      string   `json:"topic_key,omitempty"`
	TopicLabel    string   `json:"topic_label,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	CallbackHint  string   `json:"callback_hint,omitempty"`
	ClusterKey    string   `json:"cluster_key,omitempty"`
	RelationType  string   `json:"relation_type,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	AliasTerms    []string `json:"alias_terms,omitempty"`
	SourceClauses []string `json:"source_clauses,omitempty"`
}

// TopicLink 表示本轮新观察到的话题边。
type TopicLink struct {
	FromTopicKey string  `json:"from_topic_key,omitempty"`
	ToTopicKey   string  `json:"to_topic_key,omitempty"`
	RelationType string  `json:"relation_type,omitempty"`
	Weight       float64 `json:"weight,omitempty"`
}

// TopicStrategy 描述本轮是否要延续/回钩某条旧话题。
type TopicStrategy struct {
	Mode         string           `json:"mode"`
	TopicKey     string           `json:"topic_key,omitempty"`
	TopicLabel   string           `json:"topic_label,omitempty"`
	Summary      string           `json:"summary,omitempty"`
	CallbackHint string           `json:"callback_hint,omitempty"`
	ClusterKey   string           `json:"cluster_key,omitempty"`
	Reason       string           `json:"reason,omitempty"`
	Related      []TopicReference `json:"related,omitempty"`
	Links        []TopicLink      `json:"links,omitempty"`
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
	TopicContext     string
	RelevantMemories string
	ActiveTopics     []TopicSnapshot
	TopicGraph       []TopicEdgeSnapshot
}

// ConversationRequest 是 CE 的输入上下文。
type ConversationRequest struct {
	UserID               string
	SessionID            string
	UserMessage          string
	LastTurns            []llm.Message
	MemorySnapshot       MemorySnapshot
	Emotion              string
	EmotionIntensity     float64
	Now                  time.Time
	RelationshipState    string
	RelationshipSnapshot RelationshipSnapshot
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
		r.RelationshipState = "familiar"
	}
	r.RelationshipSnapshot.Stage = normalizeRelationshipStage(r.RelationshipSnapshot.Stage)
	if r.RelationshipSnapshot.Stage == "" {
		r.RelationshipSnapshot.Stage = normalizeRelationshipStage(r.RelationshipState)
	}
	r.RelationshipSnapshot.Familiarity = clampMetric(r.RelationshipSnapshot.Familiarity, 0.36)
	r.RelationshipSnapshot.Intimacy = clampMetric(r.RelationshipSnapshot.Intimacy, 0.58)
	r.RelationshipSnapshot.Trust = clampMetric(r.RelationshipSnapshot.Trust, 0.62)
	r.RelationshipSnapshot.Flirt = clampMetric(r.RelationshipSnapshot.Flirt, 0.08)
	r.RelationshipSnapshot.BoundaryRisk = clampMetric(r.RelationshipSnapshot.BoundaryRisk, 0.08)
	r.RelationshipSnapshot.SupportNeed = clampMetric(r.RelationshipSnapshot.SupportNeed, 0.30)
	r.RelationshipSnapshot.Playfulness = clampMetric(r.RelationshipSnapshot.Playfulness, 0.32)
	r.RelationshipSnapshot.InteractionHeat = clampMetric(r.RelationshipSnapshot.InteractionHeat, 0.28)
	for i := range r.MemorySnapshot.ActiveTopics {
		r.MemorySnapshot.ActiveTopics[i].TopicKey = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].TopicKey)
		r.MemorySnapshot.ActiveTopics[i].Label = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].Label)
		r.MemorySnapshot.ActiveTopics[i].Summary = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].Summary)
		r.MemorySnapshot.ActiveTopics[i].CallbackHint = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].CallbackHint)
		r.MemorySnapshot.ActiveTopics[i].ClusterKey = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].ClusterKey)
		for j := range r.MemorySnapshot.ActiveTopics[i].AliasTerms {
			r.MemorySnapshot.ActiveTopics[i].AliasTerms[j] = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].AliasTerms[j])
		}
		for j := range r.MemorySnapshot.ActiveTopics[i].RelatedTopicKeys {
			r.MemorySnapshot.ActiveTopics[i].RelatedTopicKeys[j] = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].RelatedTopicKeys[j])
		}
		r.MemorySnapshot.ActiveTopics[i].Status = strings.TrimSpace(r.MemorySnapshot.ActiveTopics[i].Status)
		if r.MemorySnapshot.ActiveTopics[i].Importance <= 0 {
			r.MemorySnapshot.ActiveTopics[i].Importance = 3
		}
	}
	for i := range r.MemorySnapshot.TopicGraph {
		r.MemorySnapshot.TopicGraph[i].FromTopicKey = strings.TrimSpace(r.MemorySnapshot.TopicGraph[i].FromTopicKey)
		r.MemorySnapshot.TopicGraph[i].ToTopicKey = strings.TrimSpace(r.MemorySnapshot.TopicGraph[i].ToTopicKey)
		r.MemorySnapshot.TopicGraph[i].RelationType = strings.TrimSpace(r.MemorySnapshot.TopicGraph[i].RelationType)
		if r.MemorySnapshot.TopicGraph[i].Weight < 0 {
			r.MemorySnapshot.TopicGraph[i].Weight = 0
		}
		if r.MemorySnapshot.TopicGraph[i].EvidenceCount < 0 {
			r.MemorySnapshot.TopicGraph[i].EvidenceCount = 0
		}
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
	ResponseStructure []string             `json:"response_structure"`
	Questions         []string             `json:"questions"`
	Actions           []Action             `json:"actions"`
	StopRules         []string             `json:"stop_rules"`
	Relationship      RelationshipSnapshot `json:"relationship"`
	Topic             TopicStrategy        `json:"topic"`
}

// clampMetric 对关系分数做 0~1 范围收敛，并在缺省时使用温和默认值。
func clampMetric(v float64, fallback float64) float64 {
	if v == 0 {
		v = fallback
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func normalizeRelationshipStage(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "", "companion", "stage_a":
		return "companion"
	case "stranger":
		return "companion"
	case "familiar", "friend", "stage_b":
		return "familiar"
	case "trust_building", "trusting", "close", "stage_c":
		return "trust_building"
	case "light_flirt", "flirt", "stage_d":
		return "light_flirt"
	case "romantic", "stage_e":
		return "romantic"
	default:
		return strings.ToLower(strings.TrimSpace(stage))
	}
}
