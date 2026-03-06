package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PromptBuilder 负责协调模块、策略、预算和组装器，产出最终 prompt。
type PromptBuilder struct {
	modules []Module
	policy  PolicyEngine
	budget  TokenBudgeter
	asm     Assembler
}

// NewBuilder 返回默认实现，按固定顺序注册模块。
func NewBuilder() *PromptBuilder {
	return &PromptBuilder{
		modules: []Module{
			newSafetyModule(),
			newPersonaModule(),
			newRelationshipModule(),
			newBoundariesModule(),
			newProfileModule(),
			newPreferencesModule(),
			newEventsModule(),
			newTopicsModule(),
			newRAGModule(),
			newEmotionModule(),
			newConversationPlanModule(),
			newSTMModule(),
			newUserMessageModule(),
		},
		policy: newDefaultPolicyEngine(),
		budget: newDefaultTokenBudgeter(),
		asm:    newDefaultAssembler(),
	}
}

// Build 是 prompt 构建主流程，保证相同输入稳定输出。
func (b *PromptBuilder) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	req = req.Normalize()
	if req.UserMessage == "" {
		return BuildResult{}, fmt.Errorf("user message is required")
	}

	trace := BuildTrace{
		ModuleOrder:    make([]string, 0, len(b.modules)),
		StageReports:   []StageReport{},
		BucketBudget:   map[BudgetBucket]int{},
		BucketUsed:     map[BudgetBucket]int{},
		TrimLogs:       []string{},
		BlockDecisions: []BlockTrace{},
		MemoryHits:     []MemoryHit{},
	}

	blocks := make([]PromptBlock, 0, 32)
	for _, m := range b.modules {
		trace.ModuleOrder = append(trace.ModuleOrder, m.ID())

		out, err := m.Build(ctx, req)
		if err != nil {
			if m.Required() {
				return BuildResult{}, fmt.Errorf("prompt module %s failed: %w", m.ID(), err)
			}
			// 可选模块失败时降级，不中断主流程。
			trace.TrimLogs = append(trace.TrimLogs, fmt.Sprintf("module %s degraded: %v", m.ID(), err))
			out = m.Degrade(ctx, req, err)
		}
		blocks = append(blocks, out...)
	}

	// 三阶段：模块产出 -> 策略过滤 -> 预算拟合。
	blocks = sanitizeBlocks(blocks)
	sortBlocksStable(blocks)
	collectMetadataHints(&trace, blocks)
	trace.addStage("module_build", "raw blocks from modules (sorted by priority)", blocks)

	blocks = b.policy.Apply(ctx, req, blocks, &trace)
	trace.addStage("policy_applied", "after conflict resolution and feature switches", blocks)

	blocks = b.budget.Fit(ctx, req, blocks, &trace)
	trace.addStage("budget_fit", "after token bucket fitting and trimming", blocks)
	if len(trace.StageReports) > 0 {
		last := len(trace.StageReports) - 1
		trace.StageReports[last].Budget = cloneBudgetMap(trace.BucketBudget)
		trace.StageReports[last].Used = cloneBudgetMap(trace.BucketUsed)
		trace.StageReports[last].Notes = append(trace.StageReports[last].Notes, trace.TrimLogs...)
	}
	messages := b.asm.Assemble(ctx, req, blocks, &trace)
	trace.TotalTokens = estimateMessagesTokens(messages)

	return BuildResult{
		Messages: messages,
		Trace:    trace,
	}, nil
}

// sanitizeBlocks 清洗空内容、兜底 ID、补全 token 估算与 metadata。
func sanitizeBlocks(blocks []PromptBlock) []PromptBlock {
	out := make([]PromptBlock, 0, len(blocks))
	for _, block := range blocks {
		block.Content = strings.TrimSpace(block.Content)
		if block.Content == "" {
			continue
		}
		if block.ID == "" {
			block.ID = "unknown"
		}
		if block.TokensEst <= 0 {
			block.TokensEst = estimateTextTokens(block.Content)
		}
		if block.Metadata == nil {
			block.Metadata = map[string]string{}
		}
		out = append(out, block)
	}
	return out
}

// sortBlocksStable 按 priority + seq + id 做稳定排序，保证构建确定性。
func sortBlocksStable(blocks []PromptBlock) {
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].Priority == blocks[j].Priority {
			iSeq := blocks[i].Metadata["seq"]
			jSeq := blocks[j].Metadata["seq"]
			if iSeq != jSeq {
				return iSeq < jSeq
			}
			return blocks[i].ID < blocks[j].ID
		}
		return blocks[i].Priority < blocks[j].Priority
	})
}

// collectMetadataHints 从 RAG block 元数据提取 memory hits 与 requested K。
func collectMetadataHints(trace *BuildTrace, blocks []PromptBlock) {
	seenHit := map[string]struct{}{}
	requestedK := 0
	for _, block := range blocks {
		if block.Bucket != BucketRAG {
			continue
		}
		if k, ok := block.Metadata["rag_requested_k"]; ok {
			if n := atoiSafe(k); n > requestedK {
				requestedK = n
			}
		}
		if raw, ok := block.Metadata["memory_hits_json"]; ok && strings.TrimSpace(raw) != "" {
			var hits []MemoryHit
			if err := json.Unmarshal([]byte(raw), &hits); err == nil {
				for _, hit := range hits {
					key := hit.ID + "|" + fmt.Sprintf("%.4f", hit.Similarity)
					if _, exists := seenHit[key]; exists {
						continue
					}
					seenHit[key] = struct{}{}
					trace.MemoryHits = append(trace.MemoryHits, hit)
				}
			}
		}
	}
	if requestedK < len(trace.MemoryHits) {
		requestedK = len(trace.MemoryHits)
	}
	trace.RAGStats.RequestedK = requestedK
}

// cloneBudgetMap 复制预算 map，避免后续阶段修改影响快照。
func cloneBudgetMap(in map[BudgetBucket]int) map[BudgetBucket]int {
	out := make(map[BudgetBucket]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// atoiSafe 是轻量数字解析，遇到非数字字符时返回当前累积值。
func atoiSafe(s string) int {
	n := 0
	for _, r := range strings.TrimSpace(s) {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
