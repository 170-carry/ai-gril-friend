# 关系升级 NLP 开发说明

## 1. 本次升级的核心策略

本次关系升级不再依赖单一关键词命中，而是采用：

- `规则层`：关键词 + 正则 + 模板短语 + 否定词窗口
- `语义层`：embedding 相似度兜底
- `理解层`：LLM fallback 结构化打分
- `状态层`：多维关系状态 + 持续证据累积 + 阶段式状态机
- `对话层`：CE / Prompt / Memory 共用同一份关系语义

目标是把“关系升级”从一次性触发，升级成“连续观察、分阶段放权、边界优先、支持优先”的稳定策略。

## 2. 主链路

### 2.1 信号分析链路

1. 用户消息进入 `signals.Matcher`
2. 先跑规则命中
3. 规则信号弱时，跑 embedding fallback
4. 规则 + embedding 仍弱时，跑 LLM fallback
5. 输出结构化 signal 分数给 `memory` 和 `conversation`

### 2.2 关系状态链路

1. `memory.ProcessTurn` 调用 `updateRelationshipState`
2. `relationshipSignalsFromTurn` 产出多维信号
3. `applyRelationshipSignals` 平滑更新多维关系分数
4. `deriveRelationshipStage` 归纳当前关系阶段
5. `memory.BuildContext` 把关系快照注入给 `chat/prompt`

## 3. 升级功能 -> 代码位置

| 升级功能 | 代码位置 | 说明 |
| --- | --- | --- |
| 共享 signal 目录 | `internal/signals/defaults.go` | 定义关系/情绪/支持/边界等 signal 规则，新增 `self_disclosure`、`engagement`、`acceptance_of_closeness`、`romantic_interest`、`dependence_risk` 等信号。 |
| 共享 matcher 总入口 | `internal/signals/catalog.go` `NewMatcher` `ScoreSignalsWithOptions` `ScoreIntents` | `memory` 和 `conversation` 共用同一套 matcher，不再各写一套词表。 |
| 规则 + embedding + LLM 级联 | `internal/signals/catalog.go` `ScoreSignalsWithOptions` | 先规则，再 embedding，再 LLM fallback；避免每条消息都直接打 LLM。 |
| LLM fallback 分析器 | `internal/signals/llm_fallback.go` `NewLLMAnalyzer` `Analyze` | 用结构化 prompt 让 LLM 输出 signal 分数，并做缓存。 |
| 服务启动接线 | `cmd/server/main.go` `cmd/backfill-memory/main.go` | 创建共享 `signalAnalyzer`，同时注入 `memoryService` 和 `chatService`。 |
| Memory 使用共享 analyzer | `internal/memory/service.go` `UseSignalAnalyzer` | 记忆侧信号识别接入 LLM fallback。 |
| Chat/CE 使用共享 analyzer | `internal/chat/service.go` `UseSignalAnalyzer` | 对话规划侧意图识别也接入同一 analyzer。 |
| 多维关系状态持久化 | `internal/repo/memory_repo.go` `RelationshipState` `GetRelationshipState` `UpsertRelationshipState` | 把关系状态从单薄字段升级为 familiarity / intimacy / trust / flirt / boundary_risk / support_need 等连续分数。 |
| 关系表结构升级 | `internal/repo/migrations.go` | 新增 `relationship_state` 表字段：`familiarity_score`、`flirt_score`、`boundary_risk_score`、`support_need_score`。 |
| Memory 对外关系快照 | `internal/memory/types.go` `RelationshipSnapshot` | 给 chat / prompt 暴露统一关系快照结构。 |
| CE 对外关系快照 | `internal/conversation/types.go` `RelationshipSnapshot` `Normalize` | CE 内部也升级为同一套关系字段，并兼容旧阶段名。 |
| Prompt Persona 关系字段 | `internal/prompt/system_prompt.go` `PersonaConfig` `normalizePersona` | Prompt 人设层新增多维关系字段，并统一阶段归一化。 |
| Prompt 关系块注入 | `internal/prompt/modules.go` `relationshipModule.Build` | 把阶段 + 连续分数一起注入到 system prompt。 |
| 关系摘要构建 | `internal/memory/relationship.go` `buildRelationshipSnapshot` `formatRelationshipState` | 生成给 PromptBuilder 使用的关系摘要文本和快照。 |
| 关系策略文案 | `internal/memory/relationship.go` `relationshipGuidance` | 根据阶段、边界风险、支持需求输出关系策略，明确“支持优先、不把脆弱当升级机会”。 |
| 多维关系信号抽取 | `internal/memory/relationship.go` `relationshipSignalsFromTurn` | 从用户消息抽 affection / vulnerability / self_disclosure / engagement / acceptance / romantic / dependence_risk 等信号。 |
| 支持优先 / 边界冻结 | `internal/memory/relationship.go` `applyRelationshipSignals` | `closenessGate` 在高边界风险或高支持需求时收紧 intimacy / flirt / playfulness 的增长。 |
| 急性边界回落 | `internal/memory/relationship.go` `applyRelationshipSignals` | 当前轮 `boundary` 很高时，阶段直接回落到 `companion`。 |
| 阶段式状态机 | `internal/memory/relationship.go` `deriveRelationshipStage` | 新阶段：`companion` / `familiar` / `trust_building` / `light_flirt` / `romantic`。 |
| CE 关系感知规划 | `internal/conversation/engine.go` `BuildPlan` `RenderPlanForPrompt` | 计划文本中输出新的关系维度，供 prompt 使用。 |
| CE 关系感知语气控制 | `internal/conversation/engine.go` `resolveTone` | 根据 familiarity / trust / flirt / boundary_risk / support_need 调整 warmth / playfulness / directness。 |
| Chat 合并关系上下文 | `internal/chat/service.go` `mergePersonaWithMemory` `buildConversationPlan` | 把 memory 的关系快照合并进 persona，再传给 CE。 |
| Prompt 阶段说明升级 | `internal/prompt/system_prompt.go` `BuildInstructionPrompt` | 系统提示中的关系阶段从旧版 `friend/close` 升级成新 5 阶段。 |
| STM 预算保护 | `internal/prompt/token_budget.go` `fitSTMBlock` `fitSTMBlockWithFallback` `hardCapPrompt` | prompt 过长时，优先压缩最近历史而不是直接删光；同时保住 `STM summary + latest stm anchor`。 |
| 历史摘要保留 | `internal/prompt/token_budget.go` `fitSummaryBlock` | 历史被裁剪时，尽量保留结构化摘要。 |
| 测试覆盖 | `internal/signals/matcher_test.go` `internal/memory/relationship_test.go` `internal/conversation/engine_test.go` `internal/prompt/builder_test.go` | 覆盖否定识别、embedding fallback、LLM fallback、边界冻结、支持优先、STM 摘要与最近历史保留。 |

## 4. 当前关系状态定义

### 4.1 连续分数

- `familiarity`: 熟悉度，是否已经形成稳定互动历史
- `intimacy`: 亲近感，可表达多近
- `trust`: 信任度，是否适合更深表达
- `flirt`: 暧昧/恋爱化表达接受度
- `boundary_risk`: 当前是否需要收住节奏
- `support_need`: 当前是否更需要接情绪和陪伴
- `playfulness`: 允许的玩笑和俏皮强度
- `interaction_heat`: 最近互动热度

### 4.2 阶段定义

- `companion`
- `familiar`
- `trust_building`
- `light_flirt`
- `romantic`

阶段不是单词命中触发，而是由多维分数和持续证据共同决定。

## 5. 关键实现约束

- 脆弱、依赖、委屈不直接当作“关系升级证据”
- 边界风险高时，优先回落表达权限
- 支持需求高时，先安慰和陪伴，压低 flirt / playfulness 增长
- Prompt 超预算时，优先压缩而不是粗暴删掉最近上下文
- `memory / conversation / prompt` 使用同一套关系语义，避免各层判断不一致

## 6. 后续建议

- 把 `internal/signals/defaults.go` 继续抽成可热更新配置文件
- 给 LLM fallback 增加可观测日志和命中采样
- 给关系状态增加“衰减规则”和“多轮窗口统计”
- 为 `relationship_state` 增加后台可视化调试页
