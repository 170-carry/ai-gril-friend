# Topic Engine 与主动召回

更新时间：2026-03-06

## 1. 目标

这次补的是 CE 文档里一直挂着的两个“像真人”的核心能力：

- 话题维持：不是只答当前句，而是知道“我们刚刚在聊什么、这个话题有没有讲完”。
- 主动召回：当用户只是轻轻来一句“在吗/下班了/哈哈”，系统可以在合适的时候回钩昨天没讲完的事，而不是永远从零开场。

实现原则：

- 不把 topic state 塞进 `memory_chunks`，而是独立成 `conversation_topics`。
- 当前轮回复和未来主动回钩共用同一份话题状态，避免两套逻辑漂移。
- 回钩必须“有来由、有节制、可取消”，不能变成机械催进度。

## 2. 新增数据结构

新增表：`conversation_topics`

关键字段：

- `topic_key`：稳定 key，用于同一话题线程去重和续写。
- `topic_label`：给 CE / prompt / proactive 看的短标题，例如“面试准备”。
- `summary`：当前话题的可读摘要。
- `callback_hint`：适合轻轻回提的旧梗/短句。
- `cluster_key`：把同一旧梗 / 同一线程的不同说法聚成一类。
- `metadata.alias_terms`：给 callback / joke / thread clustering 用的别名词。
- `status`：`active` / `resolved`。
- `importance`：1~5，决定排序和主动回钩优先级。
- `last_discussed_at`：最近一次聊到这个话题的时间。
- `next_recall_at`：下一次适合主动提起的时间。
- `last_recalled_at` / `recall_count`：记录已经主动回钩过几次。

新增表：`conversation_topic_edges`

- 用于记录两个 topic 之间的轻量关系边，形成 topic graph。
- 当前已支持 `co_occurs / cause_effect / progression / contrast / context`。

## 3. 运行链路

### 3.1 对话前

`memory.BuildContext` 现在会额外读取 `conversation_topics`，返回：

- `memory_context.topic_context`
- `memory_context.active_topics`
- `memory_context.topic_graph`

这些信息会继续流向：

- `prompt.PersonaConfig.TopicContext`
- Prompt Builder 的 `[Active Topics]` block
- CE 的 `MemorySnapshot.ActiveTopics`

### 3.2 当前轮规划

CE 新增 `TopicStrategy`，本轮会在以下模式里选择：

- `open_new`：用户开了一个新话题，值得持续。
- `continue_existing`：用户正在延续之前的话题。
- `gentle_recall`：用户当前只是轻触式开场，但存在适合自然回钩的未完话题。
- `none`：这轮不强行绑定旧话题。

同时 CE 现在支持：

- 一条长叙事消息拆出多个 `topic seed`
- 超长叙事优先走 LLM topic summarizer，失败再回退 clause 规则
- 主线程之外附带 `related` 并行线程
- 通过 `cluster_key + alias_terms + callback 相似度` 匹配旧梗
- 通过 `topic graph` 把相关线程一起带出来，并区分因果 / 递进 / 对照 / 上下文关系

`RenderPlanForPrompt` 现在会输出：

- `topic: mode=... label=... reason=...`
- `topic_hint: ...`

这样 LLM 在回复时会知道：

- 该不该接昨天的话题。
- 是“继续讲”，还是“轻轻提一下”。
- 不要像数据库检索一样生硬背记忆。

### 3.3 对话后落库

CE 会额外产出两个动作：

- `TRACK_TOPIC`
- `LINK_TOPICS`
- `SCHEDULE_TOPIC_REENGAGE`

它们由现有 memory async worker 执行：

- `TRACK_TOPIC`：更新 `conversation_topics`
- `LINK_TOPICS`：更新 `conversation_topic_edges`
- `SCHEDULE_TOPIC_REENGAGE`：写入 `proactive_tasks(task_type=topic_reengage)`

## 4. 主动召回规则

新任务类型：`topic_reengage`

生成文案时会优先使用：

- `topic_label`
- `callback_hint`
- `topic_summary`
- 若存在强关系 secondary topic，则只带 1 条相关线程，不会一次抛出全部并行线

典型文案：

- `昨天没讲完的「面试准备」，我又想起来了。后来推进得怎么样啦？`
- `突然想起你昨天提到的「面试准备」，你还说系统设计那块最让你发虚。后来有新进展吗？`

发送前不是直接发，而是会先过两层判断：

### 4.1 通用 proactive 决策

- 用户是否开启主动消息
- 是否命中免打扰
- 是否还在主动冷却窗口
- `reason` 是否足够明确
- 文案是否和最近一次同类型主动过于相似

### 4.2 话题专属决策

在 `Dispatcher.stageDueTasks` 里，`topic_reengage` 还会额外检查：

- 这个话题是否还存在
- 话题是否已经 `resolved`
- 这个任务创建之后，话题是否已经被重新聊过
- `next_recall_at` 是否还没到
- 这个话题是否已经被主动回钩过

只有都通过，才会真正进 `outbound_queue`。

## 5. 当前启发式

目前话题提取是“LLM topic summarizer + 规则 + 聚类 + 轻量图关系”的组合：

- 长叙事拆句：会按叙事转折先做 clause 切分，超长叙事优先交给 LLM 提炼线程，失败时退回规则 seed
- 更细 label：支持“工作冲突 / 工作压力 / 感情关系 / 家庭关系 / 睡眠状态 / 金钱压力 / 居住安排 / 职业选择”等
- 延续判断：识别“那个/这件事/后来/进展/还没/继续”等续话信号
- 轻回钩判断：识别“在吗/想你/哈哈/下班了/晚安”等轻触式开场
- 收束判断：识别“搞定了/解决了/没事了/我去做了”等关闭信号
- 旧梗聚类：不仅看 `callback_hint` 精确命中，还会比对 `alias_terms` 与文本相似度
- 并行线程：同一条消息里若同时包含多条线，会选 primary topic，并记录 `related topics`
- topic graph：若某个 topic 和当前命中的线程强相关，会被一起带出来，并按 `cause_effect / progression / contrast / context / co_occurs` 调整排序与提示
- 主动回钩：默认仍以 primary topic 为主，但在强关系边上会补 1 条 secondary topic

这套规则的目标不是“完美理解所有 topic”，而是先把最常见、最有陪伴感的那类场景跑起来。

## 6. 调试与验证

你现在可以从这几个位置看 topic engine 是否生效：

- `/chat/debug`
  - `memory_context.topic_context`
  - `memory_context.active_topics`
  - `conversation_plan.topic`
- `/chat/proactive/debug`
  - `task_type=topic_reengage`
  - `decision`
- 数据库
  - `conversation_topics`
  - `proactive_tasks`
  - `outbound_queue`

建议手动验证三种场景：

1. 用户聊一个未完话题，例如“我明天面试，好慌”。
2. 第二天用户只说“在吗”。
3. 看 CE 是否给出 `gentle_recall`，以及是否生成/投递 `topic_reengage`。

## 7. 已知边界

当前版本还不是最终形态，主要边界有：

- LLM summarizer 目前主要用于超长/多线程叙事，不会覆盖所有普通短消息。
- topic graph 已有关系类型，但仍是轻量推断，还不是完整的 topic graph reasoning。
- 主动回钩只会附带 1 条强相关 secondary 线程，仍然不会一次抛出整张图。

但这版已经把“话题线程”从概念变成了真正可运行的状态机和主动链路，后面可以在这个基础上继续精修策略，而不用再重搭底座。
