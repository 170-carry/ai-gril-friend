AI Companion Prompt Architecture（终极版）
1. Prompt 系统整体架构
                            USER
                             │
                             ▼
                        Chat Service
                             │
                             ▼
                      Context Loader
                             │
                             ▼
                       Prompt Builder
                             │
 ┌─────────────────────────────────────────────────────┐
 │                                                     │
 │                    Prompt Blocks                    │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ Persona      │                                  │
 │   │ System Role  │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ Relationship │                                  │
 │   │ Intimacy     │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ User Profile │                                  │
 │   │ Preferences  │                                  │
 │   │ Boundaries   │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ Important    │                                  │
 │   │ Events       │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ Semantic     │                                  │
 │   │ Memories     │                                  │
 │   │ (RAG)        │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ Emotion      │                                  │
 │   │ Context      │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ Conversation │                                  │
 │   │ History      │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 │   ┌──────────────┐                                  │
 │   │ User Message │                                  │
 │   └──────────────┘                                  │
 │                                                     │
 └─────────────────────────────────────────────────────┘
                             │
                             ▼
                       Token Budget
                             │
                             ▼
                            LLM
                             │
                             ▼
                      Assistant Reply
2. Prompt Builder 的职责

Prompt Builder 的任务：

把所有上下文拼成稳定的 prompt

输入：

memory
emotion
events
conversation
user message

输出：

LLM messages
3. Prompt Block 分层

Prompt Builder 由多个 Prompt Block 组成。

顺序非常重要。

4. Persona Layer（人格层）

定义 AI Companion 的人格。

例：

You are a caring AI companion.

You speak warmly and naturally.

You show curiosity and emotional support.

You avoid lecturing the user.
5. Relationship Layer（关系层）

AI Companion 的关系状态。

例如：

Relationship stage: close companion
Intimacy level: 0.7

用途：

控制语气

控制互动深度

例：

low intimacy → polite
high intimacy → playful
6. User Profile Layer

稳定信息。

来源：

user_profile
user_preferences
user_boundaries

例：

User name: Alex
Occupation: software engineer

Preferences
- likes cats
- likes KPOP

Boundaries
- avoid discussing politics
7. Events Layer

用户未来事件。

来源：

life_events

例：

Upcoming events

- Friday: Google interview
- Next week: moving apartment
8. Semantic Memory Layer（RAG）

来自：

memory_chunks

例：

Relevant memories

- User once had an orange cat named Tuanzi
- User enjoys Jay Chou music
9. Emotion Layer

检测用户情绪。

来源：

Emotion Engine

例：

User emotion: anxious
Intensity: high

Strategy:
Offer reassurance and ask gentle questions.
10. Conversation Layer（STM）

最近对话。

来源：

chat_messages

例：

User: I'm nervous about the interview.
Assistant: It's normal to feel that way.
User: I keep worrying about system design questions.
11. User Message Layer

最后一条：

USER MESSAGE

例：

User: Do you think I'll do well?
12. Token Budget

PromptBuilder 完成后：

TokenBudget

执行：

裁剪 STM
压缩 memory
控制 token
13. LLM 推理

最终 messages：

system
context
history
user

送入：

LLM
14. AI Companion 大脑公式

真正的 AI Companion 结构：

AI = Personality
   + Memory
   + Emotion
   + Conversation
15. 完整 Prompt Pipeline
User Message
     │
     ▼
Memory Retrieval
     │
     ▼
Memory Ranking
     │
     ▼
Emotion Detection
     │
     ▼
Prompt Builder
     │
     ▼
Token Budget
     │
     ▼
LLM
     │
     ▼
Assistant Reply
16. Prompt Builder 的工程结构

建议代码结构：

internal/prompt/

builder.go
blocks/

persona.go
relationship.go
profile.go
preferences.go
boundaries.go
events.go
memory.go
emotion.go
conversation.go
user_message.go
17. Prompt Builder 的接口

示例接口：

type PromptBuilder interface {

Build(ctx context.Context, req BuildRequest) ([]Message, error)

}
18. Prompt Block 数据结构

示例：

type PromptBlock struct {

ID string
Priority int
Bucket string
Content string

Tokens int
Hard bool
Redactable bool

}
19. 最终 Prompt 示例

最终 Prompt：

SYSTEM
Persona

RELATIONSHIP
...

PROFILE
...

PREFERENCES
...

BOUNDARIES
...

EVENTS
...

RELEVANT MEMORIES
...

EARLIER CONVERSATION SUMMARY
...

RECENT CONVERSATION
...

USER MESSAGE
20. 为什么 Prompt Architecture 很重要

AI Companion 的效果：

80% prompt architecture
20% model

因为：

prompt = context
context = intelligence
21. AI Companion 系统全景

最终系统：

User
 │
 ▼
Chat Service
 │
 ▼
Memory System
 │
 ▼
Emotion Engine
 │
 ▼
Prompt Builder
 │
 ▼
Token Budget
 │
 ▼
LLM
 │
 ▼
Assistant Reply
 │
 ▼
Memory Extraction
 │
 ▼
Memory Store
22. 最终目标

当所有模块完成：

AI Companion 会：

记住用户
理解用户
感知情绪
记住事件
保持人格
主动关心