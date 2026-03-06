# Conversation Engine 功能清单（当前实现）

## 1. 核心模块
- [x] Intent Router（规则路由 + 置信度输出）
- [x] State Manager（OPEN/EXPLORE/SUPPORT/SOLVE/COMMIT/CLOSE）
- [x] Style Controller（warmth/playfulness/directness/length/emoji_level）
- [x] Turn Planner（mirror/ask/add_value/close_softly）
- [x] 最多 1 个追问规则
- [x] stop_rules（用户拒绝则停止追问）
- [x] Safety & Boundary Gate（高风险与边界命中收敛）
- [x] Memory Hooks（生成 SAVE_* 动作）
- [x] Proactive Hooks（生成 SCHEDULE_* 动作）

## 2. Prompt 注入
- [x] 输出 `CONVERSATION_PLAN` block 注入 PromptBuilder
- [x] `/chat/debug` 返回结构化 `conversation_plan`

## 3. Actions 执行链路（本次补完）
- [x] `ConversationPlan.actions` 映射到 `memory.TurnInput.PlannedActions`
- [x] 动作在现有 memory async worker 中真正执行（`ProcessTurn`）
- [x] 队列失败时降级同步执行，动作不丢

执行映射：
- `SAVE_PREFERENCE` -> `user_preferences`
- `SAVE_BOUNDARY` -> `user_boundaries`
- `SAVE_EVENT` -> `life_events`
- `SCHEDULE_EVENT_REMINDER` -> `proactive_tasks(task_type=event_reminder)` -> `outbound_queue` -> `chat_messages/chat_outbox`
- `SCHEDULE_CARE_FOLLOWUP` -> `proactive_tasks(task_type=care_followup)` -> `outbound_queue` -> `chat_messages/chat_outbox`
- `SAVE_SEMANTIC_MEMORY` -> `memory_chunks`（`memory_type=ce_action`）

## 4. 主要代码位置
- CE 规划：`internal/conversation/engine.go`
- CE 类型：`internal/conversation/types.go`
- Prompt 注入：`internal/prompt/modules.go`
- Chat 接线：`internal/chat/service.go`
- Action 执行：`internal/memory/service.go`

## 5. 验证状态
- [x] 单元测试通过：`go test ./...`
- [x] 运行时验证：发送带“偏好+事件+焦虑”消息后，数据库出现偏好、事件、提醒、回访、ce_action 记忆
