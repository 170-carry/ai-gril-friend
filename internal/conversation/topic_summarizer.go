package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ai-gf/internal/llm"
)

// TopicSummaryRequest 是 LLM topic summarizer 的输入。
type TopicSummaryRequest struct {
	UserMessage  string
	Intent       Intent
	ActiveTopics []TopicSnapshot
}

// TopicSummaryThread 是一次长叙事中抽出的单条线程。
type TopicSummaryThread struct {
	Label         string   `json:"label"`
	Summary       string   `json:"summary"`
	CallbackHint  string   `json:"callback_hint,omitempty"`
	AliasTerms    []string `json:"alias_terms,omitempty"`
	SourceClauses []string `json:"source_clauses,omitempty"`
	Importance    int      `json:"importance,omitempty"`
}

// TopicSummaryResult 是 topic summarizer 的标准输出。
type TopicSummaryResult struct {
	Threads []TopicSummaryThread `json:"threads"`
}

// TopicSummarizer 描述可插拔的话题摘要器。
type TopicSummarizer interface {
	Summarize(ctx context.Context, req TopicSummaryRequest) (TopicSummaryResult, error)
}

type topicSummaryGenerator interface {
	Generate(ctx context.Context, req llm.GenerateRequest) (string, error)
}

// LLMTopicSummarizer 用 LLM 对超长叙事做线程抽取和摘要。
type LLMTopicSummarizer struct {
	generator topicSummaryGenerator
	mu        sync.RWMutex
	cache     map[string]TopicSummaryResult
}

// NewLLMTopicSummarizer 创建一个基于 llm.Provider 的话题摘要器。
func NewLLMTopicSummarizer(generator topicSummaryGenerator) *LLMTopicSummarizer {
	if generator == nil {
		return nil
	}
	return &LLMTopicSummarizer{
		generator: generator,
		cache:     map[string]TopicSummaryResult{},
	}
}

func (s *LLMTopicSummarizer) Summarize(ctx context.Context, req TopicSummaryRequest) (TopicSummaryResult, error) {
	if s == nil || s.generator == nil {
		return TopicSummaryResult{}, fmt.Errorf("topic summarizer is not configured")
	}
	req.UserMessage = strings.TrimSpace(req.UserMessage)
	if req.UserMessage == "" {
		return TopicSummaryResult{}, fmt.Errorf("topic summarizer input is empty")
	}

	cacheKey := buildTopicSummaryCacheKey(req)
	s.mu.RLock()
	if cached, ok := s.cache[cacheKey]; ok {
		s.mu.RUnlock()
		return cloneTopicSummaryResult(cached), nil
	}
	s.mu.RUnlock()

	raw, err := s.generator.Generate(ctx, llm.GenerateRequest{
		Messages: []llm.Message{
			{
				Role: "system",
				Content: "You are a careful Chinese conversation topic summarizer. " +
					"Return only valid JSON. Extract follow-up-worthy threads from a single user message. " +
					"Prefer concrete labels and concise summaries. Do not invent facts.",
			},
			{
				Role:    "user",
				Content: buildTopicSummaryPrompt(req),
			},
		},
		Temperature: 0,
	})
	if err != nil {
		return TopicSummaryResult{}, err
	}

	result, err := parseTopicSummaryResult(raw)
	if err != nil {
		return TopicSummaryResult{}, err
	}

	s.mu.Lock()
	s.cache[cacheKey] = cloneTopicSummaryResult(result)
	s.mu.Unlock()
	return result, nil
}

func buildTopicSummaryCacheKey(req TopicSummaryRequest) string {
	labels := make([]string, 0, len(req.ActiveTopics))
	for _, topic := range req.ActiveTopics {
		if label := strings.TrimSpace(topic.Label); label != "" {
			labels = append(labels, label)
		}
	}
	sort.Strings(labels)
	return strings.TrimSpace(string(req.Intent)) + "|" + strings.TrimSpace(req.UserMessage) + "|" + strings.Join(labels, ",")
}

func buildTopicSummaryPrompt(req TopicSummaryRequest) string {
	var b strings.Builder
	b.WriteString("请分析这条中文聊天消息，抽出 1~3 条适合后续继续聊的话题线程。\n")
	b.WriteString("要求：\n")
	b.WriteString("- 只返回 JSON，不要解释。\n")
	b.WriteString("- label 用 2~6 个中文，尽量具体，像“工作冲突”“睡眠状态”“家庭关系”，避免空泛词。\n")
	b.WriteString("- summary 用一句 10~28 字中文，概括这条线程的核心进展。\n")
	b.WriteString("- callback_hint 可为空；如果消息里有很适合回钩的原话/旧梗/强画面，再写进去，尽量不超过 16 字。\n")
	b.WriteString("- alias_terms 为 0~4 个短语，用于旧梗 / thread clustering。\n")
	b.WriteString("- source_clauses 为 1~2 个原始片段，不要改写太多。\n")
	b.WriteString("- importance 为 1~5，越适合后续跟进越高。\n")
	b.WriteString("- 多个片段如果本质是同一条线，要合并；如果是并行线程，要拆开。\n")
	if len(req.ActiveTopics) > 0 {
		b.WriteString("已有线程参考（如果能自然对齐就对齐，不要强行贴合）：\n")
		for _, topic := range req.ActiveTopics {
			label := strings.TrimSpace(topic.Label)
			if label == "" {
				continue
			}
			b.WriteString("- ")
			b.WriteString(label)
			if hint := strings.TrimSpace(topic.CallbackHint); hint != "" {
				b.WriteString(" / ")
				b.WriteString(hint)
			}
			b.WriteString("\n")
		}
	}
	if strings.TrimSpace(string(req.Intent)) != "" {
		b.WriteString("本轮主意图：")
		b.WriteString(strings.TrimSpace(string(req.Intent)))
		b.WriteString("\n")
	}
	b.WriteString("返回格式：\n")
	b.WriteString("{\"threads\":[{\"label\":\"\",\"summary\":\"\",\"callback_hint\":\"\",\"alias_terms\":[\"\"],\"source_clauses\":[\"\"],\"importance\":3}]}\n")
	b.WriteString("消息：\n")
	b.WriteString(strings.TrimSpace(req.UserMessage))
	return b.String()
}

func parseTopicSummaryResult(raw string) (TopicSummaryResult, error) {
	cleaned := extractTopicSummaryJSON(raw)
	var envelope struct {
		Threads []TopicSummaryThread `json:"threads"`
	}
	if err := json.Unmarshal([]byte(cleaned), &envelope); err == nil && len(envelope.Threads) > 0 {
		return sanitizeTopicSummaryResult(TopicSummaryResult{Threads: envelope.Threads}), nil
	}

	var direct []TopicSummaryThread
	if err := json.Unmarshal([]byte(cleaned), &direct); err == nil && len(direct) > 0 {
		return sanitizeTopicSummaryResult(TopicSummaryResult{Threads: direct}), nil
	}
	return TopicSummaryResult{}, fmt.Errorf("decode topic summarizer response failed")
}

func sanitizeTopicSummaryResult(in TopicSummaryResult) TopicSummaryResult {
	out := TopicSummaryResult{Threads: make([]TopicSummaryThread, 0, minInt(len(in.Threads), 3))}
	for _, thread := range in.Threads {
		thread = sanitizeTopicSummaryThread(thread)
		if strings.TrimSpace(thread.Label) == "" || strings.TrimSpace(thread.Summary) == "" {
			continue
		}
		out.Threads = append(out.Threads, thread)
		if len(out.Threads) >= 3 {
			break
		}
	}
	return out
}

func sanitizeTopicSummaryThread(thread TopicSummaryThread) TopicSummaryThread {
	thread.Label = trimRunes(strings.TrimSpace(thread.Label), 8)
	thread.Summary = trimRunes(strings.TrimSpace(thread.Summary), 32)
	thread.CallbackHint = trimRunes(strings.TrimSpace(thread.CallbackHint), 16)
	thread.Importance = clampInt(thread.Importance, 1, 5)
	if thread.Importance == 0 {
		thread.Importance = 3
	}
	thread.AliasTerms = sanitizeTopicSummaryList(thread.AliasTerms, 4, 10)
	thread.SourceClauses = sanitizeTopicSummaryList(thread.SourceClauses, 2, 22)
	if len(thread.SourceClauses) == 0 && thread.Summary != "" {
		thread.SourceClauses = []string{thread.Summary}
	}
	if len(thread.AliasTerms) == 0 {
		thread.AliasTerms = extractTopicAliasTerms(thread.Label, thread.Summary, thread.CallbackHint)
	}
	if thread.CallbackHint == "" && len(thread.AliasTerms) > 1 {
		thread.CallbackHint = thread.AliasTerms[1]
	}
	if thread.CallbackHint == "" {
		thread.CallbackHint = trimRunes(firstClause(thread.Summary), 16)
	}
	return thread
}

func sanitizeTopicSummaryList(items []string, limit int, maxRunes int) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, minInt(len(items), limit))
	seen := map[string]struct{}{}
	for _, item := range items {
		item = trimRunes(strings.TrimSpace(item), maxRunes)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func extractTopicSummaryJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```JSON")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end >= start {
		return raw[start : end+1]
	}
	return raw
}

func cloneTopicSummaryResult(in TopicSummaryResult) TopicSummaryResult {
	out := TopicSummaryResult{Threads: make([]TopicSummaryThread, 0, len(in.Threads))}
	for _, thread := range in.Threads {
		thread.AliasTerms = append([]string(nil), thread.AliasTerms...)
		thread.SourceClauses = append([]string(nil), thread.SourceClauses...)
		out.Threads = append(out.Threads, thread)
	}
	return out
}
