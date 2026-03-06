package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ai-gf/internal/chat"
	"ai-gf/internal/proactive"
	"ai-gf/internal/prompt"
	"ai-gf/internal/repo"

	"github.com/gofiber/fiber/v2"
)

// ChatHandler 负责 /chat 与 /chat/debug 的 HTTP 接入层。
type ChatHandler struct {
	chatService   *chat.Service
	outboxRepo    *repo.PGChatOutboxRepository
	proactiveRepo *repo.PGProactiveRepository
}

// NewChatHandler 创建聊天接口处理器。
func NewChatHandler(chatService *chat.Service, outboxRepo *repo.PGChatOutboxRepository, proactiveRepo *repo.PGProactiveRepository) *ChatHandler {
	return &ChatHandler{
		chatService:   chatService,
		outboxRepo:    outboxRepo,
		proactiveRepo: proactiveRepo,
	}
}

// chatRequest 是统一请求体，正式聊天与 debug 共享该结构。
type chatRequest struct {
	UserID      string       `json:"user_id"`
	SessionID   string       `json:"session_id"`
	Message     string       `json:"message"`
	Temperature *float64     `json:"temperature,omitempty"`
	ModelID     string       `json:"model_id,omitempty"`
	Options     optionsInput `json:"options,omitempty"`
	Persona     personaInput `json:"persona,omitempty"`
}

// personaInput 承接前端传入的人设上下文字段。
type personaInput struct {
	BotName            string   `json:"bot_name"`
	UserName           string   `json:"user_name"`
	RelationshipStage  string   `json:"relationship_stage"`
	Emotion            string   `json:"emotion"`
	EmotionIntensity   *float64 `json:"emotion_intensity"`
	UserProfile        string   `json:"user_profile"`
	UserPreferences    string   `json:"user_preferences"`
	UserBoundaries     string   `json:"user_boundaries"`
	ImportantEvents    string   `json:"important_events"`
	RelevantMemories   string   `json:"relevant_memories"`
	RecentConversation string   `json:"recent_conversation"`
	Language           string   `json:"language"`

	CharacterName string `json:"character_name"`
	UserNickname  string `json:"user_nickname"`
}

// optionsInput 控制 Prompt Builder 的可选模块开关。
type optionsInput struct {
	EnableRAG     *bool `json:"enable_rag,omitempty"`
	EnableEmotion *bool `json:"enable_emotion,omitempty"`
	EnableEvents  *bool `json:"enable_events,omitempty"`
}

// proactiveStateRequest 用于更新主动消息设置。
type proactiveStateRequest struct {
	UserID            string `json:"user_id"`
	Enabled           *bool  `json:"enabled,omitempty"`
	QuietHoursEnabled *bool  `json:"quiet_hours_enabled,omitempty"`
	QuietStartMinute  *int   `json:"quiet_start_minute,omitempty"`
	QuietEndMinute    *int   `json:"quiet_end_minute,omitempty"`
	Timezone          string `json:"timezone,omitempty"`
	CooldownSeconds   *int   `json:"cooldown_seconds,omitempty"`
}

// Register 注册聊天相关路由。
func (h *ChatHandler) Register(app *fiber.App) {
	app.Post("/chat", h.Chat)
	app.Post("/chat/debug", h.ChatDebug)
	app.Get("/chat/outbox/pull", h.PullOutbox)
	app.Get("/chat/outbox/stream", h.StreamOutbox)
	app.Get("/chat/proactive/state", h.GetProactiveState)
	app.Post("/chat/proactive/state", h.UpdateProactiveState)
	app.Get("/chat/proactive/debug", h.DebugProactive)
}

// Chat 处理实时聊天请求，返回 SSE 流式增量内容。
func (h *ChatHandler) Chat(c *fiber.Ctx) error {
	var req chatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.Message) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user_id and message are required",
		})
	}

	persona := buildPersona(req.Persona)

	c.Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Status(fiber.StatusOK)

	// Fiber 的流式写回调中执行实际聊天流程。
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ctx := context.Background()

		result, err := h.chatService.StreamChat(ctx, chat.StreamChatInput{
			UserID:      req.UserID,
			SessionID:   req.SessionID,
			Message:     req.Message,
			Temperature: req.Temperature,
			Persona:     persona,
		}, func(token string) error {
			return writeSSE(w, "token", fiber.Map{"content": token})
		})
		if err != nil {
			_ = writeSSE(w, "error", fiber.Map{"error": err.Error()})
			return
		}

		_ = writeSSE(w, "done", fiber.Map{
			"assistant_message_id": result.AssistantMessageID,
		})
	})

	return nil
}

// ChatDebug 仅构建 prompt，不调用模型，便于观察预算和裁剪行为。
func (h *ChatHandler) ChatDebug(c *fiber.Ctx) error {
	var req chatRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if strings.TrimSpace(req.UserID) == "" || strings.TrimSpace(req.Message) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user_id and message are required",
		})
	}

	persona := buildPersona(req.Persona)
	options := buildOptions(req.Options)

	result, err := h.chatService.BuildPromptDebug(context.Background(), chat.DebugPromptInput{
		UserID:    req.UserID,
		SessionID: req.SessionID,
		Message:   req.Message,
		Persona:   persona,
		ModelID:   req.ModelID,
		Options:   options,
	})
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.JSON(fiber.Map{
		"messages":            result.Messages,
		"trace":               result.Trace,
		"report":              result.Report,
		"conversation_plan":   result.ConversationPlan,
		"history_count":       result.HistoryCount,
		"memory_context":      result.MemoryContext,
		"memory_context_err":  result.MemoryContextErr,
		"persona_after_merge": result.PersonaAfterMerge,
	})
}

// PullOutbox 轮询拉取主动消息 outbox，供前端自动渲染“AI 先说话”。
func (h *ChatHandler) PullOutbox(c *fiber.Ctx) error {
	if h.outboxRepo == nil {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error": "outbox is not configured",
		})
	}

	userID := strings.TrimSpace(c.Query("user_id"))
	if userID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user_id is required",
		})
	}
	sessionID := strings.TrimSpace(c.Query("session_id"))
	if sessionID == "" {
		sessionID = "default"
	}

	afterID := int64(0)
	if rawAfter := strings.TrimSpace(c.Query("after_id")); rawAfter != "" {
		parsed, err := strconv.ParseInt(rawAfter, 10, 64)
		if err != nil || parsed < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "after_id must be non-negative integer",
			})
		}
		afterID = parsed
	}

	limit := 20
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "limit must be positive integer",
			})
		}
		if parsed > 100 {
			parsed = 100
		}
		limit = parsed
	}

	items, err := h.outboxRepo.PullAfter(c.Context(), userID, sessionID, afterID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	return c.JSON(fiber.Map{
		"items": items,
		"count": len(items),
	})
}

// StreamOutbox 通过 SSE 持续推送主动消息；浏览器断线后可借助 Last-Event-ID 自动续传。
func (h *ChatHandler) StreamOutbox(c *fiber.Ctx) error {
	if h.outboxRepo == nil {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error": "outbox is not configured",
		})
	}

	userID := strings.TrimSpace(c.Query("user_id"))
	if userID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user_id is required",
		})
	}
	sessionID := strings.TrimSpace(c.Query("session_id"))
	if sessionID == "" {
		sessionID = "default"
	}

	afterID := int64(0)
	if rawAfter := strings.TrimSpace(c.Query("after_id")); rawAfter != "" {
		parsed, err := strconv.ParseInt(rawAfter, 10, 64)
		if err != nil || parsed < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "after_id must be non-negative integer",
			})
		}
		afterID = parsed
	} else if lastEventID := strings.TrimSpace(c.Get("Last-Event-ID")); lastEventID != "" {
		if parsed, err := strconv.ParseInt(lastEventID, 10, 64); err == nil && parsed > 0 {
			afterID = parsed
		}
	}

	pollIntervalMS := 2000
	if raw := strings.TrimSpace(c.Query("poll_interval_ms")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "poll_interval_ms must be positive integer",
			})
		}
		if parsed < 500 {
			parsed = 500
		}
		if parsed > 60000 {
			parsed = 60000
		}
		pollIntervalMS = parsed
	}

	c.Set("Content-Type", "text/event-stream; charset=utf-8")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")
	c.Status(fiber.StatusOK)

	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		ctx := context.Background()
		currentAfterID := afterID
		pollTicker := time.NewTicker(time.Duration(pollIntervalMS) * time.Millisecond)
		heartbeatTicker := time.NewTicker(15 * time.Second)
		defer pollTicker.Stop()
		defer heartbeatTicker.Stop()

		// 建链后先发一次就绪事件，便于前端感知连接状态。
		if err := writeSSE(w, "ready", fiber.Map{
			"user_id":    userID,
			"session_id": sessionID,
			"after_id":   currentAfterID,
		}); err != nil {
			return
		}

		for {
			items, err := h.outboxRepo.PullAfter(ctx, userID, sessionID, currentAfterID, 50)
			if err != nil {
				_ = writeSSE(w, "stream_error", fiber.Map{"error": err.Error()})
				return
			}
			for _, item := range items {
				if err := writeSSEWithID(w, "outbox", item.ID, item); err != nil {
					return
				}
				if item.ID > currentAfterID {
					currentAfterID = item.ID
				}
			}

			select {
			case <-pollTicker.C:
			case <-heartbeatTicker.C:
				if err := writeSSEComment(w, "ping"); err != nil {
					return
				}
			}
		}
	})

	return nil
}

// GetProactiveState 返回用户当前主动消息设置。
func (h *ChatHandler) GetProactiveState(c *fiber.Ctx) error {
	if h.proactiveRepo == nil {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error": "proactive repo is not configured",
		})
	}

	userID := strings.TrimSpace(c.Query("user_id"))
	if userID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user_id is required",
		})
	}

	state, err := h.proactiveRepo.GetState(c.Context(), userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	return c.JSON(fiber.Map{
		"state": state,
	})
}

// UpdateProactiveState 更新用户主动消息开关、免打扰和冷却时间。
func (h *ChatHandler) UpdateProactiveState(c *fiber.Ctx) error {
	if h.proactiveRepo == nil {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error": "proactive repo is not configured",
		})
	}

	var req proactiveStateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if req.UserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user_id is required",
		})
	}

	state, err := h.proactiveRepo.GetState(c.Context(), req.UserID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	if req.Enabled != nil {
		state.Enabled = *req.Enabled
	}
	if req.QuietHoursEnabled != nil {
		state.QuietHoursEnabled = *req.QuietHoursEnabled
	}
	if req.QuietStartMinute != nil {
		state.QuietStartMinute = *req.QuietStartMinute
	}
	if req.QuietEndMinute != nil {
		state.QuietEndMinute = *req.QuietEndMinute
	}
	if strings.TrimSpace(req.Timezone) != "" {
		state.Timezone = strings.TrimSpace(req.Timezone)
	}
	if req.CooldownSeconds != nil {
		state.CooldownSeconds = *req.CooldownSeconds
	}

	if err := h.proactiveRepo.UpsertState(c.Context(), state); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	return c.JSON(fiber.Map{
		"state": state,
	})
}

// DebugProactive 返回主动系统状态、任务和发送队列，便于排查运行时问题。
func (h *ChatHandler) DebugProactive(c *fiber.Ctx) error {
	if h.proactiveRepo == nil {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error": "proactive repo is not configured",
		})
	}

	userID := strings.TrimSpace(c.Query("user_id"))
	if userID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user_id is required",
		})
	}
	sessionID := strings.TrimSpace(c.Query("session_id"))
	limit := 20
	if rawLimit := strings.TrimSpace(c.Query("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "limit must be positive integer",
			})
		}
		if parsed > 100 {
			parsed = 100
		}
		limit = parsed
	}

	state, err := h.proactiveRepo.GetState(c.Context(), userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	tasks, err := h.proactiveRepo.ListTasks(c.Context(), userID, sessionID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}
	outbound, err := h.proactiveRepo.ListOutbound(c.Context(), userID, sessionID, limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	decisionEngine := proactive.NewDecisionEngine(proactive.DefaultDecisionConfig())
	taskDecisions := make([]fiber.Map, 0, len(tasks))
	now := time.Now()
	for _, task := range tasks {
		decision := decisionEngine.Evaluate(task, state, now)
		taskDecisions = append(taskDecisions, fiber.Map{
			"task_id":   task.ID,
			"status":    task.Status,
			"task_type": task.TaskType,
			"decision":  decision,
		})
	}

	return c.JSON(fiber.Map{
		"state":          state,
		"tasks":          tasks,
		"outbound_queue": outbound,
		"task_decisions": taskDecisions,
	})
}

// buildPersona 将 HTTP 入参映射到领域层 PersonaConfig，并补全别名字段。
func buildPersona(in personaInput) prompt.PersonaConfig {
	persona := prompt.DefaultPersonaConfig()
	if in.BotName != "" {
		persona.BotName = in.BotName
	} else if in.CharacterName != "" {
		persona.BotName = in.CharacterName
	}
	if in.UserName != "" {
		persona.UserName = in.UserName
	} else if in.UserNickname != "" {
		persona.UserName = in.UserNickname
	}
	if in.RelationshipStage != "" {
		persona.RelationshipStage = in.RelationshipStage
		persona.RelationshipStageProvided = true
	}
	if in.Emotion != "" {
		persona.Emotion = in.Emotion
	}
	if in.EmotionIntensity != nil {
		persona.EmotionIntensity = *in.EmotionIntensity
	}
	if in.UserProfile != "" {
		persona.UserProfile = in.UserProfile
	}
	if in.UserPreferences != "" {
		persona.UserPreferences = in.UserPreferences
	}
	if in.UserBoundaries != "" {
		persona.UserBoundaries = in.UserBoundaries
	}
	if in.ImportantEvents != "" {
		persona.ImportantEvents = in.ImportantEvents
	}
	if in.RelevantMemories != "" {
		persona.RelevantMemories = in.RelevantMemories
	}
	if in.RecentConversation != "" {
		persona.RecentConversation = in.RecentConversation
	}
	if in.Language != "" {
		persona.Language = in.Language
	}
	return persona
}

// buildOptions 将 HTTP 开关映射到构建选项，未传则使用默认值。
func buildOptions(in optionsInput) prompt.BuildOptions {
	options := prompt.DefaultBuildOptions()
	if in.EnableRAG != nil {
		options.EnableRAG = *in.EnableRAG
	}
	if in.EnableEmotion != nil {
		options.EnableEmotion = *in.EnableEmotion
	}
	if in.EnableEvents != nil {
		options.EnableEvents = *in.EnableEvents
	}
	return options
}

// writeSSE 统一封装 SSE 事件格式输出。
func writeSSE(w *bufio.Writer, event string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}

	return w.Flush()
}

// writeSSEWithID 写出带 event id 的 SSE 事件，便于客户端断线重连续传。
func writeSSEWithID(w *bufio.Writer, event string, id int64, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\n", id); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return err
	}
	return w.Flush()
}

// writeSSEComment 写出注释心跳，避免长连接被中间层静默断开。
func writeSSEComment(w *bufio.Writer, comment string) error {
	if comment == "" {
		comment = "ping"
	}
	if _, err := fmt.Fprintf(w, ": %s\n\n", comment); err != nil {
		return err
	}
	return w.Flush()
}
