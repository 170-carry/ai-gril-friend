Memory System 设计文档
1. 目标

Memory System 的目标是让 AI Companion 具备：

1.1 短期记忆（Short-term Memory）

记住最近对话，保证多轮上下文连续。

1.2 长期结构化记忆（Structured Memory）

记住用户的：

基本信息

偏好

雷区

重要事件

1.3 语义记忆（Semantic Memory / RAG）

能够回忆过去聊天中的事实。

例如：

用户说：

我之前说过我养猫吗？

AI：

记得呀，你说那只橘猫叫团子。

2. Memory System 架构
ChatService
    │
    ▼
MemoryService
    │
    ├── ShortTermMemory
    │      chat_messages
    │
    ├── StructuredMemory
    │      user_profile
    │      user_preferences
    │      user_boundaries
    │      life_events
    │
    └── SemanticMemory (RAG)
           memory_chunks
           pgvector
3. 数据库设计
3.1 chat_messages（短期记忆）
chat_messages

字段：

字段	说明
id	message id
user_id	用户
role	user / assistant
content	消息
seq	会话顺序
created_at	时间

索引：

(user_id, seq DESC)

用途：

提供 STM

用于 summarizeSTM

3.2 user_profile

用户稳定信息

user_profile

字段：

字段	说明
user_id	用户
nickname	昵称
birthday	生日
timezone	时区
occupation	职业
created_at	时间
3.3 user_preferences

用户偏好

user_preferences

字段：

字段	说明
id	id
user_id	用户
category	类型
value	值
confidence	置信度
last_used_at	最近使用

例：

music = kpop
food = sushi
game = dota2
3.4 user_boundaries

雷区

user_boundaries

字段：

字段	说明
id	id
user_id	用户
topic	话题
description	说明

例：

politics
ex girlfriend
3.5 life_events

重要事件

life_events

字段：

字段	说明
id	id
user_id	用户
title	事件
event_time	时间
importance	重要度
source_message_id	来源

例：

Google interview
Doctor appointment
Birthday
3.6 memory_chunks（RAG）
memory_chunks

字段：

字段	说明
id	id
user_id	用户
content	文本
embedding	vector
importance	重要度
access_count	访问次数
last_used_at	最后访问
created_at	时间
4. 短期记忆（STM）

STM = 最近 N 轮对话

来源：

chat_messages

读取：

SELECT *
FROM chat_messages
WHERE user_id=?
ORDER BY seq DESC
LIMIT 20

进入 PromptBuilder：

STM bucket

TokenBudget：

保留最近对话

超预算 → summarizeSTM()

5. summarizeSTM 机制

当 STM 超预算：

步骤

1 识别被丢弃消息
2 采样最多 6 条
3 每条裁剪到 24 tokens
4 生成 summary

示例：

EARLIER_CONVERSATION_SUMMARY

- 用户正在准备 Google 后端面试
- 用户最近在练系统设计
- 用户有点焦虑

summary 进入：

profile bucket
6. 长期结构化记忆
6.1 召回策略

每次对话加载：

user_profile
user_preferences top 5
user_boundaries
life_events (未来7天)
6.2 Prompt 注入格式
USER_PROFILE
昵称：小宇
职业：后端工程师

PREFERENCES
喜欢：猫、KPOP

BOUNDARIES
不要提：政治

UPCOMING_EVENTS
周五 14:00 Google 面试
7. Event Engine

Event Engine 负责：

识别时间事件

写入 life_events

7.1 输入

用户消息：

我周五要去 Google 面试

系统时间：

2026-03-05
7.2 输出
{
 title: "Google interview",
 event_time: "2026-03-06 14:00",
 importance: 4
}
7.3 实现策略

MVP：

规则 + 时间解析

关键词：

明天
后天
下周
周五
3月10号

升级：

LLM JSON extraction

8. 语义记忆（RAG）
8.1 检索流程
user_message
      │
      ▼
Embedding
      │
      ▼
pgvector search
      │
      ▼
TopK = 5
      │
      ▼
PolicyEngine filter
      │
      ▼
PromptBuilder
8.2 SQL 示例
SELECT content
FROM memory_chunks
ORDER BY embedding <=> $1
LIMIT 5
8.3 Prompt 注入
RELEVANT_MEMORIES

- 用户曾说养过一只橘猫
- 用户正在准备 Google 面试
9. Memory 写入策略

不要把所有对话写入 memory_chunks。

推荐只写：

偏好
用户喜欢猫
事件
用户周五面试
情绪事件
用户最近因为面试焦虑
人生变化
用户换工作
10. Worker（异步任务）

Worker 负责：

memory extraction
embedding generation
memory dedup
event extraction

流程：

Chat结束
    │
    ▼
enqueue job
    │
    ▼
worker
    ├ extract preferences
    ├ extract boundaries
    ├ extract events
    └ generate embedding
11. 记忆衰减

memory_chunks 增加字段：

importance
access_count
last_used_at
score

评分：

score =
importance*10
+ access_count*2
- age_days

低分记忆：

合并

删除

12. Go 项目结构

建议：

internal/memory/

memory_service.go
short_term.go
structured_memory.go
semantic_memory.go
event_engine.go
memory_extractor.go
13. Chat Pipeline

完整流程：

User message
     │
     ▼
MemoryService
 ├ STM
 ├ Structured
 └ RAG
     │
     ▼
PromptBuilder
     │
     ▼
LLM
     │
     ▼
Assistant response
     │
     ▼
Save chat
     │
     ▼
Async memory extractor