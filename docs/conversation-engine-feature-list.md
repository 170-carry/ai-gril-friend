# Conversation Engine 功能清单（当前实现）

## 1. 核心模块
- [x] Intent Router（规则路由 + 置信度输出）
- [x] State Manager（OPEN/EXPLORE/SUPPORT/SOLVE/COMMIT/CLOSE）
- [x] Style Controller（warmth/playfulness/directness/length/emoji_level）
- [x] Relationship State（stage/intimacy/trust/playfulness_threshold/interaction_heat）
- [x] Turn Planner（mirror/ask/add_value/close_softly）
- [x] 最多 1 个追问规则
- [x] stop_rules（用户拒绝则停止追问）
- [x] Safety & Boundary Gate（高风险与边界命中收敛）
- [x] Memory Hooks（生成 SAVE_* 动作）
- [x] Proactive Hooks（生成 SCHEDULE_* 动作）

## 2. Prompt 注入
- [x] 输出 `CONVERSATION_PLAN` block 注入 PromptBuilder
- [x] `/chat/debug` 返回结构化 `conversation_plan`
- [x] 输出 `RELATIONSHIP_STATE` block 注入 PromptBuilder

## 3. Relationship State（本次补完）
- [x] 新增 `relationship_state` 表
- [x] 存储连续状态：`stage / intimacy_score / trust_score / playfulness_threshold / interaction_heat`
- [x] 记录最近互动时间与累计轮次
- [x] 由现有 memory async worker 在 `ProcessTurn` 后自动更新
- [x] 对话前通过 memory context 注入 CE 和 PromptBuilder
- [x] 若请求未显式指定 stage，则优先使用存储态 relationship stage
- [x] `/chat/debug` 可通过 `memory_context.relationship_state` 与 `persona_after_merge` 查看结果

## 4. Actions 执行链路（本次补完）
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

## 5. Topic Engine（本次补完）
- [x] 新增 `conversation_topics` 表，独立维护话题线程
- [x] 新增 `conversation_topic_edges` 表，维护 topic graph
- [x] `memory.BuildContext` 返回 `topic_context` / `active_topics`
- [x] `memory.BuildContext` 返回 `topic_graph`
- [x] PromptBuilder 注入 `[Active Topics]` block
- [x] CE 输出 `conversation_plan.topic`
- [x] CE 识别 `open_new / continue_existing / gentle_recall / none`
- [x] 长叙事消息会拆成多个 `topic seed`
- [x] 超长叙事优先接入 LLM topic summarizer，失败回退 clause 规则
- [x] 支持 `related topics` 并行线程
- [x] 支持 `cluster_key + alias_terms + callback` 的旧梗聚类
- [x] 新增 `TRACK_TOPIC` 动作，写回 `conversation_topics`
- [x] 新增 `LINK_TOPICS` 动作，写回 `conversation_topic_edges`
- [x] `LINK_TOPICS` 支持 `cause_effect / progression / contrast / context / co_occurs`
- [x] 新增 `SCHEDULE_TOPIC_REENGAGE` 动作，落库 `proactive_tasks(task_type=topic_reengage)`
- [x] `topic_reengage` 发送前会检查话题是否已结束、是否已被重新聊过、是否仍在回钩窗口内
- [x] `topic_reengage` 在强关系边上可附带 1 条 secondary topic
- [x] 主动送达后回写 `last_recalled_at / recall_count`

## 6. 主动发送决策器（新增）
- [x] 发送前不是“到点就发”，而是先跑 `send / defer / cancel` 决策
- [x] 判断用户主动消息开关
- [x] 判断免打扰时间
- [x] 判断最近 6h+ 频率窗口内是否已经主动过
- [x] 判断本次任务是否有明确 reason
- [x] 判断候选文案是否与最近一次同类型主动文案过于相似
- [x] `/chat/proactive/debug` 返回每条任务的 `decision` 预览

## 7. 主要代码位置
- CE 规划：`internal/conversation/engine.go`
- CE 话题策略：`internal/conversation/topic.go`
- CE 类型：`internal/conversation/types.go`
- Prompt 注入：`internal/prompt/modules.go`
- Chat 接线：`internal/chat/service.go`
- Action 执行：`internal/memory/service.go`
- 话题状态与图：`internal/memory/topic.go` `internal/repo/topic_repo.go`
- 关系状态：`internal/memory/relationship.go`
- 主动决策：`internal/proactive/decision.go`
- 话题回钩调度：`internal/proactive/dispatcher.go`

## 8. 验证状态
- [x] 单元测试通过：`go test ./...`
- [x] 运行时验证：发送“明天面试，好慌”后，数据库出现 `conversation_topics` 与 `topic_reengage` 任务
- [x] 用户第二天只说“在吗”时，CE 可输出 `gentle_recall`
