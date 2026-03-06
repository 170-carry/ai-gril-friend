package prompt

import (
	"context"
	"sort"
	"strings"
)

// defaultPolicyEngine 处理模块冲突与开关过滤策略。
type defaultPolicyEngine struct{}

// newDefaultPolicyEngine 创建默认策略引擎。
func newDefaultPolicyEngine() PolicyEngine {
	return &defaultPolicyEngine{}
}

// Apply 逐块执行策略裁决，并记录 trace 决策原因。
func (e *defaultPolicyEngine) Apply(ctx context.Context, req BuildRequest, blocks []PromptBlock, trace *BuildTrace) []PromptBlock {
	filtered := make([]PromptBlock, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block.Content) == "" {
			trace.markStage("policy", block, false, "empty")
			continue
		}
		filtered = append(filtered, block)
	}

	boundaryKeywords := collectBoundaryKeywords(filtered)

	out := make([]PromptBlock, 0, len(filtered))
	for _, block := range filtered {
		if block.Bucket == BucketRAG && !req.Options.EnableRAG {
			trace.markStage("policy", block, false, "rag disabled")
			continue
		}
		if block.Bucket == BucketEmotion && !req.Options.EnableEmotion {
			trace.markStage("policy", block, false, "emotion disabled")
			continue
		}
		if block.ID == "events" && !req.Options.EnableEvents {
			trace.markStage("policy", block, false, "events disabled")
			continue
		}

		if block.Bucket == BucketRAG && len(boundaryKeywords) > 0 {
			lower := strings.ToLower(block.Content)
			conflict := false
			matched := ""
			for _, kw := range boundaryKeywords {
				if strings.Contains(lower, strings.ToLower(kw)) {
					conflict = true
					matched = kw
					break
				}
			}
			if conflict {
				reason := "conflict with boundaries"
				if matched != "" {
					reason += ": " + matched
				}
				trace.markStage("policy", block, false, reason)
				continue
			}
		}

		out = append(out, block)
		trace.markStage("policy", block, true, "policy keep")
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority == out[j].Priority {
			return out[i].ID < out[j].ID
		}
		return out[i].Priority < out[j].Priority
	})

	return out
}

// collectBoundaryKeywords 提取边界 block 中的关键词供冲突判断使用。
func collectBoundaryKeywords(blocks []PromptBlock) []string {
	for _, block := range blocks {
		if block.ID == "boundaries" {
			return parseBoundaryKeywords(boundaryBody(block.Content))
		}
	}
	return nil
}

// boundaryBody 提取边界 block 的正文，避免把标题/标签词误当成用户雷区关键词。
func boundaryBody(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if idx := strings.Index(content, "\n"); idx >= 0 {
		body := strings.TrimSpace(content[idx+1:])
		if body != "" {
			return body
		}
	}
	return content
}
