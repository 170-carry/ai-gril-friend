package extractor

import (
	"context"
	"strings"
)

// Extractor 负责把一轮对话抽取为可持久化记忆。
type Extractor struct{}

// New 创建默认抽取器。
func New() *Extractor {
	return &Extractor{}
}

// Extract 执行多路抽取：偏好、边界、事件、事实。
func (e *Extractor) Extract(ctx context.Context, req Request) Result {
	_ = ctx
	msg := strings.TrimSpace(req.UserMessage)
	assistantMsg := strings.TrimSpace(req.AssistantMessage)
	contextText := strings.TrimSpace(req.ConversationContext)
	if msg == "" {
		return Result{}
	}

	out := Result{}
	out.Preferences = append(out.Preferences, extractPreferences(msg)...)
	out.Preferences = append(out.Preferences, extractPreferencesWithContext(msg, assistantMsg, contextText)...)
	out.Boundaries = append(out.Boundaries, extractBoundaries(msg)...)
	out.Events = append(out.Events, extractEvents(msg, req.Now)...)
	out.Events = append(out.Events, extractEventsWithContext(msg, assistantMsg, req.Now)...)
	out.Facts = append(out.Facts, extractFacts(msg)...)
	out.Facts = append(out.Facts, extractFactsWithContext(msg, assistantMsg, contextText)...)

	out.Preferences = dedupPreferences(out.Preferences)
	out.Boundaries = dedupBoundaries(out.Boundaries)
	out.Events = dedupEvents(out.Events)
	out.Facts = dedupFacts(out.Facts)
	return out
}

// BuildSemanticSentences 把抽取结果转换成可写入语义记忆的标准句。
func BuildSemanticSentences(out Result) []string {
	lines := make([]string, 0, len(out.Preferences)+len(out.Boundaries)+len(out.Events)+len(out.Facts))
	for _, p := range out.Preferences {
		lines = append(lines, "用户偏好："+p.Category+"="+p.Value)
	}
	for _, b := range out.Boundaries {
		line := "用户边界：" + b.Topic
		if strings.TrimSpace(b.Description) != "" {
			line += "（" + strings.TrimSpace(b.Description) + "）"
		}
		lines = append(lines, line)
	}
	for _, ev := range out.Events {
		lines = append(lines, "用户事件："+ev.Title+" @ "+ev.EventTime.Format("2006-01-02 15:04"))
	}
	for _, f := range out.Facts {
		lines = append(lines, "用户事实："+f.Key+"="+f.Value)
	}
	return uniqueLines(lines)
}

func uniqueLines(lines []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		out = append(out, line)
	}
	return out
}
