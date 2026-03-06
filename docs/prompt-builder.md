Prompt Builder 架构设计（AI Companion级）
0. 设计目标

Prompt Builder 不是“拼字符串”，而是一个 决策与组装引擎：

稳定性：回复风格一致、边界一致、不会“串人格”

可维护：模块化、可插拔、可测试、可调试

可控 Token：在预算内优先保留高价值上下文

可扩展：记忆/情绪/事件/关系/主动系统加入时不重构

可观测：每次请求都能解释“为什么这样拼、删了什么、用了什么”

1. Prompt Builder 完整架构
1.1 组件分层
ChatService
  └─ PromptBuilder
       ├─ ContextLoader（加载上下文：profile/preferences/boundaries/events/stm/rag/emotion）
       ├─ PolicyEngine（优先级/冲突处理/安全约束）
       ├─ TokenBudgeter（token 预算分配与裁剪）
       ├─ Assembler（组装成最终 messages[]）
       └─ Trace（可观测：拼装过程、token 统计、命中记忆）
1.2 输入输出

输入（BuildRequest）

user_id / session_id

user_message（本轮输入）

now（当前时间，事件抽取/提醒很关键）

model_id（不同模型 token 预算不同）

options（是否启用 RAG/情绪/事件等开关）

输出（BuildResult）

messages[]（给 LLM 的最终消息数组，system+user+assistant）

trace（调试信息：模块顺序、每段 token、裁剪记录、命中记忆）

2. Prompt 优先级系统（最核心）

Prompt 是“规则系统”，必须有优先级与冲突治理：

2.1 优先级层级

从高到低（建议固定顺序）：

Safety / Policy（强约束）

System Persona（人设与边界）

Relationship State（关系阶段/亲密度策略，可先留空）

User Boundaries（用户雷区，强约束）

User Profile（稳定事实）

Preferences（偏好）

Events（未来事件、重要日程）

Semantic Memories（RAG 命中片段）

Emotion Context（当前情绪与话术策略）

Short-term Conversation（最近对话）

User Message（本轮输入）

关键点：强约束永远在最上面，避免“记忆/情绪”覆盖安全与边界。

2.2 冲突规则（示例）

若 RAG 命中内容与 Boundaries 冲突：丢弃 RAG

若 STM 与 Profile 冲突：以 Profile 为准（除非 STM 是用户刚更新的事实）

若 Emotion 提示要求“强安慰”，但用户明确要“直接给方案”：遵循用户指令，但保持温柔语气

实现方式：每个模块产出 PromptBlock，进入 PolicyEngine 做冲突决策。

3. Prompt Block 规范（模块化基础）
3.1 数据结构

每个模块生成一个 Block：

id：唯一标识（persona / boundaries / rag / emotion / stm…）

priority：优先级（越小越高或相反，固定即可）

kind：System / Developer / User / Assistant（最终要映射到 OpenAI messages）

content：文本内容

tokens_est：估算 token（可先粗估，后续接 tokenizer）

budget_tag：属于哪个预算桶（见 TokenBudget）

hard：是否“不可裁剪”（如 safety/boundaries）

redactable：是否允许脱敏/摘要（如 stm）

metadata：模块内部信息（命中哪些记忆、相似度等）

4. Token 管理（工业级可控）
4.1 预算分桶（推荐）

把总预算拆成桶，便于“可解释裁剪”：

HARD（不可裁剪）：safety/persona/boundaries

PROFILE：profile/preferences/events

RAG：语义记忆

EMOTION：情绪策略

STM：短期对话

USER：本轮输入（必留）

RESERVE：给模型输出的预留（必须留）

示例（可按模型调整）：

Bucket	目标占比
HARD	10–15%
PROFILE	10–15%
RAG	15–25%
EMOTION	5–8%
STM	25–35%
USER	固定（用户输入原文）
RESERVE	固定（输出预留）
4.2 裁剪策略（从低优先级开始）

裁剪顺序通常是：STM → RAG → Preferences → Events（视重要度）
HARD 一律不动。

STM 裁剪

先删最老轮次

再把旧轮次做摘要（可选：用轻量 summarizer 或规则摘要）

最后只保留“用户最后意图 + 关键事实”

RAG 裁剪

TopK 先少后多（如默认 5，超预算降到 3/2）

只保留高相似度（阈值 0.75+）

去重（相似片段合并）

5. RAG 插入策略（语义记忆）
5.1 检索的正确姿势

检索 query 不一定是“用户原话”，建议两步：

Query Rewrite（可选但很有效）
把用户输入改写成更适合检索的查询（例如去掉口语、补全实体）。

Embedding + TopK
取 3–8 条，再由 reranker/规则筛选 3–5 条

5.2 注入格式（推荐模板）

RAG 不是“直接塞原文”，要加上使用规范：

明确：这些是“可能相关的记忆”

明确：不确定就追问，不要编造

给每条加简短标签（时间/主题/可信度）

示例：

RELEVANT_MEMORIES (use as background; if uncertain, ask user to confirm)
- [topic: cat][when: 2026-02][conf:0.82] 用户曾说小时候养过一只橘猫，叫“团子”
- [topic: interview][when: 2026-03][conf:0.79] 用户提到准备 Google 后端面试，担心系统设计
5.3 “记忆污染”防护

RAG 必须经过 PolicyEngine：

与 boundaries 冲突 → 丢弃

低相似度 → 不注入（宁可不注入，也别硬塞）

“敏感/隐私” → 降级为模糊提示或不注入

6. Emotion 插入策略（情绪引导）
6.1 Emotion 的定位

Emotion 不是让模型“演戏”，而是给它 对话策略：

当前情绪（sad/anxious/happy/angry…）

强度（1–5）

建议话术（先共情/再提问/再建议）

禁忌（不要说教/不要冷处理）

6.2 注入格式（短而强）

放在 RAG 后、STM 前（或 STM 前也行，但别盖过核心事实）：

EMOTION_GUIDE
- Detected emotion: anxious (4/5)
- Strategy: validate feelings → ask 1 gentle question → give 2 actionable steps
- Tone: warm, intimate, not preachy
6.3 触发条件

强度 ≥ 3 才注入（避免每句都写情绪）

若用户明确要求“只要结论/不要安慰”，Emotion 策略切换为“简洁+温柔”

7. Go 代码设计（可落地骨架）
7.1 目录建议
internal/prompt/
  builder.go
  types.go
  modules/
    safety.go
    persona.go
    boundaries.go
    profile.go
    preferences.go
    events.go
    rag.go
    emotion.go
    stm.go
  policy/
    engine.go
  budget/
    token_budget.go
    tokenizer.go
  trace/
    trace.go
7.2 核心接口

Module：产出 PromptBlock

PolicyEngine：排序/冲突/过滤

TokenBudgeter：裁剪/摘要/降级

Assembler：输出 LLM messages[]

建议接口（简化版）：

type Module interface {
    ID() string
    Priority() int
    Build(ctx context.Context, req BuildRequest) ([]PromptBlock, error)
}

type PromptBuilder struct {
    modules []Module
    policy  PolicyEngine
    budget  TokenBudgeter
    asm     Assembler
}

func (b *PromptBuilder) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
    blocks := []PromptBlock{}
    for _, m := range b.modules {
        out, err := m.Build(ctx, req)
        if err != nil { /* degrade or fail based on module */ }
        blocks = append(blocks, out...)
    }
    blocks = b.policy.Apply(ctx, req, blocks)
    blocks = b.budget.Fit(ctx, req, blocks)
    msgs, trace := b.asm.Assemble(ctx, req, blocks)
    return BuildResult{Messages: msgs, Trace: trace}, nil
}
7.3 模块降级（Production 必备）

RAG 失败 → 跳过 RAG

Emotion 分类失败 → 不注入 Emotion

DB 超时 → 只用 persona + user_message + 少量 stm（缓存/redis）

每个模块应该声明：

Required() 是否硬依赖

Degrade() 降级策略（返回空 block 或用缓存）

8. 可观测 Trace（调试/回放的关键）

每次 Build 产出：

blocks 顺序

每块 token_est / 最终保留与否

裁剪日志（删了哪些轮、RAG 从 K=5 降到 K=3）

命中记忆列表（id、相似度）

这样你能回答：“为什么她会提到这件事？”

9. 测试策略（强烈建议）
9.1 Golden Prompt Tests（黄金样例）

给定固定上下文（profile+pref+events+stm+rag），断言输出 prompt 结构和顺序不变。

9.2 Budget Tests（预算裁剪）

构造超长 stm/rag，验证：

HARD 不被裁

STM 从旧到新裁剪

RAG TopK 自动降级

9.3 Policy Tests（冲突）

boundaries 禁政治，RAG 命中政治相关 → RAG 被丢弃