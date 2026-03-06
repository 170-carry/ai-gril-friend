package prompt

import (
	"context"
	"strings"
	"time"

	"ai-gf/internal/llm"
)

// MessageKind 标记消息在最终会话中的角色类型。
type MessageKind string

const (
	MessageKindSystem    MessageKind = "system"
	MessageKindDeveloper MessageKind = "developer"
	MessageKindUser      MessageKind = "user"
	MessageKindAssistant MessageKind = "assistant"
)

// BudgetBucket 表示 token 预算的分桶分类。
type BudgetBucket string

const (
	BucketHard    BudgetBucket = "hard"
	BucketProfile BudgetBucket = "profile"
	BucketRAG     BudgetBucket = "rag"
	BucketEmotion BudgetBucket = "emotion"
	BucketSummary BudgetBucket = "summary"
	BucketSTM     BudgetBucket = "stm"
	BucketUser    BudgetBucket = "user"
)

// BudgetConfig 定义 prompt 预算总量、预留量与各桶比例。
type BudgetConfig struct {
	MaxPromptTokens int
	ReserveTokens   int
	SystemRatio     int
	MemoryRatio     int
	HistoryRatio    int
}

// DefaultBudgetConfig 返回系统默认预算配置。
func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		MaxPromptTokens: 3200,
		ReserveTokens:   800,
		SystemRatio:     35,
		MemoryRatio:     25,
		HistoryRatio:    40,
	}
}

// Normalize 对预算配置做兜底与边界修正。
func (b BudgetConfig) Normalize() BudgetConfig {
	def := DefaultBudgetConfig()
	if b.MaxPromptTokens <= 0 {
		b.MaxPromptTokens = def.MaxPromptTokens
	}
	if b.ReserveTokens <= 0 {
		b.ReserveTokens = def.ReserveTokens
	}
	if b.ReserveTokens >= b.MaxPromptTokens {
		b.ReserveTokens = b.MaxPromptTokens / 4
	}
	if b.SystemRatio <= 0 {
		b.SystemRatio = def.SystemRatio
	}
	if b.MemoryRatio <= 0 {
		b.MemoryRatio = def.MemoryRatio
	}
	if b.HistoryRatio <= 0 {
		b.HistoryRatio = def.HistoryRatio
	}
	return b
}

// BuildOptions 控制可选模块（RAG/情绪/事件）是否开启。
type BuildOptions struct {
	EnableRAG     bool
	EnableEmotion bool
	EnableEvents  bool
}

// DefaultBuildOptions 返回默认全开策略。
func DefaultBuildOptions() BuildOptions {
	return BuildOptions{
		EnableRAG:     true,
		EnableEmotion: true,
		EnableEvents:  true,
	}
}

// Normalize 处理调用方未显式设置选项时的默认行为。
func (o BuildOptions) Normalize() BuildOptions {
	// bool 的零值无法区分“未设置”与“显式关闭”，因此全部为 false 时按默认全开处理。
	if !o.EnableRAG && !o.EnableEmotion && !o.EnableEvents {
		return DefaultBuildOptions()
	}
	return o
}

// BuildRequest 是 Prompt Builder 的输入上下文。
type BuildRequest struct {
	UserID      string
	SessionID   string
	UserMessage string
	// ConversationPlan 是 CE 输出的结构化策略文本，供系统层约束回复行为。
	ConversationPlan string
	Now              time.Time
	ModelID          string
	Options          BuildOptions
	Budget           BudgetConfig
	Persona          PersonaConfig
	History          []llm.Message
}

// Normalize 规整请求字段并补全默认值。
func (r BuildRequest) Normalize() BuildRequest {
	r.UserID = strings.TrimSpace(r.UserID)
	r.SessionID = strings.TrimSpace(r.SessionID)
	if r.SessionID == "" {
		r.SessionID = "default"
	}
	r.UserMessage = strings.TrimSpace(r.UserMessage)
	r.ConversationPlan = strings.TrimSpace(r.ConversationPlan)
	if r.Now.IsZero() {
		r.Now = time.Now()
	}
	r.Options = r.Options.Normalize()
	r.Budget = r.Budget.Normalize()
	r.Persona = normalizePersona(r.Persona)
	return r
}

// PromptBlock 是预算与裁剪阶段的最小处理单元。
type PromptBlock struct {
	ID         string
	Priority   int
	Kind       MessageKind
	Bucket     BudgetBucket
	Content    string
	TokensEst  int
	Hard       bool
	Redactable bool
	Metadata   map[string]string
}

// MemoryHit 记录命中的记忆条目及相似度信息。
type MemoryHit struct {
	ID         string  `json:"id"`
	Similarity float64 `json:"similarity"`
	Snippet    string  `json:"snippet,omitempty"`
	Source     string  `json:"source,omitempty"`
}

// RAGStats 记录 RAG 条目请求量与最终保留量。
type RAGStats struct {
	RequestedK int `json:"requested_k"`
	KeptK      int `json:"kept_k"`
	DroppedK   int `json:"dropped_k"`
}

// BlockTrace 记录单个 block 在某阶段的决策结果。
type BlockTrace struct {
	Stage    string       `json:"stage"`
	ID       string       `json:"id"`
	Bucket   BudgetBucket `json:"bucket"`
	Priority int          `json:"priority"`
	Tokens   int          `json:"tokens"`
	Kept     bool         `json:"kept"`
	Reason   string       `json:"reason"`
}

// StageBlock 是阶段报告里的块级快照。
type StageBlock struct {
	Order      int          `json:"order"`
	ID         string       `json:"id"`
	Priority   int          `json:"priority"`
	Kind       MessageKind  `json:"kind"`
	Bucket     BudgetBucket `json:"bucket"`
	TokensEst  int          `json:"tokens_est"`
	Hard       bool         `json:"hard"`
	Redactable bool         `json:"redactable"`
}

// StageReport 记录一个构建阶段的总体快照与备注。
type StageReport struct {
	Stage       string               `json:"stage"`
	Description string               `json:"description"`
	Blocks      []StageBlock         `json:"blocks"`
	Budget      map[BudgetBucket]int `json:"budget,omitempty"`
	Used        map[BudgetBucket]int `json:"used,omitempty"`
	Notes       []string             `json:"notes,omitempty"`
}

// BuildTrace 汇总一次 prompt 构建的完整可观测信息。
type BuildTrace struct {
	ModuleOrder    []string             `json:"module_order"`
	StageReports   []StageReport        `json:"stage_reports"`
	BlockDecisions []BlockTrace         `json:"block_decisions"`
	BucketBudget   map[BudgetBucket]int `json:"bucket_budget"`
	BucketUsed     map[BudgetBucket]int `json:"bucket_used"`
	TrimLogs       []string             `json:"trim_logs"`
	MemoryHits     []MemoryHit          `json:"memory_hits"`
	RAGStats       RAGStats             `json:"rag_stats"`
	TotalTokens    int                  `json:"total_tokens"`
}

// mark 兼容旧调用，默认记到 unknown 阶段。
func (t *BuildTrace) mark(block PromptBlock, kept bool, reason string) {
	t.markStage("unknown", block, kept, reason)
}

// markStage 记录某个 block 在指定阶段的决策。
func (t *BuildTrace) markStage(stage string, block PromptBlock, kept bool, reason string) {
	t.BlockDecisions = append(t.BlockDecisions, BlockTrace{
		Stage:    stage,
		ID:       block.ID,
		Bucket:   block.Bucket,
		Priority: block.Priority,
		Tokens:   block.TokensEst,
		Kept:     kept,
		Reason:   reason,
	})
}

// addStage 将当前 block 列表快照写入阶段报告。
func (t *BuildTrace) addStage(stage, description string, blocks []PromptBlock, notes ...string) {
	stageBlocks := make([]StageBlock, 0, len(blocks))
	for i, block := range blocks {
		stageBlocks = append(stageBlocks, StageBlock{
			Order:      i + 1,
			ID:         block.ID,
			Priority:   block.Priority,
			Kind:       block.Kind,
			Bucket:     block.Bucket,
			TokensEst:  block.TokensEst,
			Hard:       block.Hard,
			Redactable: block.Redactable,
		})
	}
	t.StageReports = append(t.StageReports, StageReport{
		Stage:       stage,
		Description: description,
		Blocks:      stageBlocks,
		Notes:       notes,
	})
}

// BuildResult 是 Prompt Builder 最终输出：messages + trace。
type BuildResult struct {
	Messages []llm.Message
	Trace    BuildTrace
}

// Module 定义单个 prompt 模块的构建契约。
type Module interface {
	ID() string
	Priority() int
	Required() bool
	Build(ctx context.Context, req BuildRequest) ([]PromptBlock, error)
	Degrade(ctx context.Context, req BuildRequest, err error) []PromptBlock
}

// PolicyEngine 负责模块冲突处理与开关过滤。
type PolicyEngine interface {
	Apply(ctx context.Context, req BuildRequest, blocks []PromptBlock, trace *BuildTrace) []PromptBlock
}

// TokenBudgeter 负责 token 预算拟合与降级裁剪。
type TokenBudgeter interface {
	Fit(ctx context.Context, req BuildRequest, blocks []PromptBlock, trace *BuildTrace) []PromptBlock
}

// Assembler 将 block 重组为最终给模型的 message 数组。
type Assembler interface {
	Assemble(ctx context.Context, req BuildRequest, blocks []PromptBlock, trace *BuildTrace) []llm.Message
}

// MessageBuilder 是对外暴露的 prompt 构建统一接口。
type MessageBuilder interface {
	Build(ctx context.Context, req BuildRequest) (BuildResult, error)
}
