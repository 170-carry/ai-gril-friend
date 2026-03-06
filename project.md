下面是一份 AI女友整体架构 + 按步骤开发路线图（Go + Postgres + pgvector + Redis），你照这个顺序做，能一路从 MVP 做到“像真人”的陪伴体验。

0. 总体架构分层
客户端

Web / App（文字）

可选：语音（ASR/TTS）

后端核心服务

Chat Service（对话入口）

LLM Provider（模型适配层）：API/本地模型可切换

Prompt Builder（提示词拼装）：人设 + 记忆 + 情绪 + 规则

Memory System（记忆系统）

短期记忆（最近对话）

长期结构化记忆（偏好/雷区/事件）

长期语义记忆（pgvector 向量检索）

Emotion Engine（情绪检测）

Event Engine（时间事件抽取）

Proactive System（主动聊天/提醒）

Safety & Policy（安全与边界）

Observability（日志/回放/监控）

基础设施

Postgres（结构化数据 + pgvector 向量）

Redis（会话缓存、限流、短期状态）

Queue/Worker（可选但强烈建议）：NATS/RabbitMQ（记忆抽取、embedding 异步化）

1) 数据库与表（先定地基）
必备表（第一期就建）

users

chat_messages（对话记录）

sessions（会话，可选）

user_profile（生日、称呼、时区等）

user_preferences（喜欢什么）

user_boundaries（雷区/禁忌）

life_events（面试/考试/纪念日）

memory_chunks（语义记忆文本 + metadata + embedding vector）

relationship_state（亲密度/阶段，可先留空字段）

proactive_state（最后一次主动消息时间、cooldown）

你可以先只建前 8 张表，关系系统/主动系统后面加。

2) Go 项目结构（建议）
ai-gf/
  cmd/server/main.go
  internal/
    api/            // http handlers (Gin/Fiber)
    chat/           // ChatService
    llm/            // Provider interface + implementations
    prompt/         // PromptBuilder
    memory/         // MemoryService (short/long/semantic)
    vector/         // Pgvector store (search/upsert)
    emotion/        // EmotionService
    event/          // EventService (time/event extraction)
    proactive/      // Scheduler + TriggerEngine
    safety/         // Input/Output filter
    repo/           // DB access (sqlx/gorm)
    worker/         // async jobs (embedding/extract)
  pkg/
    token/          // token count + truncation helpers
    timeparse/      // time normalize helpers
3) 开发顺序（一步一步来）
Step 1：LLM Provider（先能“说话”）

实现统一接口：

StreamGenerate(ctx, messages) -> SSE

Generate(ctx, messages)

Embed(ctx, text)（后面做向量用）

做到：

/chat 能跑通

支持流式输出（SSE/WebSocket）

✅ 里程碑：能像 ChatGPT 一样打字回复

Step 2：短期记忆（最近 N 轮）

做法：

每次 /chat 先从 chat_messages 取最近 10~20 轮

拼到 prompt 里

存储本轮 user/assistant 消息

加一个 token 裁剪：

超过阈值删最早消息

✅ 里程碑：多轮对话不串台

Step 3：人设系统（Prompt Builder）

做一份可配置的人设模板（system prompt）：

性格、语气、边界

不做什么（安全规则）

“像女友”的互动方式（多追问、多细节、少说教）

✅ 里程碑：回复风格稳定，像同一个人

Step 4：长期结构化记忆（偏好/雷区/事件）

实现：

GET/PUT user_profile

POST/DELETE user_preferences

POST/DELETE user_boundaries

POST/DELETE life_events

写入策略（先简单）：

用户明确说“记住/我喜欢/我讨厌/别再提/我生日是…” → 直接写

召回策略：

每次对话读取 profile + preferences + boundaries + 未来7天 events（可选）

注入 prompt

✅ 里程碑：能记住：喜欢什么、雷区、生日、重要安排

Step 5：语义记忆（pgvector RAG）

实现：

memory_chunks 表加 embedding vector(...)

HNSW 索引

Embed() 用 API 生成向量

每次对话：对用户输入做 embedding → pgvector TopK 检索 → 注入 prompt

写入策略：

先人工/规则触发：命中重要规则时，把“标准化记忆句子”写入 chunks

✅ 里程碑：能“回忆”以前的事，不用全靠结构化字段

Step 6：情绪检测（Emotion Engine）

前期最省事：

规则（“想哭/崩溃/烦死/焦虑/好开心…”）+ LLM 分类（兜底）
输出：

emotion + intensity

用途：

Prompt 加一句策略：不同情绪不同话术

情绪强度高时可写入情绪记忆（importance=3）

✅ 里程碑：会安慰，会顺着情绪说话

Step 7：时间事件检测（Event Engine）

前期建议用 LLM 抽取（最省事）：

输入：用户一句话 + 当前日期（很重要）

输出：event_type/title/time/importance

写入：

存到 life_events

用于主动提醒/追问

✅ 里程碑：“你明天面试”这种会自动记下来

Step 8：记忆抽取与异步化（Worker）

把耗时工作放到队列：

对话结束 → 投递 job

抽取偏好/雷区/事件（Extractor）

生成 embedding

去重合并（相似度阈值）

✅ 里程碑：对话不卡顿，记忆自动整理

Step 9：记忆衰减（Decay & Cleanup）

给 memory_chunks 加字段：

importance / access_count / last_used_at / score
定时任务：

合并重复

降权、归档、删除低价值记忆

重要记忆（pinned/importance=5）不衰减

✅ 里程碑：越用越“聪明”，不会越用越乱

Step 10：主动聊天系统（Proactive）

触发器：

早安晚安

事件前提醒、事件后追问

情绪关怀（24h 内低落）

多天未聊召回

加冷却：

每天最多 1~2 条

用户可关闭

✅ 里程碑：她会主动找你