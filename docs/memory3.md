Memory Ranking System 设计（AI Companion级）
1. 目标

Memory Ranking System（MRS）负责在每次对话时，从多种记忆来源中选出最优的少量上下文：

Structured Memory（profile/preferences/boundaries/events）

Semantic Memory（pgvector chunks）

Conversation Summary（早期对话摘要）

Short-term Memory（最近对话，通常由 STM 管）

输出给 PromptBuilder 的是：

ranked_memories[]（3–8 条）

rank_trace（解释：为什么选它、分数多少、是否被过滤）

2. 设计原则

宁缺毋滥：不确定就不注入，改为追问

边界优先：Boundaries 永远在更高层拦截

新鲜度加权但不压过重要度：重要人生事实不应该被“昨天随口一句”挤掉

多样性：避免 5 条记忆都在讲同一件事（去重/覆盖面）

可解释：每条入选都能说明“为何入选”，否则调不动

3. 输入输出
3.1 输入（RankRequest）

user_id

now

user_message

conversation_context（可选：最近 1–2 条对话摘要）

k（目标返回条数，默认 5）

constraints（tokenBudget 允许给 RAG 的预算）

3.2 输出（RankResult）

memories[]：已排序的记忆（含类型、文本、来源 id）

trace[]：每条候选的分数与过滤原因（debug 用）

4. 总体流程（Pipeline）
Candidate Generation
    │
    ├─ Structured candidates (profile/preferences/events)
    └─ Semantic candidates (pgvector TopK)
            │
            ▼
Policy Filter (boundaries / safety / conflicts)
            │
            ▼
Scoring (multi-factor)
            │
            ▼
Diversity Selection (MMR / clustering)
            │
            ▼
Compression (optional: memory sentence)
            │
            ▼
TopK output → PromptBuilder
5. Candidate Generation（候选生成）
5.1 Structured 候选（强规则）

profile：固定注入（昵称/称呼/时区/生日可选）

boundaries：固定注入（高优先级）

preferences：TopN（按 score 或 last_used_at）

events：未来 7 天 + importance≥阈值（如 3）

Structured 的策略是“少且稳定”：不靠 rank 系统做复杂选择。

5.2 Semantic 候选（向量检索）

用 pgvector 先拉一批候选（不要一开始就只拉 5 条，容易漏）：

初始候选：TopK = 30（或 20）

再进入 rerank/过滤，最后输出 3–8 条

候选结构建议带上：

sim（向量相似度）

importance

created_at

last_used_at

access_count

type/topic（可选，便于去重）

6. Policy Filter（过滤与冲突处理）

先过滤再打分，避免“有毒记忆”进入评分池。

6.1 Boundaries 冲突过滤

记忆 topic 命中用户雷区 → 直接丢弃

记忆里含敏感内容且用户明确不提 → 丢弃

6.2 事实冲突（Conflicting memories）

同一 key 的互斥事实（例如：

喜欢 KPOP vs 不喜欢 KPOP

单身 vs 有对象
）
建议策略：

优先保留 最近一次被用户确认 的那条

旧的标记为 superseded=true（不删也行，但不参与注入）

6.3 低可信过滤

confidence < 0.6 或 importance=1 且 access_count=0 且 age>30d → 丢

7. Scoring（多因子评分）

对每条候选记忆计算一个综合分 S：

7.1 推荐评分模型（可落地）
S = w_sim * SimScore
  + w_imp * ImportanceScore
  + w_rec * RecencyScore
  + w_use * UsageScore
  + w_pin * PinnedScore
  - w_red * RedundancyPenalty
(1) SimScore（语义相关）

来自 pgvector（cosine similarity）

归一化到 0~1

(2) ImportanceScore（重要度）

importance：1~5
建议映射到 0~1：

ImportanceScore = importance / 5.0

(3) RecencyScore（新鲜度）

用衰减函数（越久越低）：

RecencyScore = exp(-age_days / tau)

tau 建议：

普通记忆 tau=30

重要记忆 tau=120（更慢衰减）

(4) UsageScore（使用价值）

访问越多越可能是“有用记忆”：

UsageScore = log(1 + access_count) / log(1 + cap)

cap 例如 20

(5) PinnedScore（钉住）

对于生日、雷区、长期称呼等：

pinned=true → +0.2~0.4 的加分（或直接硬注入不参与排序）

(6) RedundancyPenalty（冗余惩罚）

如果这条和已选集合太相似（sim > 0.9），扣分。

8. 权重推荐（默认值）

对 AI Companion（更重“重要度+关系事实”，而不是纯相似度）：

w_sim = 0.45

w_imp = 0.25

w_rec = 0.15

w_use = 0.10

w_pin = 0.20（只对 pinned 生效）

w_red = 0.25

如果你发现模型“老提不相关的旧事”，调高 w_sim。
如果你发现模型“只会顺着当前话题，忘记重要设定”，调高 w_imp / w_pin。

9. Diversity Selection（多样性选择）

仅按分数 topK 会出现：

5 条都在讲“面试”

或 5 条都在讲“猫”

你需要多样性选择。

9.1 MMR（Maximal Marginal Relevance）

经典做法：每次选一条，同时惩罚与已选集合过相似的候选。

公式（概念）：

MMR = λ * relevance(candidate)
    - (1-λ) * max_similarity(candidate, selected_set)

λ 建议 0.7（更偏相关性）。

9.2 Topic Bucket（简单工程版）

如果你已经给 memory 标了 topic：

每个 topic 最多取 1–2 条

先取最高分 topic，再填充

10. Compression（记忆压缩策略）

RAG chunk 原文往往很长，注入 prompt 前应压缩成“记忆句”。

10.1 记忆句规范

一条记忆最好是：

主语明确（用户/你们）

事实单一

无废话

可被核对

例：

✅ 用户喜欢周杰伦的歌（偏好）

✅ 用户周五下午有 Google 面试（事件）

❌ 用户很棒很努力（空洞）

❌ 你之前说过很多事情……（不可核对）

10.2 压缩时机

当 RAG bucket 接近预算上限时：

优先改为记忆句，而不是丢弃

11. 与 TokenBudget / PromptBuilder 的配合

输出给 PromptBuilder 的 RAG 记忆应包含：

content_short（记忆句）

content_full（可选，调试用）

score

sim

importance

topic

PromptBuilder 注入模板建议：

RELEVANT_MEMORIES (use as background; if uncertain, ask user to confirm)
- ...
- ...

并且加一句硬规则：

若记忆与当前输入不一致，优先追问确认

12. 线上指标（必须有）

你至少要记录：

memory_hit_rate：多少请求用了记忆

memory_token_cost：记忆占用 token

memory_helpfulness（可用用户反馈 proxy：继续追问/停留时长）

hallucination_events：记忆引发的错误次数（需要标注）

top_memory_sources：structured vs rag 的使用比例

这些决定你后续怎么调权重、怎么做衰减。

13. Go 代码结构建议
internal/memory/ranking/
  ranker.go
  candidates.go
  filter.go
  scoring.go
  diversity_mmr.go
  compress.go
  trace.go

接口：

GenerateCandidates(ctx, req) []Candidate

Filter(ctx, req, candidates) []Candidate

Score(ctx, req, candidates) []ScoredCandidate

Select(ctx, req, scored) []Candidate (MMR)

Compress(ctx, selected) []MemoryItem

14. 默认参数（MVP 推荐）

pgvector initialTopK = 30

outputK = 5

similarityThreshold = 0.75（低于不注入）

dedupThreshold = 0.90

topicMaxPer = 2

maxRagTokens（给 RAG 的桶预算）= 800～1500（看模型）