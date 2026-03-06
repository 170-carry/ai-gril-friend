package chat

import (
	"sort"

	"ai-gf/internal/prompt"
)

// DebugPromptReport 是 /chat/debug 返回的可读报告结构。
type DebugPromptReport struct {
	StageReports []prompt.StageReport `json:"stage_reports"`
	FinalBlocks  []DebugFinalBlock    `json:"final_blocks"`
	TrimLogs     []string             `json:"trim_logs"`
	MemoryHits   []prompt.MemoryHit   `json:"memory_hits"`
	RAGStats     prompt.RAGStats      `json:"rag_stats"`
}

// DebugFinalBlock 表示一个 block 在最终阶段的保留状态与原因。
type DebugFinalBlock struct {
	ID        string              `json:"id"`
	Priority  int                 `json:"priority"`
	Bucket    prompt.BudgetBucket `json:"bucket"`
	TokenEst  int                 `json:"token_est"`
	Kept      bool                `json:"kept"`
	LastStage string              `json:"last_stage"`
	Reason    string              `json:"reason"`
}

// buildDebugPromptReport 将底层 trace 汇总成便于前端展示的报告。
func buildDebugPromptReport(trace prompt.BuildTrace) DebugPromptReport {
	finalMap := map[string]DebugFinalBlock{}
	for _, d := range trace.BlockDecisions {
		finalMap[d.ID] = DebugFinalBlock{
			ID:        d.ID,
			Priority:  d.Priority,
			Bucket:    d.Bucket,
			TokenEst:  d.Tokens,
			Kept:      d.Kept,
			LastStage: d.Stage,
			Reason:    d.Reason,
		}
	}

	if len(trace.StageReports) > 0 {
		last := trace.StageReports[len(trace.StageReports)-1]
		for _, block := range last.Blocks {
			if _, ok := finalMap[block.ID]; !ok {
				finalMap[block.ID] = DebugFinalBlock{
					ID:        block.ID,
					Priority:  block.Priority,
					Bucket:    block.Bucket,
					TokenEst:  block.TokensEst,
					Kept:      true,
					LastStage: "final",
					Reason:    "present in final stage",
				}
			}
		}
	}

	finalBlocks := make([]DebugFinalBlock, 0, len(finalMap))
	for _, block := range finalMap {
		finalBlocks = append(finalBlocks, block)
	}
	sort.Slice(finalBlocks, func(i, j int) bool {
		if finalBlocks[i].Priority == finalBlocks[j].Priority {
			return finalBlocks[i].ID < finalBlocks[j].ID
		}
		return finalBlocks[i].Priority < finalBlocks[j].Priority
	})

	return DebugPromptReport{
		StageReports: trace.StageReports,
		FinalBlocks:  finalBlocks,
		TrimLogs:     trace.TrimLogs,
		MemoryHits:   trace.MemoryHits,
		RAGStats:     trace.RAGStats,
	}
}
