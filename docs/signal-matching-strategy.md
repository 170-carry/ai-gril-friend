# Signal Matching Strategy（当前实现）

这份文档描述当前仓库里“用户文本信号识别”的实际运行策略。

目标不是做一个大而全的 NLP 系统，而是用一套可维护、可调参、可逐步增强的方案，同时服务两条链路：

- `Conversation Engine` 的意图路由
- `relationship_state` 的关系信号更新

当前实现的核心原则是：

1. 共享一份信号配置，避免 `engine.go` 和 `relationship.go` 各自维护词表。
2. 先走高精度规则匹配，再用 embedding 做语义补充。
3. 只有前两层都不够确定时，才启用 LLM fallback。
4. 不把 LLM 作为主判定器，优先保证稳定性、延迟和可解释性。

## 1. 代码入口

- 共享规则定义：`internal/signals/defaults.go`
- 通用匹配器：`internal/signals/catalog.go`
- LLM 兜底分析器：`internal/signals/llm_fallback.go`
- CE 意图路由接入：`internal/conversation/engine.go`
- 关系信号接入：`internal/memory/relationship.go`
- 生产 wiring：`internal/chat/service.go`、`cmd/server/main.go`

## 2. 总体流程

用户消息进入系统后，信号识别按下面的顺序执行：

1. 文本标准化
2. 规则匹配
3. 否定词过滤
4. 汇总成 signal score
5. 若规则命中偏弱，再尝试 embedding fallback
6. 若规则和 embedding 之后仍然偏弱，再尝试 LLM fallback
7. 将 signal score 映射到：
   - CE intent score
   - relationship signal

可以把它理解成：

`文本 -> signal -> intent / relationship`

## 3. 当前信号集合

当前共享信号包括：

- `affection`
- `vulnerability`
- `support_seeking`
- `humor`
- `routine_warmth`
- `boundary`
- `advice_seeking`
- `small_talk`
- `story_sharing`
- `planning_event`
- `meta_product`
- `safety`

这批信号定义在 `internal/signals/defaults.go`，是整个系统现在的统一来源。

## 4. 规则层策略

每个 signal 都由一组规则构成：

- `Phrases`
  - 直接短语命中
  - 例如 `焦虑`、`想你`、`别问`
- `Regexes`
  - 用来覆盖更灵活的表达
  - 例如 `(?:睡不着|睡不好)`、`(?:别|不要).{0,4}(?:问|提|聊)`
- `Templates`
  - 用来匹配“词之间允许隔一点字”的结构化短语
  - 例如 `陪 ... 我`、`找 ... 你 ... 聊`

### 4.1 打分方式

一个 signal 的规则命中后，会把对应权重累加，最后 clamp 到 `0~1`。

也就是说，当前不是“命中几个词就给 0.4 / 0.7 / 1.0”那种固定档位了，而是：

- 不同规则可以有不同权重
- phrase / regex / template 可以同时贡献分数
- 最终分数上限仍然是 `1.0`

这让它比原来的纯关键词计数更细，也更容易调。

### 4.2 否定词策略

对大多数需要“语义方向正确”的信号，当前启用了 `NegationSensitive`。

典型否定词包括：

- `不`
- `没`
- `没有`
- `不是`
- `不再`
- `已经不`
- `不用`

机制是：

- 如果命中词前面的一个小窗口内出现否定词，就把这次命中视为无效
- 例如：
  - `我已经不焦虑了` 不再算 `vulnerability`
  - `不用陪我` 不再算 `support_seeking`

当前这套否定策略是“轻量窗口法”，不是完整句法分析，所以优点是简单稳定，缺点是面对特别复杂的长句时仍然可能有边角误判。

## 5. Embedding 层策略

embedding 不是主判定器，而是第二层语义补充。

### 5.1 什么时候触发

只有同时满足下面条件时才会走 embedding：

1. 该 signal 配置了 `EmbeddingSeeds`
2. 当前规则分数低于该 signal 的 `EmbeddingMinLexical`
3. 本句所有 signal 的最大规则分数仍然低于 `0.40`

这代表系统认为：

- 规则没有明确命中
- 但整句可能仍然有语义信号
- 这时才值得付出额外 embedding 成本

换句话说：

- 强规则命中时，不跑 embedding
- 明显句子时，优先信规则
- 只有“有点像，但没写在词表里”的句子才走语义兜底

### 5.2 怎么打分

每个 signal 可以配置若干 `EmbeddingSeeds`，比如：

- `vulnerability` 里有 `心里空落落的`
- `support_seeking` 里有 `可以陪我待一会吗`

流程是：

1. 对用户句子做 embedding
2. 对该 signal 的 seed 句做 embedding
3. 取最大 cosine similarity
4. 如果相似度低于 `EmbeddingThreshold`，记 0
5. 如果高于阈值，就映射成一个 `0~EmbeddingMaxScore` 的分数

设计意图是：

- embedding 只能“补票”，不能无限放大
- 语义兜底最多补到一个受控上限
- 避免某个语义相似句把信号直接冲到 1.0

## 6. LLM Fallback 策略

LLM 现在已经接入，但它的定位是第三层兜底，而不是常驻主分类器。

### 6.1 什么时候触发

当前 matcher 只有在下面条件都成立时才会调用 LLM：

1. 已经完成规则匹配
2. 如果 embedding 可用，也已经尝试过 embedding fallback
3. 当前所有 signal 的最高分仍然低于 `0.35`
4. matcher 上配置了 LLM analyzer

这意味着：

- 明显命中的句子，不走 LLM
- embedding 已经补得足够清楚的句子，不走 LLM
- 只有“规则和 embedding 都还不够确定”的句子，才走 LLM

### 6.2 LLM 输出形式

LLM 不直接输出 intent，而是输出 signal 分数 JSON。

目标格式类似：

```json
{
  "affection": 0,
  "vulnerability": 0.82,
  "support_seeking": 0.18,
  "boundary": 0
}
```

这样做的原因是：

- 仍然保持 `text -> signal -> intent / relationship` 这条统一链路
- CE 和 relationship 继续共用同一组 signal
- LLM 不会绕开现有结构，变成一条平行且不可控的新逻辑

### 6.3 LLM prompt 策略

当前 LLM prompt 会明确要求：

- 只返回 JSON
- 对所有 signal 打 `0~1` 分
- 保守判定
- 正确处理否定、边界、转折

同时 prompt 会带上：

- signal 名称
- signal 描述
- 典型 phrase / seed 示例
- 几条关键反例
  - `我已经不焦虑了`
  - `不用陪我`
  - `别问了`

### 6.4 LLM 结果如何合并

当前合并策略很克制：

- 只有当 LLM 分数高于当前已有分数时，才会覆盖
- LLM 不会把已有强规则命中再次放大
- 本质上是 `max(current, llm)`

也就是说，LLM 只补弱项，不推翻前两层。

### 6.5 缓存策略

LLM analyzer 内部带了按文本缓存。

设计目的是：

- 同一句文本被多次分析时，不重复调用 LLM
- 让 CE 和 memory 在共享 analyzer 的情况下尽量复用结果

## 7. CE（Conversation Engine）里的策略

CE 不再直接维护一套本地关键词列表，而是通过共享 matcher 获取 intent score。

### 6.1 signal -> intent 映射

当前 intent 映射关系是：

- `emotional_support`
  - `vulnerability * 1.0`
  - `support_seeking * 0.85`
- `advice_problem_solving`
  - `advice_seeking * 1.0`
- `small_talk`
  - `small_talk * 1.0`
  - `humor * 0.25`
  - `routine_warmth * 0.20`
- `story_sharing`
  - `story_sharing * 1.0`
- `planning_event`
  - `planning_event * 1.0`
- `relationship_intimacy`
  - `affection * 1.0`
  - `routine_warmth * 0.25`
- `boundary_safety`
  - `boundary * 1.0`
  - `safety * 1.2`
- `meta_product`
  - `meta_product * 1.0`

### 6.2 intent 选择策略

CE 当前做法是：

1. 算出每个 intent 的 score
2. 取 top1 和 top2
3. 如果 top1 太低（`<= 0.05`）
   - 有问号时偏向 `advice_problem_solving`
   - 否则回落到 `small_talk`

### 6.3 特殊覆盖规则

目前还保留了一条人工覆盖规则：

- 如果 `emotional_support` 和 `advice_problem_solving` 都有明显命中
- 并且情绪支持分不低于建议分
- 则优先路由到 `emotional_support`

这是为了保留“先接住情绪，再给方案”的策略。

### 6.4 置信度策略

CE 的 intent confidence 现在基于：

- top1 分数
- top1 和 top2 的 margin
- 特殊安全场景的置信度抬升

其中：

- 边界/安全类命中较强时，置信度至少抬到 `0.92`

## 8. Relationship State 里的策略

relationship 更新不再自己维护独立词表，而是直接使用共享 signal。

当前从用户消息提取这些关系信号：

- `Affection <- affection`
- `Vulnerability <- vulnerability`
- `SupportSeeking <- support_seeking`
- `Humor <- humor`
- `RoutineWarmth <- routine_warmth`
- `Boundary <- boundary`

如果用户文本没有明显 boundary 命中，还会额外看一眼 assistant 消息：

- 若 assistant 文本里有收边界信号
- 则按 `boundary * 0.2` 作为轻微补充

### 7.1 interaction heat

当前 heat 由这些因素组成：

- 基础值 `0.20`
- 最近互动新鲜度 `recency`
- 如果用户消息长度 `>= 12 rune`，再加一点
- affection / vulnerability / support / humor / routine 的正向贡献
- boundary 的负向贡献

### 7.2 连续状态更新

relationship state 当前更新方式仍然是平滑累计：

- `intimacy`
  - affection 正向
  - vulnerability 轻正向
  - routine 轻正向
  - boundary 负向
- `trust`
  - vulnerability 正向
  - support_seeking 正向
  - routine 轻正向
  - boundary 负向
- `playfulness_threshold`
  - humor 正向
  - affection 轻正向
  - boundary 负向
  - vulnerability 轻负向
- `interaction_heat`
  - 采用上一轮和当前 heat 的平滑混合

也就是说：

- 亲密表达会让 intimacy 上升
- 暴露脆弱会让 trust 上升
- 用户划边界会让 intimacy / trust / playfulness 都收回来

## 9. 为什么不直接全靠 LLM

当前策略刻意没有把这件事做成“每句都走 LLM 输出分类 JSON”，原因是：

- 规则更稳定
- 更容易解释和调试
- 延迟更低
- 成本更低
- 单测更容易写

对于当前这个项目，信号识别的目标更像：

- 稳定 routing
- 稳定 relationship 演化
- 避免明显误判

而不是追求一个“理论上最聪明”的分类器。

所以现在的设计不是“不要 LLM”，而是：

- 用 LLM
- 但只在需要的时候用
- 并且让它服从现有 signal 结构

## 10. 当前优点

- `engine` 和 `relationship` 终于共用同一份信号配置
- 可以统一调词表、权重、阈值
- 比原来的 `strings.Contains + 命中个数` 更细
- 能处理一部分否定表达
- 能覆盖一些“词不完全连着出现”的短语模板
- 对未显式写入词表但语义相近的句子，有 embedding 兜底
- 对规则和 embedding 都不够确定的句子，有 LLM 最后兜底
- 仍然保留统一、可解释的 signal 中间层

## 11. 当前边界和已知局限

这套策略虽然比以前强，但仍然不是完整语义理解系统，当前局限包括：

- 否定词仍然是窗口法，不是句法级理解
- 反讽、玩笑式否定、复杂转折句还可能误判
- embedding seed 还是人工配置，覆盖度取决于 seed 质量
- LLM fallback 仍然可能受 prompt 质量和模型稳定性影响
- LLM 当前只看单句文本，还没有显式引入多轮上下文
- 多意图混合句虽然能部分处理，但不是显式多标签输出
- intent 层仍是单标签 top1 选择

## 12. 现在应该怎么调

如果后面要继续优化，优先级建议是：

1. 先改 `internal/signals/defaults.go`
   - 加短语
   - 调权重
   - 加 regex
   - 加 template
   - 调 seed 和阈值
2. 如果是 matcher 机制问题，再改 `internal/signals/catalog.go`
3. 如果是 LLM 兜底质量问题，再改 `internal/signals/llm_fallback.go`
4. 尽量不要再回到 `engine.go` / `relationship.go` 里各自加私有词表

也就是说：

- 配置变化，优先改 `defaults.go`
- 机制变化，才改 `catalog.go`
- LLM prompt / 解析 / 缓存变化，改 `llm_fallback.go`

## 13. 当前验证范围

当前单测已经覆盖了几类关键场景：

- 否定词不会把 `不焦虑了` 误判成 vulnerability
- 模板可以识别 `可以陪我待一会吗`
- embedding fallback 可以识别词表外但语义接近的脆弱表达
- 当规则和 embedding 都不足时，LLM fallback 可以补出 signal
- 当规则或 embedding 已经足够明确时，LLM 不会重复介入
- CE 可以用 fallback 把句子路由到 `emotional_support`
- relationship 信号提取也会复用同样的 fallback

如果后续继续补测试，建议优先加这几类：

- 转折句
  - `虽然还是有点焦虑，但比昨天好多了`
- 反向 support
  - `不用陪我，我自己待会`
- 边界 + 玩笑混合
  - `别闹啦，太肉麻了`
- 多意图混合
  - `我有点焦虑，你能陪我顺便帮我想想怎么办吗`

## 14. 一句话总结

当前实现是一套“共享规则为主、embedding 第二层补充、LLM 最后兜底”的信号识别策略。

它的定位不是最智能，而是：

- 统一
- 稳定
- 可解释
- 可持续调参

这正是当前阶段最适合这个项目的方案。
