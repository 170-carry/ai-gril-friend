Memory Extractor 设计文档（AI Companion级）
1. 模块目标

Memory Extractor 的作用：

从聊天中自动提取 值得长期记住的信息。

不是所有聊天都值得记忆。

例如：

用户说：

今天好累

不应该写入记忆。

但如果用户说：

我其实特别喜欢猫

就应该记录：

用户喜欢猫
2. Memory Extractor 架构
ChatService
     │
     ▼
Message Stored
     │
     ▼
Job Queue
     │
     ▼
Memory Extractor Worker
     │
     ├ Preference Extractor
     ├ Boundary Extractor
     ├ Event Extractor
     ├ Fact Extractor
     │
     ▼
Deduplication Engine
     │
     ▼
Memory Scoring
     │
     ▼
Memory Store
     │
     ├ structured memory
     └ semantic memory (RAG)
3. Extractor 输入

Extractor 输入：

user_message
assistant_message
conversation_context
timestamp

示例：

user: 我最近特别喜欢听周杰伦
assistant: 哈哈，你最喜欢哪首？
4. Extractor 输出

Extractor 输出统一 JSON：

{
 "preferences":[
   {"category":"music","value":"Jay Chou","confidence":0.8}
 ],
 "boundaries":[],
 "events":[],
 "facts":[]
}
5. 记忆分类

Extractor 只提取 四类记忆：

类型	说明
Preferences	用户偏好
Boundaries	用户雷区
Events	未来事件
Facts	长期事实
6. Preference Extractor

识别用户喜欢的东西。

触发句：

我喜欢
我特别爱
我迷上了
我最喜欢

例：

我喜欢KPOP

输出：

music: kpop
7. Boundary Extractor

识别用户雷区。

触发：

别再提
不要说
我讨厌聊

例：

别再提我前任

输出：

topic: ex_relationship
8. Event Extractor

识别时间事件。

例：

我周五有面试

输出：

{
 title: "job interview",
 event_time: "2026-03-06"
}
9. Fact Extractor

识别稳定事实。

例：

我在Google当工程师

输出：

occupation: software engineer
10. LLM Extractor Prompt

推荐 Prompt：

You are an information extractor.

Extract only durable personal facts from the conversation.

Ignore temporary emotions.

Return JSON with fields:
preferences
boundaries
events
facts

Rules:
Only extract when confidence > 0.7
Normalize information.
Avoid duplicates.
11. Deduplication（去重）

防止 memory 爆炸。

策略：

11.1 相似度去重

使用 embedding：

cosine similarity > 0.9

认为重复。

11.2 structured key 去重

例如：

category = music
value = kpop

已有记录 → 更新 confidence。

12. Memory Scoring

每条 memory 评分。

字段：

importance
confidence
recency

评分公式：

score =
importance * 10
+ confidence * 5
+ recency
13. 写入策略

只写高价值 memory。

写入条件：

confidence >= 0.7
importance >= 2
14. Semantic Memory 写入

写入 memory_chunks。

内容：

User likes Jay Chou music

embedding：

Embed(content)
15. Memory Pollution 防护

避免 AI 记错。

策略：

15.1 user override

如果用户说：

其实我不喜欢KPOP

删除旧记录。

15.2 conflicting memory detection

检测：

like vs dislike
16. Memory Merge

合并重复记忆。

例：

用户喜欢猫
用户喜欢橘猫

合并：

用户喜欢猫（特别喜欢橘猫）
17. Worker 架构

Extractor 在 Worker 运行。

流程：

chat finished
     │
     ▼
enqueue extraction job
     │
     ▼
worker
     │
     ├ extract memory
     ├ deduplicate
     ├ embed
     └ store
18. Go 模块结构
internal/memory/

extractor/
  extractor.go
  preference.go
  boundary.go
  event.go
  fact.go

dedup/
  dedup.go

scoring/
  scoring.go
19. 运行频率

Extractor 不必每条消息运行。

推荐：

每 3-5 轮对话

或：

聊天结束
20. 最终效果

Memory Extractor 让 AI：

越来越了解用户
越来越个性化
记忆越来越干净

而不是：

越来越混乱
记错信息
记忆爆炸
21. 与 PromptBuilder 结合

最终 Prompt：

SYSTEM
persona

PROFILE
...

PREFERENCES
...

BOUNDARIES
...

EVENTS
...

RELEVANT_MEMORIES
...

STM
...

USER_MESSAGE
22. AI Companion 的核心公式

真正让 AI 像人的公式：

AI Personality
+
Memory
+
Emotion
+
Conversation