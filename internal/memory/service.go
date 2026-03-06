package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ai-gf/internal/memory/dedup"
	"ai-gf/internal/memory/extractor"
	"ai-gf/internal/memory/ranking"
	"ai-gf/internal/proactive"
	"ai-gf/internal/repo"
)

// savedEvent 表示本轮刚写入的真实 life_event，供主动提醒任务引用。
type savedEvent struct {
	ID        int64
	EventTime time.Time
}

// Service 是记忆系统门面：统一编排结构化记忆、语义记忆与抽取逻辑。
type Service struct {
	repo               repo.MemoryRepository
	embedder           Embedder
	proactiveScheduler proactive.Scheduler
	extractor          *extractor.Extractor
	ranker             *ranking.Ranker
	cfg                Config
}

// NewService 创建记忆服务实例。
func NewService(repository repo.MemoryRepository, embedder Embedder, proactiveScheduler proactive.Scheduler, cfg Config) *Service {
	cfg = cfg.Normalize()
	return &Service{
		repo:               repository,
		embedder:           embedder,
		proactiveScheduler: proactiveScheduler,
		extractor:          extractor.New(),
		ranker:             ranking.NewRanker(cfg.Ranking),
		cfg:                cfg,
	}
}

// BuildContext 在对话前构建可注入 PromptBuilder 的长记忆上下文。
func (s *Service) BuildContext(ctx context.Context, req ContextRequest) (ContextResult, error) {
	req.UserID = strings.TrimSpace(req.UserID)
	req.UserMessage = strings.TrimSpace(req.UserMessage)
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	if req.UserID == "" {
		return ContextResult{}, fmt.Errorf("memory context: user_id is required")
	}

	profile, err := s.repo.GetUserProfile(ctx, req.UserID)
	if err != nil {
		return ContextResult{}, err
	}
	preferences, err := s.repo.ListUserPreferences(ctx, req.UserID, s.cfg.PreferenceTopN)
	if err != nil {
		return ContextResult{}, err
	}
	boundaries, err := s.repo.ListUserBoundaries(ctx, req.UserID)
	if err != nil {
		return ContextResult{}, err
	}
	events, err := s.repo.ListUpcomingEvents(ctx, req.UserID, req.Now, s.cfg.EventWindowDays, 8)
	if err != nil {
		return ContextResult{}, err
	}
	events = filterUpcomingEventsByImportance(events, s.cfg.EventMinImportance)

	semanticChunks, _ := s.loadSemanticCandidates(ctx, req.UserID, req.UserMessage)

	initialCandidates := make([]ranking.Candidate, 0, len(preferences)+len(events)+len(semanticChunks))
	initialCandidates = append(initialCandidates, buildPreferenceCandidates(preferences)...)
	initialCandidates = append(initialCandidates, buildEventCandidates(events)...)
	initialCandidates = append(initialCandidates, buildSemanticCandidates(semanticChunks)...)

	boundaryKeywords := buildBoundaryKeywords(boundaries)
	rankResult := s.ranker.Rank(ctx, ranking.RankRequest{
		Now:               req.Now,
		UserMessage:       req.UserMessage,
		K:                 req.K,
		InitialCandidates: initialCandidates,
		BoundaryKeywords:  boundaryKeywords,
	})

	memoryIDs := make([]int64, 0, len(rankResult.Memories))
	for _, item := range rankResult.Memories {
		if item.Kind == ranking.CandidateSemantic && item.SourceID > 0 {
			memoryIDs = append(memoryIDs, item.SourceID)
		}
	}
	if len(memoryIDs) > 0 {
		_ = s.repo.TouchMemoryChunks(ctx, req.UserID, memoryIDs, req.Now)
	}

	return ContextResult{
		UserProfile:      formatProfile(profile),
		UserPreferences:  formatPreferences(preferences),
		UserBoundaries:   formatBoundaries(boundaries),
		ImportantEvents:  formatEvents(events),
		RelevantMemories: formatRankedMemories(rankResult.Memories),
		MemoryIDs:        memoryIDs,
		RankTrace:        rankResult.Trace,
	}, nil
}

// ProcessTurn 在一轮聊天结束后执行记忆抽取、去重与持久化。
func (s *Service) ProcessTurn(ctx context.Context, in TurnInput) error {
	in.UserID = strings.TrimSpace(in.UserID)
	in.UserMessage = strings.TrimSpace(in.UserMessage)
	in.ConversationContext = strings.TrimSpace(in.ConversationContext)
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	if in.UserID == "" || in.UserMessage == "" {
		return nil
	}

	// 优先用用户画像中的时区做事件时间解析，避免服务端时区污染。
	extractNow := in.Now
	if profile, err := s.repo.GetUserProfile(ctx, in.UserID); err == nil {
		if loc := resolveUserLocation(profile.Timezone); loc != nil {
			extractNow = in.Now.In(loc)
		}
	}

	extracted := s.extractor.Extract(ctx, extractor.Request{
		UserID:              in.UserID,
		UserMessage:         in.UserMessage,
		AssistantMessage:    in.AssistantMessage,
		ConversationContext: in.ConversationContext,
		Now:                 extractNow,
	})

	if err := s.persistFacts(ctx, in.UserID, extracted.Facts); err != nil {
		return err
	}
	if err := s.persistPreferences(ctx, in.UserID, extracted.Preferences); err != nil {
		return err
	}
	if err := s.persistBoundaries(ctx, in.UserID, extracted.Boundaries); err != nil {
		return err
	}
	if err := s.persistEvents(ctx, in.UserID, in.UserMessageID, extracted.Events); err != nil {
		return err
	}
	if err := s.persistSemanticMemories(ctx, in.UserID, extracted); err != nil {
		return err
	}
	if err := s.executePlannedActions(ctx, in.UserID, in.SessionID, in.UserMessageID, in.Now, in.PlannedActions); err != nil {
		return err
	}
	return nil
}

// executePlannedActions 执行 CE 输出的动作计划。
// 原则：动作执行失败不影响已完成的抽取写入；仅返回首个错误给 worker 记录。
func (s *Service) executePlannedActions(ctx context.Context, userID, sessionID string, sourceMessageID int64, now time.Time, actions []TurnAction) error {
	if len(actions) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}

	eventTimes := map[string]savedEvent{}
	seen := map[string]struct{}{}

	for _, action := range actions {
		actionType := strings.ToUpper(strings.TrimSpace(action.Type))
		if actionType == "" {
			continue
		}
		key := actionType + "|" + normalizeActionParams(action.Params)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		switch actionType {
		case "SAVE_PREFERENCE":
			if err := s.executeSavePreference(ctx, userID, action.Params); err != nil {
				return err
			}
		case "SAVE_BOUNDARY":
			if err := s.executeSaveBoundary(ctx, userID, action.Params); err != nil {
				return err
			}
		case "SAVE_EVENT":
			eventID, eventTime, err := s.executeSaveEvent(ctx, userID, sourceMessageID, now, action.Params)
			if err != nil {
				return err
			}
			title := strings.TrimSpace(action.Params["title"])
			if title != "" && !eventTime.IsZero() {
				eventTimes[title] = savedEvent{
					ID:        eventID,
					EventTime: eventTime,
				}
			}
		case "SAVE_SEMANTIC_MEMORY":
			if err := s.executeSaveSemanticMemory(ctx, userID, action.Params, "conversation_plan_action", 3, 0.85, false); err != nil {
				return err
			}
		case "SCHEDULE_EVENT_REMINDER":
			if err := s.executeScheduleEventReminder(ctx, userID, sessionID, sourceMessageID, now, action.Params, action.Reason, eventTimes); err != nil {
				return err
			}
		case "SCHEDULE_CARE_FOLLOWUP":
			if err := s.executeScheduleCareFollowup(ctx, userID, sessionID, sourceMessageID, now, action.Params, action.Reason); err != nil {
				return err
			}
		default:
			// 未识别动作静默跳过，保持向前兼容。
			continue
		}
	}
	return nil
}

// executeSavePreference 执行偏好写入动作。
func (s *Service) executeSavePreference(ctx context.Context, userID string, params map[string]string) error {
	category := strings.TrimSpace(params["category"])
	value := strings.TrimSpace(params["value"])
	if value == "" {
		return nil
	}
	if category == "" {
		category = "general"
	}
	if _, err := s.repo.UpsertUserPreference(ctx, repo.UserPreference{
		UserID:     userID,
		Category:   category,
		Value:      value,
		Confidence: 0.82,
	}); err != nil {
		return err
	}
	return nil
}

// executeSaveBoundary 执行边界写入动作。
func (s *Service) executeSaveBoundary(ctx context.Context, userID string, params map[string]string) error {
	topic := strings.TrimSpace(params["topic"])
	if topic == "" {
		return nil
	}
	description := strings.TrimSpace(params["description"])
	if _, err := s.repo.UpsertUserBoundary(ctx, repo.UserBoundary{
		UserID:      userID,
		Topic:       topic,
		Description: description,
	}); err != nil {
		return err
	}
	return nil
}

// executeSaveEvent 执行真实 life_event 写入，并返回事件主键与时间。
func (s *Service) executeSaveEvent(ctx context.Context, userID string, sourceMessageID int64, now time.Time, params map[string]string) (int64, time.Time, error) {
	title := strings.TrimSpace(params["title"])
	if title == "" {
		title = "用户提到未来安排"
	}
	timeHint := strings.TrimSpace(params["time_hint"])
	eventTime := parseEventTimeWithHint(now, timeHint)
	if eventTime.IsZero() {
		eventTime = now.Add(24 * time.Hour)
	}
	sourceID := sourceMessageID
	eventID, err := s.repo.UpsertLifeEvent(ctx, repo.LifeEvent{
		UserID:          userID,
		Title:           title,
		EventTime:       eventTime,
		Importance:      4,
		SourceMessageID: &sourceID,
	})
	if err != nil {
		return 0, time.Time{}, err
	}
	return eventID, eventTime, nil
}

// executeSaveSemanticMemory 执行语义记忆写入动作（含 embedding 与去重）。
func (s *Service) executeSaveSemanticMemory(
	ctx context.Context,
	userID string,
	params map[string]string,
	source string,
	importance int,
	confidence float64,
	pinned bool,
) error {
	content := strings.TrimSpace(params["sentence"])
	if content == "" {
		content = strings.TrimSpace(params["content"])
	}
	if content == "" {
		return nil
	}

	chunk := repo.MemoryChunk{
		UserID:       userID,
		Content:      content,
		ContentShort: trimSemanticShort(content),
		Topic:        strings.TrimSpace(params["topic"]),
		MemoryType:   "ce_action",
		Importance:   clampInt(importance, 1, 5),
		Confidence:   safeConfidence(confidence),
		Pinned:       pinned,
		Metadata: map[string]any{
			"source": source,
		},
	}
	if chunk.Topic == "" {
		chunk.Topic = "general"
	}

	if s.embedder != nil {
		emb, err := s.embedder.Embed(ctx, chunk.Content)
		if err == nil && len(emb) > 0 {
			chunk.Embedding = emb
			if similar, err := s.repo.FindSimilarMemoryChunk(ctx, userID, emb, s.cfg.Ranking.DedupThreshold); err == nil && similar != nil {
				_ = s.repo.TouchMemoryChunks(ctx, userID, []int64{similar.ID}, time.Now())
				return nil
			}
		}
	}
	if _, err := s.repo.UpsertMemoryChunk(ctx, chunk); err != nil {
		return err
	}
	return nil
}

// executeScheduleEventReminder 执行事件提醒动作（落库为事件，供后续调度器消费）。
func (s *Service) executeScheduleEventReminder(
	ctx context.Context,
	userID string,
	sessionID string,
	sourceMessageID int64,
	now time.Time,
	params map[string]string,
	reason string,
	eventTimes map[string]savedEvent,
) error {
	if s.proactiveScheduler == nil {
		return nil
	}
	title := strings.TrimSpace(params["title"])
	if title == "" {
		return nil
	}
	baseEvent := eventTimes[title]
	baseTime := baseEvent.EventTime
	if baseTime.IsZero() {
		baseTime = parseEventTimeWithHint(now, strings.TrimSpace(params["time_hint"]))
	}
	if baseTime.IsZero() {
		return nil
	}
	offset := parseHourOrDayOffset(strings.TrimSpace(params["offset"]))
	remindAt := baseTime.Add(offset)
	sourceID := sourceMessageID
	payload := map[string]any{
		"event_title":  title,
		"target_title": title,
		"offset":       strings.TrimSpace(params["offset"]),
		"time_hint":    strings.TrimSpace(params["time_hint"]),
	}
	var sourceEventID *int64
	if baseEvent.ID > 0 {
		sourceEventID = &baseEvent.ID
		payload["source_life_event_id"] = baseEvent.ID
	}
	return s.proactiveScheduler.Schedule(ctx, proactive.ScheduleRequest{
		UserID:            userID,
		SessionID:         sessionID,
		TaskType:          "event_reminder",
		Reason:            strings.TrimSpace(reason),
		SourceMessageID:   &sourceID,
		SourceLifeEventID: sourceEventID,
		RunAt:             remindAt,
		CooldownSeconds:   12 * 60 * 60,
		Payload:           payload,
	})
}

// executeScheduleCareFollowup 执行情绪回访动作，真正写入 proactive_tasks。
func (s *Service) executeScheduleCareFollowup(
	ctx context.Context,
	userID string,
	sessionID string,
	sourceMessageID int64,
	now time.Time,
	params map[string]string,
	reason string,
) error {
	if s.proactiveScheduler == nil {
		return nil
	}
	offset := parseHourOrDayOffset(strings.TrimSpace(params["offset"]))
	if offset == 0 {
		offset = 24 * time.Hour
	}
	followAt := now.Add(offset)
	sourceID := sourceMessageID
	return s.proactiveScheduler.Schedule(ctx, proactive.ScheduleRequest{
		UserID:          userID,
		SessionID:       sessionID,
		TaskType:        "care_followup",
		Reason:          strings.TrimSpace(reason),
		SourceMessageID: &sourceID,
		RunAt:           followAt,
		CooldownSeconds: 24 * 60 * 60,
		Payload: map[string]any{
			"offset": strings.TrimSpace(params["offset"]),
		},
	})
}

// parseEventTimeWithHint 使用已有 extractor 规则解析时间线索。
func parseEventTimeWithHint(now time.Time, hint string) time.Time {
	hint = strings.TrimSpace(hint)
	if hint == "" {
		return time.Time{}
	}
	ex := extractor.New()
	out := ex.Extract(context.Background(), extractor.Request{
		UserMessage: hint,
		Now:         now,
	})
	if len(out.Events) == 0 {
		return time.Time{}
	}
	return out.Events[0].EventTime
}

// parseHourOrDayOffset 解析如 -2h / 24h / -1d 的偏移；无法解析则返回 0。
func parseHourOrDayOffset(raw string) time.Duration {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0
	}
	sign := 1
	if strings.HasPrefix(raw, "-") {
		sign = -1
		raw = strings.TrimPrefix(raw, "-")
	} else if strings.HasPrefix(raw, "+") {
		raw = strings.TrimPrefix(raw, "+")
	}

	if strings.HasSuffix(raw, "h") {
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "h"))
		if err != nil {
			return 0
		}
		return time.Duration(sign*n) * time.Hour
	}
	if strings.HasSuffix(raw, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0
		}
		return time.Duration(sign*n) * 24 * time.Hour
	}
	return 0
}

// normalizeActionParams 把动作参数转成稳定字符串，便于去重。
func normalizeActionParams(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, strings.TrimSpace(k)+"="+strings.TrimSpace(params[k]))
	}
	return strings.Join(parts, "|")
}

// trimSemanticShort 生成 memory chunk 的短文本键，避免过长内容影响唯一键质量。
func trimSemanticShort(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	r := []rune(text)
	if len(r) <= 120 {
		return text
	}
	return string(r[:120])
}

func (s *Service) loadSemanticCandidates(ctx context.Context, userID, userMessage string) ([]repo.MemoryChunk, error) {
	var queryEmbedding []float32
	if s.embedder != nil && strings.TrimSpace(userMessage) != "" {
		emb, err := s.embedder.Embed(ctx, userMessage)
		if err == nil && len(emb) > 0 {
			queryEmbedding = emb
		}
	}

	if len(queryEmbedding) > 0 {
		items, err := s.repo.SearchMemoryChunks(ctx, userID, queryEmbedding, s.cfg.Ranking.InitialTopK)
		if err == nil && len(items) > 0 {
			return items, nil
		}
	}
	return s.repo.ListRecentMemoryChunks(ctx, userID, s.cfg.Ranking.InitialTopK)
}

func buildPreferenceCandidates(items []repo.UserPreference) []ranking.Candidate {
	out := make([]ranking.Candidate, 0, len(items))
	for _, item := range items {
		content := "用户偏好：" + strings.TrimSpace(item.Category) + "=" + strings.TrimSpace(item.Value)
		out = append(out, ranking.Candidate{
			ID:           fmt.Sprintf("pref_%d", item.ID),
			SourceID:     item.ID,
			Kind:         ranking.CandidateStructured,
			Topic:        strings.TrimSpace(item.Category),
			Content:      content,
			ContentShort: content,
			Similarity:   0,
			Importance:   3,
			Confidence:   safeConfidence(item.Confidence),
			Pinned:       false,
			AccessCount:  1,
			CreatedAt:    item.CreatedAt,
			LastUsedAt:   item.LastUsedAt,
		})
	}
	return out
}

func buildEventCandidates(items []repo.LifeEvent) []ranking.Candidate {
	out := make([]ranking.Candidate, 0, len(items))
	for _, item := range items {
		content := "用户事件：" + item.Title + " @ " + item.EventTime.Format("2006-01-02 15:04")
		out = append(out, ranking.Candidate{
			ID:           fmt.Sprintf("event_%d", item.ID),
			SourceID:     item.ID,
			Kind:         ranking.CandidateStructured,
			Topic:        "event",
			Content:      content,
			ContentShort: content,
			Importance:   clampInt(item.Importance, 1, 5),
			Confidence:   0.8,
			Pinned:       item.Importance >= 4,
			AccessCount:  1,
			CreatedAt:    item.CreatedAt,
		})
	}
	return out
}

func buildSemanticCandidates(items []repo.MemoryChunk) []ranking.Candidate {
	out := make([]ranking.Candidate, 0, len(items))
	for _, item := range items {
		out = append(out, ranking.Candidate{
			ID:           fmt.Sprintf("chunk_%d", item.ID),
			SourceID:     item.ID,
			Kind:         ranking.CandidateSemantic,
			Topic:        strings.TrimSpace(item.Topic),
			Content:      item.Content,
			ContentShort: item.ContentShort,
			Similarity:   safeConfidence(item.Similarity),
			Importance:   clampInt(item.Importance, 1, 5),
			Confidence:   safeConfidence(item.Confidence),
			Pinned:       item.Pinned,
			AccessCount:  item.AccessCount,
			CreatedAt:    item.CreatedAt,
			LastUsedAt:   item.LastUsedAt,
			Superseded:   item.Superseded,
		})
	}
	return out
}

func buildBoundaryKeywords(items []repo.UserBoundary) []string {
	out := make([]string, 0, len(items)*2)
	seen := map[string]struct{}{}
	for _, item := range items {
		for _, raw := range []string{item.Topic, item.Description} {
			raw = strings.TrimSpace(strings.ToLower(raw))
			if raw == "" {
				continue
			}
			if _, ok := seen[raw]; ok {
				continue
			}
			seen[raw] = struct{}{}
			out = append(out, raw)
		}
	}
	return out
}

func formatProfile(profile repo.UserProfile) string {
	lines := make([]string, 0, 4)
	if strings.TrimSpace(profile.Nickname) != "" {
		lines = append(lines, "昵称："+strings.TrimSpace(profile.Nickname))
	}
	if strings.TrimSpace(profile.Occupation) != "" {
		lines = append(lines, "职业："+strings.TrimSpace(profile.Occupation))
	}
	if strings.TrimSpace(profile.Timezone) != "" {
		lines = append(lines, "时区："+strings.TrimSpace(profile.Timezone))
	}
	if profile.Birthday != nil {
		lines = append(lines, "生日："+profile.Birthday.Format("2006-01-02"))
	}
	return joinOrNone(lines)
}

func formatPreferences(items []repo.UserPreference) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := fmt.Sprintf("- %s: %s (conf: %.2f)", strings.TrimSpace(item.Category), strings.TrimSpace(item.Value), safeConfidence(item.Confidence))
		lines = append(lines, line)
	}
	return joinOrNone(lines)
}

func formatBoundaries(items []repo.UserBoundary) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := "- " + strings.TrimSpace(item.Topic)
		if strings.TrimSpace(item.Description) != "" {
			line += "：" + strings.TrimSpace(item.Description)
		}
		lines = append(lines, line)
	}
	return joinOrNone(lines)
}

func formatEvents(items []repo.LifeEvent) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		line := fmt.Sprintf("- %s %s (importance=%d)", item.EventTime.Format("01-02 15:04"), strings.TrimSpace(item.Title), clampInt(item.Importance, 1, 5))
		lines = append(lines, line)
	}
	return joinOrNone(lines)
}

func formatRankedMemories(items []ranking.MemoryItem) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		id := item.ID
		if item.SourceID > 0 {
			id = fmt.Sprintf("m_%d", item.SourceID)
		}
		topic := strings.TrimSpace(item.Topic)
		if topic == "" {
			topic = "general"
		}
		line := fmt.Sprintf("- [id:%s conf:%.2f topic:%s] %s", id, safeConfidence(item.Confidence), topic, strings.TrimSpace(item.Content))
		lines = append(lines, line)
	}
	return joinOrNone(lines)
}

func joinOrNone(lines []string) string {
	if len(lines) == 0 {
		return "暂无"
	}
	return strings.Join(lines, "\n")
}

func (s *Service) persistFacts(ctx context.Context, userID string, items []extractor.FactMemory) error {
	if len(items) == 0 {
		return nil
	}
	profile, err := s.repo.GetUserProfile(ctx, userID)
	if err != nil {
		return err
	}
	profile.UserID = userID

	changed := false
	for _, item := range items {
		if item.Confidence < s.cfg.ExtractorMinConfidence || item.Importance < s.cfg.ExtractorMinImportance {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(item.Key)) {
		case "nickname":
			if strings.TrimSpace(item.Value) != "" {
				profile.Nickname = strings.TrimSpace(item.Value)
				changed = true
			}
		case "occupation":
			if strings.TrimSpace(item.Value) != "" {
				profile.Occupation = strings.TrimSpace(item.Value)
				changed = true
			}
		case "timezone":
			if strings.TrimSpace(item.Value) != "" {
				profile.Timezone = strings.TrimSpace(item.Value)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return s.repo.UpsertUserProfile(ctx, profile)
}

func (s *Service) persistPreferences(ctx context.Context, userID string, items []extractor.PreferenceMemory) error {
	for _, item := range items {
		if item.Confidence < s.cfg.ExtractorMinConfidence || item.Importance < s.cfg.ExtractorMinImportance {
			continue
		}
		keyword := normalizeConflictKeyword(item.Value)
		if keyword != "" {
			_, _ = s.repo.DeleteUserBoundariesByKeyword(ctx, userID, keyword)
			_, _ = s.repo.SupersedeMemoryChunksByKeyword(ctx, userID, "boundary", keyword)
		}
		if _, err := s.repo.UpsertUserPreference(ctx, repo.UserPreference{
			UserID:     userID,
			Category:   item.Category,
			Value:      item.Value,
			Confidence: item.Confidence,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistBoundaries(ctx context.Context, userID string, items []extractor.BoundaryMemory) error {
	for _, item := range items {
		if item.Confidence < s.cfg.ExtractorMinConfidence || item.Importance < s.cfg.ExtractorMinImportance {
			continue
		}
		keyword := normalizeConflictKeyword(item.Description)
		if keyword == "" {
			keyword = normalizeConflictKeyword(item.Topic)
		}
		if keyword != "" {
			_, _ = s.repo.DeleteUserPreferencesByKeyword(ctx, userID, keyword)
			_, _ = s.repo.SupersedeMemoryChunksByKeyword(ctx, userID, "preference", keyword)
		}
		if _, err := s.repo.UpsertUserBoundary(ctx, repo.UserBoundary{
			UserID:      userID,
			Topic:       item.Topic,
			Description: item.Description,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistEvents(ctx context.Context, userID string, sourceMessageID int64, items []extractor.EventMemory) error {
	for _, item := range items {
		if item.Confidence < s.cfg.ExtractorMinConfidence || item.Importance < s.cfg.ExtractorMinImportance {
			continue
		}
		sourceID := sourceMessageID
		if _, err := s.repo.UpsertLifeEvent(ctx, repo.LifeEvent{
			UserID:          userID,
			Title:           item.Title,
			EventTime:       item.EventTime,
			Importance:      item.Importance,
			SourceMessageID: &sourceID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) persistSemanticMemories(ctx context.Context, userID string, extracted extractor.Result) error {
	lines := buildSemanticEntries(extracted)
	seen := map[string]struct{}{}
	for _, line := range lines {
		normalized := dedup.SemanticKey(line.Line)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}

		chunk := repo.MemoryChunk{
			UserID:       userID,
			Content:      strings.TrimSpace(line.Line),
			ContentShort: strings.TrimSpace(line.Line),
			Topic:        line.Topic,
			MemoryType:   line.MemoryType,
			Importance:   clampInt(line.Importance, 1, 5),
			Confidence:   safeConfidence(line.Confidence),
			Pinned:       line.Pinned,
			Metadata: map[string]any{
				"source":     "memory_extractor",
				"importance": clampInt(line.Importance, 1, 5),
				"confidence": safeConfidence(line.Confidence),
			},
		}

		if s.embedder != nil {
			emb, err := s.embedder.Embed(ctx, chunk.Content)
			if err == nil && len(emb) > 0 {
				chunk.Embedding = emb
				similar, err := s.repo.FindSimilarMemoryChunk(ctx, userID, emb, s.cfg.Ranking.DedupThreshold)
				if err == nil && similar != nil {
					_ = s.repo.TouchMemoryChunks(ctx, userID, []int64{similar.ID}, time.Now())
					continue
				}
			}
		}

		if _, err := s.repo.UpsertMemoryChunk(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func filterUpcomingEventsByImportance(items []repo.LifeEvent, minImportance int) []repo.LifeEvent {
	if len(items) == 0 {
		return nil
	}
	if minImportance <= 1 {
		return items
	}
	out := make([]repo.LifeEvent, 0, len(items))
	for _, item := range items {
		if clampInt(item.Importance, 1, 5) < minImportance {
			continue
		}
		out = append(out, item)
	}
	return out
}

func resolveUserLocation(raw string) *time.Location {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	alias := map[string]string{
		"北京时间": "Asia/Shanghai",
		"上海时间": "Asia/Shanghai",
		"东京时间": "Asia/Tokyo",
		"纽约时间": "America/New_York",
	}
	if v, ok := alias[raw]; ok {
		raw = v
	}
	if loc, err := time.LoadLocation(raw); err == nil {
		return loc
	}
	if strings.HasPrefix(strings.ToUpper(raw), "UTC") {
		// 兼容 UTC+8 / UTC-5 这类偏移表达。
		offset := strings.TrimPrefix(strings.ToUpper(raw), "UTC")
		if len(offset) >= 2 {
			sign := 1
			if strings.HasPrefix(offset, "-") {
				sign = -1
			}
			offset = strings.TrimPrefix(strings.TrimPrefix(offset, "+"), "-")
			hour := parseHour(offset)
			if hour >= 0 && hour <= 14 {
				return time.FixedZone(raw, sign*hour*3600)
			}
		}
	}
	return nil
}

func parseHour(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return -1
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return -1
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func normalizeConflictKeyword(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "尽量避免：")
	v = strings.TrimPrefix(v, "尽量避免:")
	v = strings.TrimPrefix(v, "避免：")
	v = strings.TrimPrefix(v, "避免:")
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	stopMarks := []string{"。", "，", ",", "；", ";", "！", "!", "？", "?", "但", "不过"}
	for _, mark := range stopMarks {
		if idx := strings.Index(v, mark); idx >= 0 {
			v = strings.TrimSpace(v[:idx])
		}
	}
	return strings.TrimSpace(v)
}

type semanticEntry struct {
	Line       string
	Topic      string
	MemoryType string
	Importance int
	Confidence float64
	Pinned     bool
}

func buildSemanticEntries(extracted extractor.Result) []semanticEntry {
	out := make([]semanticEntry, 0, len(extracted.Preferences)+len(extracted.Boundaries)+len(extracted.Events)+len(extracted.Facts))
	for _, p := range extracted.Preferences {
		line := "用户偏好：" + strings.TrimSpace(p.Category) + "=" + strings.TrimSpace(p.Value)
		out = append(out, semanticEntry{
			Line:       line,
			Topic:      "preference",
			MemoryType: "preference",
			Importance: p.Importance,
			Confidence: p.Confidence,
			Pinned:     false,
		})
	}
	for _, b := range extracted.Boundaries {
		line := "用户边界：" + strings.TrimSpace(b.Topic)
		if strings.TrimSpace(b.Description) != "" {
			line += "（" + strings.TrimSpace(b.Description) + "）"
		}
		out = append(out, semanticEntry{
			Line:       line,
			Topic:      "boundary",
			MemoryType: "boundary",
			Importance: maxInt(4, b.Importance),
			Confidence: b.Confidence,
			Pinned:     true,
		})
	}
	for _, ev := range extracted.Events {
		line := "用户事件：" + strings.TrimSpace(ev.Title) + " @ " + ev.EventTime.Format("2006-01-02 15:04")
		out = append(out, semanticEntry{
			Line:       line,
			Topic:      "event",
			MemoryType: "event",
			Importance: ev.Importance,
			Confidence: ev.Confidence,
			Pinned:     ev.Importance >= 4,
		})
	}
	for _, f := range extracted.Facts {
		line := "用户事实：" + strings.TrimSpace(f.Key) + "=" + strings.TrimSpace(f.Value)
		out = append(out, semanticEntry{
			Line:       line,
			Topic:      "fact",
			MemoryType: "fact",
			Importance: f.Importance,
			Confidence: f.Confidence,
			Pinned:     f.Importance >= 4,
		})
	}
	return out
}

func inferSemanticTopic(line string) string {
	switch {
	case strings.Contains(line, "用户偏好"):
		return "preference"
	case strings.Contains(line, "用户边界"):
		return "boundary"
	case strings.Contains(line, "用户事件"):
		return "event"
	case strings.Contains(line, "用户事实"):
		return "fact"
	default:
		return "general"
	}
}

func inferSemanticType(line string) string {
	switch {
	case strings.Contains(line, "用户偏好"):
		return "preference"
	case strings.Contains(line, "用户边界"):
		return "boundary"
	case strings.Contains(line, "用户事件"):
		return "event"
	case strings.Contains(line, "用户事实"):
		return "fact"
	default:
		return "semantic"
	}
}

func safeConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func maxInt(a, b int) int {
	if a >= b {
		return a
	}
	return b
}
