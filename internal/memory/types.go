package memory

import (
	"context"
	"time"

	"ai-gf/internal/memory/ranking"
)

// Config 是记忆系统总配置。
type Config struct {
	EventWindowDays        int
	EventMinImportance     int
	PreferenceTopN         int
	ExtractorMinConfidence float64
	ExtractorMinImportance int
	Ranking                ranking.Config
}

// DefaultConfig 返回记忆系统默认参数（MVP 推荐值）。
func DefaultConfig() Config {
	return Config{
		EventWindowDays:        7,
		EventMinImportance:     3,
		PreferenceTopN:         5,
		ExtractorMinConfidence: 0.7,
		ExtractorMinImportance: 2,
		Ranking:                ranking.DefaultConfig(),
	}
}

// Normalize 对配置做边界归一化，防止异常输入。
func (c Config) Normalize() Config {
	def := DefaultConfig()
	if c.EventWindowDays <= 0 {
		c.EventWindowDays = def.EventWindowDays
	}
	if c.PreferenceTopN <= 0 {
		c.PreferenceTopN = def.PreferenceTopN
	}
	if c.EventMinImportance <= 0 {
		c.EventMinImportance = def.EventMinImportance
	}
	if c.EventMinImportance > 5 {
		c.EventMinImportance = 5
	}
	if c.ExtractorMinConfidence <= 0 || c.ExtractorMinConfidence > 1 {
		c.ExtractorMinConfidence = def.ExtractorMinConfidence
	}
	if c.ExtractorMinImportance <= 0 {
		c.ExtractorMinImportance = def.ExtractorMinImportance
	}
	c.Ranking = c.Ranking.Normalize()
	return c
}

// ContextRequest 是对话前构建记忆上下文的输入。
type ContextRequest struct {
	UserID      string
	SessionID   string
	UserMessage string
	Now         time.Time
	K           int
}

// ContextResult 是注入 PromptBuilder 的记忆上下文结果。
type ContextResult struct {
	UserProfile         string               `json:"user_profile"`
	UserPreferences     string               `json:"user_preferences"`
	UserBoundaries      string               `json:"user_boundaries"`
	ImportantEvents     string               `json:"important_events"`
	TopicContext        string               `json:"topic_context"`
	RelevantMemories    string               `json:"relevant_memories"`
	RelationshipSummary string               `json:"relationship_summary"`
	RelationshipState   RelationshipSnapshot `json:"relationship_state"`
	ActiveTopics        []TopicSnapshot      `json:"active_topics"`
	TopicGraph          []TopicEdgeSnapshot  `json:"topic_graph"`
	MemoryIDs           []int64              `json:"memory_ids"`
	RankTrace           []ranking.TraceItem  `json:"rank_trace"`
}

// RelationshipSnapshot 是给 ChatService / PromptBuilder 使用的关系状态快照。
type RelationshipSnapshot struct {
	Stage             string     `json:"stage"`
	Familiarity       float64    `json:"familiarity"`
	Intimacy          float64    `json:"intimacy"`
	Trust             float64    `json:"trust"`
	Flirt             float64    `json:"flirt"`
	BoundaryRisk      float64    `json:"boundary_risk"`
	SupportNeed       float64    `json:"support_need"`
	Playfulness       float64    `json:"playfulness"`
	InteractionHeat   float64    `json:"interaction_heat"`
	TotalTurns        int        `json:"total_turns"`
	LastInteractionAt *time.Time `json:"last_interaction_at,omitempty"`
	Summary           string     `json:"summary"`
}

// TopicSnapshot 是当前对话前可见的话题线程摘要。
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

// TopicEdgeSnapshot 是话题图的轻量边摘要。
type TopicEdgeSnapshot struct {
	FromTopicKey  string  `json:"from_topic_key"`
	ToTopicKey    string  `json:"to_topic_key"`
	RelationType  string  `json:"relation_type"`
	Weight        float64 `json:"weight"`
	EvidenceCount int     `json:"evidence_count"`
}

// TurnInput 是一轮对话结束后的抽取输入。
type TurnInput struct {
	UserID              string
	SessionID           string
	UserMessageID       int64
	AssistantMessageID  int64
	UserMessage         string
	AssistantMessage    string
	ConversationContext string
	// PlannedActions 是 Conversation Engine 产出的动作计划，会在异步 worker 中真正执行。
	PlannedActions []TurnAction
	Now            time.Time
}

// TurnAction 是 CE 计划到 memory 层的动作单元。
type TurnAction struct {
	Type   string
	Params map[string]string
	Reason string
}

// Engine 定义 ChatService 与记忆系统之间的交互契约。
type Engine interface {
	BuildContext(ctx context.Context, req ContextRequest) (ContextResult, error)
	ProcessTurn(ctx context.Context, in TurnInput) error
}

// Embedder 抽象 Embedding 能力，便于复用 llm.Provider。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}
