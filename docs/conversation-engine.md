AI Companion Conversation Engine（对话引擎设计）
1. 模块目标

Conversation Engine（CE）负责决定：

怎么回：语气、深度、长度、结构

要不要追问：追什么、问几句、什么时候收

怎么维持话题：不跳戏、不机械

怎么推进关系：亲密度、边界感

什么时候主动：提醒、关心、召回

它输出的不是“最终回答”，而是给 PromptBuilder / LLM 的 对话策略（Conversation Plan）。

2. 总体架构
User Message
    │
    ▼
Conversation Engine
    │
    ├─ Intent Router（意图路由）
    ├─ State Manager（对话状态机）
    ├─ Style Controller（风格控制）
    ├─ Turn Planner（回合规划：追问/建议/安慰）
    ├─ Safety & Boundary Gate（边界与安全）
    ├─ Memory Hooks（记忆写入/召回钩子）
    └─ Proactive Hooks（主动触发钩子）
    │
    ▼
Conversation Plan
    │
    ▼
Prompt Builder
    │
    ▼
LLM Response
3. 输入输出
3.1 输入（ConversationRequest）

user_id / session_id

user_message

last_turns（少量最近对话）

memory_snapshot（profile/boundary/events/top memories）

emotion（可选）

now（时间）

relationship_state（可选）

3.2 输出（ConversationPlan）

intent：用户意图

mode：对话模式（安慰/建议/闲聊/任务/亲密）

tone：语气控制参数

response_structure：回答结构模板

questions[]：建议追问列表（0~2）

actions[]：需要调用的工具/写入事件/记忆

stop_rules：收束规则（避免无限追问）

4. Intent Router（意图路由）

把用户输入路由到“对话模式”，这是 CE 的入口。

建议最小集合（8 类就够强）：

Emotional Support（情绪支持）

Advice / Problem Solving（建议/方案）

Small Talk（闲聊陪伴）

Story / Sharing（讲故事/分享经历）

Planning / Event（计划/日程事件）

Relationship / Intimacy（关系互动/撒娇/暧昧）

Boundary / Safety（边界/敏感）

Meta / Product（问你是谁、怎么工作）

路由方式：

MVP：规则 + 轻量 LLM 分类兜底

输出一个 intent 和置信度

5. State Manager（对话状态机）

AI Companion 必须有“对话状态”，否则会像问答机一样跳来跳去。

5.1 核心状态（建议）

OPEN：刚进入一个话题

EXPLORE：追问获取细节

SUPPORT：共情安慰

SOLVE：给方案/步骤

COMMIT：形成计划/承诺（“明天我提醒你”）

CLOSE：收束/总结/转场

每次回复都要判断：下一轮应该处于哪个状态。

5.2 状态转移示例

用户表达焦虑 → SUPPORT

用户问“怎么办” → SOLVE

方案给完 → COMMIT（确认下一步）

用户说“谢谢我去做了” → CLOSE（鼓励+轻转场）

6. Turn Planner（回合规划）

Turn Planner 决定这次回复包含哪些“段落组件”。

推荐默认结构（非常像真人聊天）：

6.1 四段式（高命中）

Mirror：复述/共情（短）

Ask：追问 1 个关键问题（可选）

Add Value：给信息/建议/陪伴内容

Close Softly：轻收尾 + 给下一步机会

示例（焦虑场景）：

Mirror：听起来你现在很紧绷…

Ask：你最担心的是面试哪一块？

Add：我们可以把系统设计拆成 3 步…

Close：你想先从哪一步开始？我陪你走。

6.2 追问策略（最重要）

真人聊天不是一直输出，而是“问得刚好”。

规则：

每轮最多 1–2 个问题

优先问“能推进对话”的问题（澄清/细化/选择题）

不问“查户口式问题”

用户明显疲惫时：不追问、先安慰

追问类型：

澄清问题：你说的“很烦”主要是工作还是家里？

选择题：你更想要我安慰你，还是一起想办法？

深挖问题：如果这件事解决了，你希望生活变成怎样？

7. Style Controller（风格控制）

风格由三部分决定：

persona（固定）

relationship_state（亲密度）

user_preference（用户偏好：要简洁还是要陪聊）

7.1 可控参数

warmth：温柔程度 0~1

playfulness：俏皮程度 0~1

directness：直接程度 0~1（越高越像“给结论”）

length：短/中/长

emoji_level：0~2（陪伴产品常用，但要克制）

你不需要模型“自己猜”，这些参数应注入 prompt 作为策略。

8. Safety & Boundary Gate（边界门）

在 CE 内部先挡掉不该走的模式：

命中 boundaries：禁止继续深挖，转为轻描淡写或换话题

敏感内容：进入 safety 模式（更谨慎，更少细节）

用户拒绝：停止追问（stop_rules）

9. Memory Hooks（记忆钩子）

CE 要告诉 Memory 系统：本轮有哪些记忆线索。

9.1 写入触发（例）

用户明确说“记住/我喜欢/别再提/我生日”

用户提到未来事件（明天/下周/几点）

用户长期事实（职业、城市、关系状态变化）

9.2 写入动作（actions）

CE 输出 actions：

SAVE_PREFERENCE(category,value)

SAVE_BOUNDARY(topic)

SAVE_EVENT(title,time)

SAVE_SEMANTIC_MEMORY(sentence, importance)

真正写入由 worker 执行（避免阻塞）。

10. Proactive Hooks（主动触发钩子）

CE 不是只处理被动回复，还要留下“以后主动”的线索：

事件前提醒：event_time - 2h / -1d

事件后追问：event_time + 3h / +1d

情绪关怀：高强度负面情绪 24h 内回访（可选）

召回：多天未聊（cooldown 控制）

这些不一定当下发，而是写入 proactive_state 或任务队列。

11. Conversation Plan 注入 Prompt 的格式

建议 PromptBuilder 加一个 block：

CONVERSATION_PLAN
- intent: Emotional Support
- mode: support_then_solve
- tone: warm, gentle, not preachy
- structure: mirror → ask 1 question → give 2 steps → soft close
- ask: ["你最担心面试哪一块？"]
- stop_rules: if user refuses, stop asking

LLM 有了 plan，就不会乱跑。

12. 关键指标（你上线后要看）

Follow-up Rate：用户是否继续聊（陪伴产品核心）

Question Accept Rate：用户是否愿意回答追问

Conversation Length：平均轮次

User Satisfaction：点赞/停留/复访

Boundary Violations：踩雷次数（必须极低）

13. MVP 实现顺序（强烈建议）

Intent Router（规则+兜底）

Turn Planner（四段式）

“最多问 1 个问题” 规则

stop_rules（用户拒绝就停）

Memory Hooks（事件/偏好/雷区）

relationship_state（后加）

上线后再加：

多样化话题维持（topic engine）

更细粒度的 relationship_state 策略学习

说明：主动聊天触发器（proactive）已在当前代码中落地，具体见第 14 节。

14. 当前实现清单（2026-03-06）

这一节描述的是“文档设计”已经在当前项目代码里真正落地的部分，目的是让设计文档和实际行为保持一致。

14.1 核心模块落地情况

- [x] Intent Router：规则路由 + 置信度输出
- [x] State Manager：OPEN / EXPLORE / SUPPORT / SOLVE / COMMIT / CLOSE
- [x] Style Controller：warmth / playfulness / directness / length / emoji_level
- [x] Turn Planner：mirror / ask / add_value / close_softly
- [x] 每轮最多 1 个追问
- [x] stop_rules：用户拒绝后停止追问
- [x] Safety & Boundary Gate：高风险内容和边界命中时自动收敛
- [x] Memory Hooks：生成 SAVE_* 动作
- [x] Proactive Hooks：生成 SCHEDULE_* 动作

14.2 Prompt 注入

- [x] CE 输出 `CONVERSATION_PLAN` block，注入 Prompt Builder
- [x] `/chat/debug` 返回结构化 `conversation_plan`
- [x] Prompt Builder 会把最近对话、记忆、情绪和 CE 规划一起组装给 LLM

14.3 Actions 执行链路

当前实现不是“只做规划”，而是已经把 CE 产出的 actions 接到现有 memory async worker 中真正执行。

- [x] `ConversationPlan.actions` 映射到 `memory.TurnInput.PlannedActions`
- [x] actions 优先进入 memory async worker 执行
- [x] 队列不可用时降级同步执行，避免动作丢失

当前动作映射如下：

- `SAVE_PREFERENCE` -> `user_preferences`
- `SAVE_BOUNDARY` -> `user_boundaries`
- `SAVE_EVENT` -> `life_events`
- `SAVE_SEMANTIC_MEMORY` -> `memory_chunks`

14.4 主动消息运行时（Proactive Runtime）

这里是本轮补完的重点。现在系统已经不是“记下以后要主动”，而是具备真正可运行的主动消息链路。

- [x] `SCHEDULE_EVENT_REMINDER` -> `proactive_tasks(task_type=event_reminder)`
- [x] `SCHEDULE_CARE_FOLLOWUP` -> `proactive_tasks(task_type=care_followup)`
- [x] `proactive_tasks` 到期后进入 `outbound_queue`
- [x] `outbound_queue` 投递成功后写入 `chat_messages` 和 `chat_outbox`
- [x] 前端可以通过 `/chat/outbox/pull` 或 `/chat/outbox/stream` 接收主动消息

主动运行时当前已支持：

- [x] 独立任务表，不再把系统提醒/回访混入 `life_events`
- [x] `reason` 字段，记录“为什么要主动”
- [x] `dedup_key` 去重
- [x] `status` 状态流转
- [x] `attempt_count` / `max_attempts` 失败重试
- [x] `cooldown_seconds` 冷却时间
- [x] `quiet_hours_enabled` 免打扰时间窗
- [x] `enabled` 用户主动消息总开关

重要约束：

- `life_events` 现在只保留真实用户事件
- 系统提醒、回访、主动关心任务不再写入 `life_events`
- Prompt 中的 UPCOMING_EVENTS 已过滤旧版遗留的“提醒：*”和“情绪回访”污染数据

14.5 调试与观测接口

- [x] `POST /chat/debug`：查看 Prompt Builder 和 `conversation_plan`
- [x] `GET /chat/proactive/state`：查看用户主动消息状态
- [x] `POST /chat/proactive/state`：更新主动消息开关、冷却、免打扰
- [x] `GET /chat/proactive/debug`：查看任务、队列和状态
- [x] `GET /chat/outbox/pull`：短轮询拉取主动消息
- [x] `GET /chat/outbox/stream`：SSE 实时接收主动消息

14.6 已完成的行为修正

- [x] 修复“有点焦虑”因为包含“点”而被误判为时间事件的问题
- [x] 修复 proactive hooks 只落库、不真正触发主动消息的问题
- [x] 修复系统提醒/回访污染长期事件记忆的问题

14.7 当前验证结果

- [x] `go test ./...` 通过
- [x] 真实运行时验证通过：事件提醒任务可生成、可到期扫描、可进入 outbox、可投递到聊天窗口
- [x] 用户关闭主动消息后，新任务会被取消，不再投递
- [x] 命中免打扰窗口后，任务会顺延到允许时间
- [x] 命中冷却时间后，任务会顺延到下一个可发送时间

14.8 仍未补完的部分

以下能力仍然属于后续增强项，不在当前已落地清单内：

- [ ] 多样化话题维持（topic engine）
- [ ] 更细粒度 relationship_state 学习与长期演化
- [ ] 更复杂的主动召回策略（如多天未聊、多条件组合触发）
