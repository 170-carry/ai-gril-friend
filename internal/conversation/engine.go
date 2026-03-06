package conversation

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Engine 是 Conversation Engine（CE）入口，负责把输入上下文转换为可执行的对话计划。
type Engine struct{}

var (
	// timeSignalPattern 只匹配真正像“时间”的片段，避免“有点焦虑”误命中“点”。
	timeSignalPattern = regexp.MustCompile(`(\d{1,2}\s*点|\d{1,2}\s*号|\d{1,2}:\d{2})`)
)

// NewEngine 创建默认 CE 实例（规则路由 + 状态机 + 规划器）。
func NewEngine() *Engine {
	return &Engine{}
}

// BuildPlan 生成一轮对话计划。
// 目标：同一输入稳定得到同一策略，供 PromptBuilder 注入。
func (e *Engine) BuildPlan(ctx context.Context, req ConversationRequest) ConversationPlan {
	_ = ctx
	req = req.Normalize()

	intent, confidence := routeIntent(req.UserMessage)
	currentState, nextState := resolveStates(intent, req.UserMessage)
	mode := resolveMode(intent, currentState, nextState, req.UserMessage)
	tone := resolveTone(intent, req.RelationshipState, req.MemorySnapshot.UserPreferences)
	structure := resolveStructure(intent, mode, req.UserMessage, req.EmotionIntensity)
	questions := resolveQuestions(intent, mode, req.UserMessage)
	stopRules := resolveStopRules(req.UserMessage)
	actions := resolveActions(req, intent)

	plan := ConversationPlan{
		Intent:            intent,
		IntentConfidence:  confidence,
		Mode:              mode,
		CurrentState:      currentState,
		NextState:         nextState,
		Tone:              tone,
		ResponseStructure: structure,
		Questions:         questions,
		Actions:           actions,
		StopRules:         stopRules,
	}

	// 最终安全门：对高风险/边界拒绝内容做硬性收敛，避免规划器继续深挖。
	applySafetyAndBoundaryGate(&plan, req)
	plan.Questions = limitQuestions(plan.Questions, 1)
	plan.Actions = dedupActions(plan.Actions)
	return plan
}

// RenderPlanForPrompt 将结构化计划渲染为可注入 prompt 的文本块。
func RenderPlanForPrompt(plan ConversationPlan) string {
	lines := make([]string, 0, 16)
	lines = append(lines, fmt.Sprintf("- intent: %s (conf=%.2f)", plan.Intent, clamp01(plan.IntentConfidence)))
	lines = append(lines, "- mode: "+strings.TrimSpace(plan.Mode))
	lines = append(lines, fmt.Sprintf("- state: %s -> %s", plan.CurrentState, plan.NextState))
	lines = append(lines, fmt.Sprintf("- tone: warmth=%.2f, playfulness=%.2f, directness=%.2f, length=%s, emoji_level=%d",
		clamp01(plan.Tone.Warmth), clamp01(plan.Tone.Playfulness), clamp01(plan.Tone.Directness), strings.TrimSpace(plan.Tone.Length), clampInt(plan.Tone.EmojiLevel, 0, 2)))

	if len(plan.ResponseStructure) > 0 {
		lines = append(lines, "- structure: "+strings.Join(plan.ResponseStructure, " -> "))
	}

	if len(plan.Questions) > 0 {
		lines = append(lines, "- ask: [\""+strings.Join(plan.Questions, "\", \"")+"\"]")
	} else {
		lines = append(lines, "- ask: []")
	}

	if len(plan.StopRules) > 0 {
		lines = append(lines, "- stop_rules:")
		for _, rule := range plan.StopRules {
			lines = append(lines, "  - "+rule)
		}
	}

	if len(plan.Actions) > 0 {
		lines = append(lines, "- actions:")
		for _, action := range plan.Actions {
			lines = append(lines, "  - "+renderAction(action))
		}
	}
	return strings.Join(lines, "\n")
}

// renderAction 统一动作展示格式，便于 debug 与 prompt 阅读。
func renderAction(action Action) string {
	if len(action.Params) == 0 {
		return action.Type
	}
	keys := make([]string, 0, len(action.Params))
	for k := range action.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+strings.TrimSpace(action.Params[k]))
	}
	return action.Type + "(" + strings.Join(pairs, ", ") + ")"
}

// routeIntent 通过规则路由用户意图，并返回置信度。
func routeIntent(msg string) (Intent, float64) {
	text := strings.ToLower(strings.TrimSpace(msg))
	if text == "" {
		return IntentSmallTalk, 0.4
	}

	scores := map[Intent]int{
		IntentEmotionalSupport: scoreKeywords(text, []string{"焦虑", "难受", "崩溃", "压力", "低落", "委屈", "心累", "烦", "孤独", "害怕", "失眠"}),
		IntentAdviceSolving:    scoreKeywords(text, []string{"怎么办", "怎么做", "建议", "方案", "步骤", "帮我想", "如何", "要不要", "能不能"}),
		IntentSmallTalk:        scoreKeywords(text, []string{"在吗", "你好", "哈喽", "哈哈", "聊聊", "最近怎么样"}),
		IntentStorySharing:     scoreKeywords(text, []string{"我今天", "刚刚", "发生", "经历", "想跟你说", "告诉你"}),
		IntentPlanningEvent:    scoreKeywords(text, []string{"明天", "下周", "今晚", "面试", "会议", "提醒", "日程", "安排", "计划"}),
		IntentRelationship:     scoreKeywords(text, []string{"想你", "抱抱", "亲亲", "恋爱", "喜欢你", "爱你", "暧昧"}),
		IntentBoundarySafety:   scoreKeywords(text, []string{"别问", "不想聊", "别再提", "不要提", "隐私", "轻生", "自杀", "伤害", "违法"}),
		IntentMetaProduct:      scoreKeywords(text, []string{"你是谁", "你能做什么", "怎么工作", "模型", "prompt", "系统"}),
	}

	intent := IntentSmallTalk
	top := -1
	second := -1
	for k, v := range scores {
		if v > top {
			second = top
			top = v
			intent = k
			continue
		}
		if v > second {
			second = v
		}
	}

	if top <= 0 {
		// 无明显命中时，带问号偏向“建议”，否则偏向“闲聊”。
		if strings.Contains(text, "？") || strings.Contains(text, "?") {
			return IntentAdviceSolving, 0.45
		}
		return IntentSmallTalk, 0.40
	}

	// “焦虑 + 怎么办”优先归入情绪支持，先接住再解决。
	if scores[IntentEmotionalSupport] > 0 && scores[IntentAdviceSolving] > 0 && scores[IntentEmotionalSupport] >= scores[IntentAdviceSolving] {
		intent = IntentEmotionalSupport
	}

	margin := top - maxInt(second, 0)
	conf := 0.55 + 0.08*float64(top) + 0.06*float64(margin)
	return intent, clamp01(conf)
}

// resolveStates 根据意图和用户措辞，给出当前/下一状态。
func resolveStates(intent Intent, userMessage string) (State, State) {
	text := strings.ToLower(strings.TrimSpace(userMessage))
	if containsAny(text, []string{"谢谢", "明白了", "我去做", "先这样", "晚点聊"}) {
		return StateClose, StateClose
	}
	if containsAny(text, []string{"我会试试", "我去准备", "我现在就做"}) {
		return StateCommit, StateClose
	}

	switch intent {
	case IntentEmotionalSupport:
		if containsAny(text, []string{"怎么办", "怎么做", "要怎么"}) {
			return StateSupport, StateSolve
		}
		return StateSupport, StateExplore
	case IntentAdviceSolving, IntentPlanningEvent:
		return StateSolve, StateCommit
	case IntentBoundarySafety:
		return StateSupport, StateClose
	case IntentMetaProduct:
		return StateSolve, StateClose
	case IntentRelationship, IntentStorySharing:
		return StateExplore, StateClose
	default:
		return StateOpen, StateExplore
	}
}

// resolveMode 把意图和状态映射到对话模式。
func resolveMode(intent Intent, current, next State, userMessage string) string {
	switch intent {
	case IntentEmotionalSupport:
		if next == StateSolve {
			return "support_then_solve"
		}
		return "support_only"
	case IntentAdviceSolving:
		return "solve_then_commit"
	case IntentPlanningEvent:
		return "plan_then_commit"
	case IntentRelationship:
		return "intimacy_light"
	case IntentBoundarySafety:
		return "safe_redirect"
	case IntentMetaProduct:
		return "meta_explain"
	case IntentStorySharing:
		return "story_companion"
	default:
		if containsAny(strings.ToLower(userMessage), []string{"哈哈", "在吗", "聊聊"}) {
			return "small_talk"
		}
		return "companion_chat"
	}
}

// resolveTone 生成可控风格参数（warmth/playfulness/directness/length/emoji_level）。
func resolveTone(intent Intent, relationshipState string, preferences string) ToneConfig {
	tone := ToneConfig{
		Warmth:      0.78,
		Playfulness: 0.25,
		Directness:  0.45,
		Length:      "medium",
		EmojiLevel:  1,
	}

	switch strings.ToLower(strings.TrimSpace(relationshipState)) {
	case "stranger":
		tone.Warmth = 0.62
		tone.Playfulness = 0.08
		tone.EmojiLevel = 0
	case "friend":
		tone.Warmth = 0.72
		tone.Playfulness = 0.18
	case "romantic":
		tone.Warmth = 0.90
		tone.Playfulness = 0.35
		tone.EmojiLevel = 1
	}

	switch intent {
	case IntentEmotionalSupport:
		tone.Warmth = maxFloat(tone.Warmth, 0.90)
		tone.Directness = 0.35
		tone.Length = "medium"
	case IntentAdviceSolving, IntentPlanningEvent:
		tone.Directness = 0.75
		tone.Playfulness = minFloat(tone.Playfulness, 0.16)
		tone.Length = "short"
	case IntentMetaProduct:
		tone.Directness = 0.80
		tone.Playfulness = 0
		tone.EmojiLevel = 0
		tone.Length = "short"
	case IntentBoundarySafety:
		tone.Warmth = maxFloat(tone.Warmth, 0.85)
		tone.Directness = 0.55
		tone.Playfulness = 0
		tone.EmojiLevel = 0
	}

	pref := strings.ToLower(strings.TrimSpace(preferences))
	if containsAny(pref, []string{"简洁", "直接", "别太长"}) {
		tone.Length = "short"
		tone.Directness = maxFloat(tone.Directness, 0.72)
	}
	if containsAny(pref, []string{"详细", "多聊", "陪我聊"}) {
		if tone.Length == "short" {
			tone.Length = "medium"
		}
	}

	tone.Warmth = clamp01(tone.Warmth)
	tone.Playfulness = clamp01(tone.Playfulness)
	tone.Directness = clamp01(tone.Directness)
	tone.EmojiLevel = clampInt(tone.EmojiLevel, 0, 2)
	return tone
}

// resolveStructure 给出推荐段落结构（Mirror / Ask / Add Value / Close Softly）。
func resolveStructure(intent Intent, mode string, userMessage string, emotionIntensity float64) []string {
	if shouldStopAsking(userMessage) {
		return []string{"mirror", "add_value", "close_softly"}
	}

	switch {
	case mode == "safe_redirect":
		return []string{"mirror", "add_value", "close_softly"}
	case intent == IntentMetaProduct:
		return []string{"add_value", "close_softly"}
	case intent == IntentEmotionalSupport && emotionIntensity >= 0.75 && !asksHowTo(userMessage):
		return []string{"mirror", "add_value", "close_softly"}
	default:
		return []string{"mirror", "ask", "add_value", "close_softly"}
	}
}

// resolveQuestions 生成 0~1 个推进型追问。
func resolveQuestions(intent Intent, mode string, userMessage string) []string {
	if shouldStopAsking(userMessage) || mode == "safe_redirect" || mode == "safety_support" {
		return nil
	}

	text := strings.ToLower(strings.TrimSpace(userMessage))
	switch intent {
	case IntentEmotionalSupport:
		if containsAny(text, []string{"面试", "汇报", "考试"}) {
			return []string{"你现在最担心的是哪一块？"}
		}
		return []string{"你现在最卡住的是哪一块？"}
	case IntentAdviceSolving:
		return []string{"你更想先要一个可执行小步骤，还是先理清原因？"}
	case IntentPlanningEvent:
		return []string{"你想让我帮你拆成 3 个小步骤吗？"}
	case IntentRelationship:
		return []string{"你现在更想被我抱抱安慰，还是一起做点轻松的事？"}
	case IntentStorySharing:
		return []string{"这件事里你最在意的点是什么？"}
	default:
		if containsAny(text, []string{"喝什么", "吃什么", "选哪个"}) {
			return []string{"你更偏向口感浓一点，还是清爽一点？"}
		}
		return []string{"你想继续聊这个，还是换个轻松话题？"}
	}
}

// resolveStopRules 构建收束规则，防止追问过度。
func resolveStopRules(userMessage string) []string {
	rules := []string{
		"每轮最多提 1 个问题。",
		"若用户未接追问或转移话题，下一轮不继续追问。",
	}
	if shouldStopAsking(userMessage) {
		rules = append(rules, "用户已表达拒绝/疲惫，立即停止追问，改为支持或收束。")
	}
	return rules
}

// resolveActions 生成记忆钩子与主动触发钩子的动作建议。
func resolveActions(req ConversationRequest, intent Intent) []Action {
	text := strings.TrimSpace(req.UserMessage)
	lower := strings.ToLower(text)
	actions := make([]Action, 0, 6)

	// 偏好写入：明确“我喜欢/爱喝/爱吃”时触发。
	if value := extractPreferenceValue(text); value != "" {
		actions = append(actions, Action{
			Type: "SAVE_PREFERENCE",
			Params: map[string]string{
				"category": inferPreferenceCategory(value),
				"value":    value,
			},
			Reason: "用户表达稳定偏好",
		})
	}

	// 边界写入：明确“别问/别提/不想聊”时触发。
	if topic := extractBoundaryTopic(text); topic != "" {
		actions = append(actions, Action{
			Type: "SAVE_BOUNDARY",
			Params: map[string]string{
				"topic": topic,
			},
			Reason: "用户明确提出边界",
		})
	}

	// 事件写入：检测到“明天/下周/几点/面试”等时间线索时触发。
	if title, timeHint := extractEventSignal(text); title != "" {
		actions = append(actions, Action{
			Type: "SAVE_EVENT",
			Params: map[string]string{
				"title":     title,
				"time_hint": timeHint,
			},
			Reason: "用户提到未来事件或日程",
		})
		actions = append(actions, Action{
			Type: "SCHEDULE_EVENT_REMINDER",
			Params: map[string]string{
				"title":  title,
				"offset": "-2h",
			},
			Reason: "事件前提醒（主动钩子）",
		})
	}

	// 语义记忆写入：对高信息密度内容做一句话沉淀。
	if shouldSaveSemanticMemory(lower) {
		actions = append(actions, Action{
			Type: "SAVE_SEMANTIC_MEMORY",
			Params: map[string]string{
				"sentence": trimRunes(text, 80),
			},
			Reason: "用户输入包含可复用事实线索",
		})
	}

	// 高强度负向情绪时，安排温和回访（主动钩子）。
	if req.EmotionIntensity >= 0.75 && (req.Emotion == "sad" || req.Emotion == "low" || req.Emotion == "anxious" || req.Emotion == "lonely") {
		actions = append(actions, Action{
			Type: "SCHEDULE_CARE_FOLLOWUP",
			Params: map[string]string{
				"offset": "24h",
			},
			Reason: "高强度负面情绪回访",
		})
	}

	// 关系互动类场景通常无需写太多动作，避免过度记忆化。
	if intent == IntentRelationship && len(actions) > 2 {
		actions = actions[:2]
	}
	return actions
}

// applySafetyAndBoundaryGate 在最终计划层执行安全/边界硬约束。
func applySafetyAndBoundaryGate(plan *ConversationPlan, req ConversationRequest) {
	text := strings.ToLower(strings.TrimSpace(req.UserMessage))

	// 高风险安全词命中时，强制切换为 safety_support 模式并禁止追问。
	if containsAny(text, []string{"自杀", "轻生", "结束生命", "伤害自己"}) {
		plan.Intent = IntentBoundarySafety
		plan.IntentConfidence = maxFloat(plan.IntentConfidence, 0.95)
		plan.Mode = "safety_support"
		plan.CurrentState = StateSupport
		plan.NextState = StateClose
		plan.ResponseStructure = []string{"mirror", "add_value", "close_softly"}
		plan.Questions = nil
		plan.StopRules = append(plan.StopRules, "命中高风险安全内容：不追问细节，优先现实求助建议。")
		plan.Tone.Warmth = maxFloat(plan.Tone.Warmth, 0.95)
		plan.Tone.Playfulness = 0
		plan.Tone.EmojiLevel = 0
		return
	}

	// 用户显式拒绝时停止追问。
	if shouldStopAsking(text) {
		plan.Questions = nil
		plan.StopRules = append(plan.StopRules, "用户已拒绝追问：本轮不再提问。")
	}

	// 若用户消息命中历史边界关键词，收敛为 safe_redirect。
	if hitBoundaryText(text, req.MemorySnapshot.UserBoundaries) {
		plan.Mode = "safe_redirect"
		plan.CurrentState = StateSupport
		plan.NextState = StateClose
		plan.ResponseStructure = []string{"mirror", "add_value", "close_softly"}
		plan.Questions = nil
		plan.StopRules = append(plan.StopRules, "命中用户边界关键词：停止深挖，轻收束或换话题。")
	}
}

// hitBoundaryText 判断用户输入是否与边界关键词直接冲突。
func hitBoundaryText(userText, boundaries string) bool {
	userText = strings.TrimSpace(strings.ToLower(userText))
	if userText == "" {
		return false
	}
	keywords := parseBoundaryKeywords(boundaries)
	for _, kw := range keywords {
		if strings.Contains(userText, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// parseBoundaryKeywords 从边界文本中提取关键词列表（轻量规则）。
func parseBoundaryKeywords(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" || text == "暂无" {
		return nil
	}
	replacer := strings.NewReplacer("\n", " ", "，", " ", "。", " ", "、", " ", ",", " ", "；", " ", ";", " ", "：", " ", ":", " ")
	text = replacer.Replace(text)
	parts := strings.Fields(text)
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if len([]rune(p)) < 2 {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func shouldStopAsking(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	return containsAny(text, []string{"别问", "不要问", "不想聊", "先这样", "算了", "不用了", "有点累", "不想说"})
}

func asksHowTo(text string) bool {
	return containsAny(strings.ToLower(text), []string{"怎么办", "怎么做", "如何", "要怎么"})
}

func containsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func scoreKeywords(text string, keywords []string) int {
	score := 0
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			score++
		}
	}
	return score
}

func limitQuestions(questions []string, limit int) []string {
	if limit <= 0 || len(questions) == 0 {
		return nil
	}
	out := make([]string, 0, minInt(len(questions), limit))
	seen := map[string]struct{}{}
	for _, q := range questions {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if _, ok := seen[q]; ok {
			continue
		}
		seen[q] = struct{}{}
		out = append(out, q)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func dedupActions(actions []Action) []Action {
	if len(actions) == 0 {
		return nil
	}
	out := make([]Action, 0, len(actions))
	seen := map[string]struct{}{}
	for _, action := range actions {
		key := action.Type + "|" + renderAction(action)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, action)
	}
	return out
}

func extractPreferenceValue(text string) string {
	reList := []*regexp.Regexp{
		regexp.MustCompile(`(?:我)?(?:喜欢|爱喝|爱吃)\s*([^，。！？\n]{1,20})`),
		regexp.MustCompile(`记住(?:我)?(?:喜欢|爱喝|爱吃)\s*([^，。！？\n]{1,20})`),
	}
	for _, re := range reList {
		matches := re.FindStringSubmatch(strings.TrimSpace(text))
		if len(matches) >= 2 {
			return strings.TrimSpace(matches[1])
		}
	}
	return ""
}

func inferPreferenceCategory(value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	switch {
	case containsAny(v, []string{"拿铁", "美式", "咖啡", "茶", "奶茶"}):
		return "drink"
	case containsAny(v, []string{"火锅", "米饭", "面", "香菜"}):
		return "food"
	default:
		return "general"
	}
}

func extractBoundaryTopic(text string) string {
	re := regexp.MustCompile(`(?:别再提|不要聊|不想聊|别问|不要问)\s*([^，。！？\n]{0,20})`)
	matches := re.FindStringSubmatch(strings.TrimSpace(text))
	if len(matches) < 2 {
		return ""
	}
	topic := strings.TrimSpace(matches[1])
	if topic == "" {
		return "sensitive_topic"
	}
	return topic
}

func extractEventSignal(text string) (title string, timeHint string) {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return "", ""
	}
	if !containsAny(lower, []string{"明天", "后天", "下周", "今晚", "明早", "下午", "面试", "会议", "约会", "安排"}) &&
		!timeSignalPattern.MatchString(lower) {
		return "", ""
	}
	title = "用户提到未来安排"
	switch {
	case strings.Contains(lower, "面试"):
		title = "面试"
	case strings.Contains(lower, "会议"):
		title = "会议"
	case strings.Contains(lower, "约会"):
		title = "约会"
	}
	timeHint = trimRunes(strings.TrimSpace(text), 24)
	return title, timeHint
}

func shouldSaveSemanticMemory(lower string) bool {
	return containsAny(lower, []string{
		"我是", "我在", "我喜欢", "我不喜欢", "记住", "别再提", "明天", "下周", "面试", "生日", "工作",
	})
}

func trimRunes(text string, n int) string {
	text = strings.TrimSpace(text)
	if n <= 0 {
		return text
	}
	rs := []rune(text)
	if len(rs) <= n {
		return text
	}
	return string(rs[:n])
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a >= b {
		return a
	}
	return b
}

func minFloat(a, b float64) float64 {
	if a <= b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a <= b {
		return a
	}
	return b
}
