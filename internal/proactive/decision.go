package proactive

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"ai-gf/internal/repo"
)

// DecisionAction 表示当前主动任务的决策结果。
type DecisionAction string

const (
	DecisionSend   DecisionAction = "send"
	DecisionDefer  DecisionAction = "defer"
	DecisionCancel DecisionAction = "cancel"
)

// DecisionCheck 记录一次决策中的单项检查结果，便于 debug 观察。
type DecisionCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// DecisionResult 是“是否主动”决策器的输出。
type DecisionResult struct {
	Action              DecisionAction  `json:"action"`
	Summary             string          `json:"summary"`
	NextAttemptAt       *time.Time      `json:"next_attempt_at,omitempty"`
	CandidateContent    string          `json:"candidate_content"`
	RecentWindowSeconds int             `json:"recent_window_seconds"`
	SimilarityScore     float64         `json:"similarity_score,omitempty"`
	SimilarityThreshold float64         `json:"similarity_threshold"`
	Checks              []DecisionCheck `json:"checks"`
}

// DecisionConfig 控制主动发送前的判定阈值。
type DecisionConfig struct {
	DefaultRecentWindow time.Duration
	MinRecentWindow     time.Duration
	SimilarityLookback  time.Duration
	SimilarityThreshold float64
	ReasonMinRuneCount  int
}

// Normalize 对决策器配置做边界修正。
func (c DecisionConfig) Normalize() DecisionConfig {
	if c.DefaultRecentWindow <= 0 {
		c.DefaultRecentWindow = 12 * time.Hour
	}
	if c.MinRecentWindow <= 0 {
		c.MinRecentWindow = 6 * time.Hour
	}
	if c.SimilarityLookback <= 0 {
		c.SimilarityLookback = 24 * time.Hour
	}
	if c.SimilarityThreshold <= 0 || c.SimilarityThreshold >= 1 {
		c.SimilarityThreshold = 0.9
	}
	if c.ReasonMinRuneCount <= 0 {
		c.ReasonMinRuneCount = 4
	}
	return c
}

// DefaultDecisionConfig 返回默认的主动决策参数。
func DefaultDecisionConfig() DecisionConfig {
	return DecisionConfig{}.Normalize()
}

// DecisionEngine 负责判断一条到期任务此刻应不应该真正主动发送。
type DecisionEngine struct {
	cfg DecisionConfig
}

// NewDecisionEngine 创建主动决策器。
func NewDecisionEngine(cfg DecisionConfig) *DecisionEngine {
	return &DecisionEngine{cfg: cfg.Normalize()}
}

// Evaluate 根据任务、用户状态和最近一次主动记录，给出 send/defer/cancel 决策。
func (e *DecisionEngine) Evaluate(task repo.ProactiveTask, state repo.ProactiveState, now time.Time) DecisionResult {
	if e == nil {
		e = NewDecisionEngine(DefaultDecisionConfig())
	}
	if now.IsZero() {
		now = time.Now()
	}

	candidate := buildCandidateQueueItem(task)
	candidateContent, _ := BuildOutboundMessage(candidate)
	result := DecisionResult{
		Action:              DecisionSend,
		Summary:             "通过主动发送决策",
		CandidateContent:    candidateContent,
		RecentWindowSeconds: int(e.resolveRecentWindow(task, state).Seconds()),
		SimilarityThreshold: e.cfg.SimilarityThreshold,
		Checks:              make([]DecisionCheck, 0, 5),
	}

	if !state.Enabled {
		result.Action = DecisionCancel
		result.Summary = "用户已关闭主动消息"
		result.Checks = append(result.Checks, DecisionCheck{
			Name:   "enabled",
			Passed: false,
			Detail: "用户主动消息开关为关闭状态",
		})
		return result
	}
	result.Checks = append(result.Checks, DecisionCheck{
		Name:   "enabled",
		Passed: true,
		Detail: "用户已开启主动消息",
	})

	if deferQuiet, nextAt := ShouldDeferForQuietHours(state, now); deferQuiet {
		next := nextAt
		result.Action = DecisionDefer
		result.Summary = "命中免打扰时间"
		result.NextAttemptAt = &next
		result.Checks = append(result.Checks, DecisionCheck{
			Name:   "quiet_hours",
			Passed: false,
			Detail: "当前时间命中免打扰窗口，顺延到允许时间",
		})
		return result
	}
	result.Checks = append(result.Checks, DecisionCheck{
		Name:   "quiet_hours",
		Passed: true,
		Detail: "当前不在免打扰窗口内",
	})

	if !e.hasExplicitReason(task) {
		result.Action = DecisionCancel
		result.Summary = "缺少明确的主动理由"
		result.Checks = append(result.Checks, DecisionCheck{
			Name:   "explicit_reason",
			Passed: false,
			Detail: "reason 为空、过短或只包含泛化描述，无法支撑主动触达",
		})
		return result
	}
	result.Checks = append(result.Checks, DecisionCheck{
		Name:   "explicit_reason",
		Passed: true,
		Detail: "存在明确的主动理由",
	})

	if deferRecent, nextAt := e.shouldDeferForRecentProactive(state, task, now); deferRecent {
		next := nextAt
		result.Action = DecisionDefer
		result.Summary = "最近已主动过，命中频率窗口"
		result.NextAttemptAt = &next
		result.Checks = append(result.Checks, DecisionCheck{
			Name:   "recent_proactive",
			Passed: false,
			Detail: "最近主动时间仍在频率窗口内，避免连续打扰",
		})
		return result
	}
	result.Checks = append(result.Checks, DecisionCheck{
		Name:   "recent_proactive",
		Passed: true,
		Detail: "最近主动频率在允许范围内",
	})

	score, triggered := e.checkSimilarity(task, state, candidateContent, now)
	result.SimilarityScore = score
	if triggered {
		result.Action = DecisionCancel
		result.Summary = "本次主动文案与上次过于相似"
		result.Checks = append(result.Checks, DecisionCheck{
			Name:   "copy_similarity",
			Passed: false,
			Detail: "与最近一次同类型主动文案过于接近，跳过本次发送",
		})
		return result
	}
	result.Checks = append(result.Checks, DecisionCheck{
		Name:   "copy_similarity",
		Passed: true,
		Detail: "与上次主动文案差异足够大",
	})

	return result
}

// buildCandidateQueueItem 把任务先投影成候选消息，用于发送前决策和 debug 预览。
func buildCandidateQueueItem(task repo.ProactiveTask) repo.OutboundQueueItem {
	return repo.OutboundQueueItem{
		ID:        0,
		UserID:    task.UserID,
		SessionID: task.SessionID,
		TaskID:    task.ID,
		Reason:    task.Reason,
		DedupKey:  task.DedupKey,
		Payload:   BuildOutboundPayload(task),
	}
}

// hasExplicitReason 判断当前任务是否具备足够明确的主动触发理由。
func (e *DecisionEngine) hasExplicitReason(task repo.ProactiveTask) bool {
	reason := strings.TrimSpace(task.Reason)
	if utf8.RuneCountInString(reason) >= e.cfg.ReasonMinRuneCount && !isGenericReason(reason) {
		return true
	}

	switch strings.TrimSpace(task.TaskType) {
	case "event_reminder":
		return strings.TrimSpace(toString(task.Payload["target_title"])) != "" ||
			strings.TrimSpace(toString(task.Payload["event_title"])) != ""
	case "care_followup":
		return strings.Contains(reason, "回访") || strings.Contains(reason, "情绪")
	case "topic_reengage":
		return strings.TrimSpace(toString(task.Payload["topic_label"])) != "" ||
			strings.TrimSpace(toString(task.Payload["topic_key"])) != ""
	default:
		return false
	}
}

// resolveRecentWindow 计算最近主动频率窗口，默认至少保留 6 小时的防打扰间隔。
func (e *DecisionEngine) resolveRecentWindow(task repo.ProactiveTask, state repo.ProactiveState) time.Duration {
	window := e.cfg.DefaultRecentWindow
	taskWindow := time.Duration(task.CooldownSeconds) * time.Second
	stateWindow := time.Duration(state.CooldownSeconds) * time.Second
	if taskWindow > window {
		window = taskWindow
	}
	if stateWindow > window {
		window = stateWindow
	}
	if window < e.cfg.MinRecentWindow {
		window = e.cfg.MinRecentWindow
	}
	return window
}

// shouldDeferForRecentProactive 判断最近是否已经主动过，如果是则顺延到窗口结束。
func (e *DecisionEngine) shouldDeferForRecentProactive(state repo.ProactiveState, task repo.ProactiveTask, now time.Time) (bool, time.Time) {
	if state.LastProactiveAt == nil {
		return false, time.Time{}
	}
	window := e.resolveRecentWindow(task, state)
	nextAllowed := state.LastProactiveAt.Add(window)
	if now.Before(nextAllowed) {
		return true, nextAllowed
	}
	return false, time.Time{}
}

// checkSimilarity 判断本次候选文案是否和最近一次同类型主动消息过于相似。
func (e *DecisionEngine) checkSimilarity(task repo.ProactiveTask, state repo.ProactiveState, candidateContent string, now time.Time) (float64, bool) {
	lastContent := strings.TrimSpace(state.LastProactiveContent)
	if state.LastProactiveAt == nil || lastContent == "" {
		return 0, false
	}
	if strings.TrimSpace(state.LastProactiveTaskType) != strings.TrimSpace(task.TaskType) {
		return 0, false
	}
	if now.Sub(*state.LastProactiveAt) > e.cfg.SimilarityLookback {
		return 0, false
	}

	score := TextSimilarity(candidateContent, lastContent)
	return score, score >= e.cfg.SimilarityThreshold
}

// TextSimilarity 用 Dice 系数比较两段短文本，适合判断模板文案是否几乎重复。
func TextSimilarity(a, b string) float64 {
	left := normalizeComparableText(a)
	right := normalizeComparableText(b)
	if left == "" || right == "" {
		return 0
	}
	if left == right {
		return 1
	}

	leftBigrams := buildBigrams(left)
	rightBigrams := buildBigrams(right)
	if len(leftBigrams) == 0 || len(rightBigrams) == 0 {
		if left == right {
			return 1
		}
		return 0
	}

	intersection := 0
	leftCount := 0
	rightCount := 0
	for _, count := range leftBigrams {
		leftCount += count
	}
	for _, count := range rightBigrams {
		rightCount += count
	}
	for gram, leftCountOne := range leftBigrams {
		if rightCountOne, ok := rightBigrams[gram]; ok {
			if leftCountOne < rightCountOne {
				intersection += leftCountOne
			} else {
				intersection += rightCountOne
			}
		}
	}
	if leftCount+rightCount == 0 {
		return 0
	}
	return float64(2*intersection) / float64(leftCount+rightCount)
}

// normalizeComparableText 把文本归一化，只保留字母、数字和中文，避免标点干扰相似度。
func normalizeComparableText(in string) string {
	in = strings.TrimSpace(strings.ToLower(in))
	if in == "" {
		return ""
	}
	var out []rune
	for _, r := range in {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), unicode.Is(unicode.Han, r):
			out = append(out, r)
		}
	}
	return string(out)
}

// buildBigrams 把短文本拆成二元组，多用于模板文案相似度比对。
func buildBigrams(in string) map[string]int {
	runes := []rune(in)
	if len(runes) < 2 {
		if len(runes) == 1 {
			return map[string]int{string(runes): 1}
		}
		return map[string]int{}
	}
	out := make(map[string]int, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		out[string(runes[i:i+2])]++
	}
	return out
}

// isGenericReason 过滤掉无法解释“为什么要主动”的泛化 reason。
func isGenericReason(reason string) bool {
	normalized := normalizeComparableText(reason)
	if normalized == "" {
		return true
	}
	switch normalized {
	case "主动消息", "主动提醒", "主动关心", "主动触达", "提醒", "回访", "关心", "跟进", "系统触发":
		return true
	default:
		return false
	}
}
