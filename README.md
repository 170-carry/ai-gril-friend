# ai-gf (Step 1-3)

已实现第一阶段：
1. LLM Provider + SSE 流式 `/chat`
2. `chat_messages` 落库 + 最近 20 轮加载
3. 可直接使用的人设 `system prompt` 模板

## 技术栈
- Go + Fiber
- Postgres (含 `pgvector` 扩展)
- LLM Provider: OpenAI-compatible `/chat/completions`

## Docker Compose 一键启动（推荐）
```bash
cp .env.example .env
# 仅需修改 .env 的 LLM_API_KEY（以及可选模型配置）
docker compose up -d --build
```

启动后：
- API: `http://localhost:9901`
- Postgres: `localhost:50081`（user: `root` / password: `123456`）
- Redis: `localhost:20081`（password: `123456` / db: `8`）
- 本地备份目录: `./backups`

查看日志：
```bash
docker compose logs -f app
docker compose logs -f db-backup
```

停止：
```bash
docker compose down
```

## 数据库本地备份
- 自动备份容器：`db-backup`
- 备份文件：`./backups/<db_name>_YYYYmmdd_HHMMSS.dump`
- 默认策略：
  - 每 `21600` 秒（6小时）备份一次
  - 保留 `7` 天
- 可在 `.env` 调整：
  - `BACKUP_INTERVAL_SECONDS`
  - `BACKUP_RETENTION_DAYS`

恢复示例（把 dump 导回容器内数据库）：
```bash
docker compose exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists' < ./backups/ai_gf_YYYYmmdd_HHMMSS.dump
```

## 本地开发启动（非 Docker）
```bash
go mod tidy
cp .env.example .env
# 修改 .env 中的 DATABASE_URL / LLM_API_KEY
go run ./cmd/server
```

服务默认监听 `:9901`（可通过 `SERVER_ADDR` 修改）。

## 数据表
启动时会自动执行迁移（`internal/repo/migrations.go`），并可参考 SQL 文件：
- `migrations/001_chat_messages.sql`

## 接口
### 健康检查
```bash
curl http://localhost:9901/healthz
```

### 流式聊天
```bash
curl -N -X POST http://localhost:9901/chat \
  -H 'Content-Type: application/json' \
  -d '{
    "user_id": "u_1001",
    "session_id": "s_default",
    "message": "我今天有点焦虑",
    "persona": {
      "bot_name": "Luna",
      "user_name": "阿泽",
      "relationship_stage": "romantic",
      "emotion": "anxious",
      "emotion_intensity": 0.72,
      "language": "zh-CN",
      "user_profile": "程序员，晚上容易焦虑",
      "user_preferences": "喜欢被温柔鼓励，不喜欢说教",
      "user_boundaries": "不讨论家庭隐私细节",
      "important_events": "明天下午有技术面试",
      "relevant_memories": "上周提过担心面试发挥",
      "recent_conversation": "刚聊到今天被需求压得有点喘不过气"
    }
  }'
```

SSE 事件：
- `event: token`：增量 token
- `event: done`：结束，返回 `assistant_message_id`
- `event: error`：中途错误

### Prompt Debug（不调用 LLM）
```bash
curl -X POST http://localhost:9901/chat/debug \
  -H 'Content-Type: application/json' \
  -d '{
    "user_id": "u_1001",
    "session_id": "s_default",
    "message": "我今天有点焦虑",
    "model_id": "deepseek-chat",
    "options": {
      "enable_rag": true,
      "enable_emotion": true,
      "enable_events": true
    },
    "persona": {
      "bot_name": "Luna",
      "user_name": "阿泽",
      "relationship_stage": "close",
      "emotion": "anxious",
      "emotion_intensity": 0.8,
      "user_boundaries": "不讨论家庭隐私",
      "relevant_memories": "上周提过面试压力"
    }
  }'
```

返回：
- `messages`：最终发给 LLM 的消息数组
- `trace`：模块顺序、block决策、预算使用、裁剪日志
- `report`：可读阶段报告（stage blocks 顺序、final kept/drop、RAG K 变化、memory hits）
- `history_count`：参与构建的历史消息数
- `memory_context`：本轮召回的结构化/语义记忆上下文
- `memory_context_err`：记忆构建失败信息（非空表示已降级）
- `persona_after_merge`：记忆回填后的最终 persona

## 关键行为
- 每次 `/chat`：
  1. 先拉取该 `user_id + session_id` 最近 20 轮（40 条）历史
  2. 写入本轮用户消息到 `chat_messages`
  3. 召回长记忆（profile/preferences/boundaries/events/ranked memories）并回填 persona
  4. 通过 Prompt Builder 拼装最终 `messages(system + history + user)`
  5. 流式返回 token
  6. 完整 assistant 回复落库
  7. 记忆抽取异步入队（队列异常时自动降级同步）

记忆异步参数：
```bash
MEMORY_ASYNC_ENABLED=true
MEMORY_WORKER_COUNT=2
MEMORY_QUEUE_SIZE=256
MEMORY_JOB_TIMEOUT_SEC=8
```

向量参数：
```bash
EMBEDDING_PROVIDER=openai_compatible
EMBEDDING_BASE_URL=https://api.openai.com/v1
EMBEDDING_API_KEY=<YOUR_API_KEY>
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_ENDPOINT=
EMBEDDING_DIMENSIONS=1536
EMBEDDING_TEXT_TYPE=document
EMBEDDING_OUTPUT_TYPE=dense
EMBEDDING_VECTOR_DIM=1536
EMBEDDING_HASH_FALLBACK=true
```

千问（百炼）通用文本向量同步接口示例：
```bash
EMBEDDING_PROVIDER=qwen_sync
EMBEDDING_BASE_URL=https://dashscope.aliyuncs.com
EMBEDDING_API_KEY=<DASHSCOPE_API_KEY>
EMBEDDING_MODEL=text-embedding-v4
EMBEDDING_ENDPOINT=/api/v1/services/embeddings/text-embedding/text-embedding
EMBEDDING_DIMENSIONS=1536
EMBEDDING_TEXT_TYPE=document
EMBEDDING_OUTPUT_TYPE=dense
EMBEDDING_VECTOR_DIM=1536
EMBEDDING_HASH_FALLBACK=true
```

说明：
- `memory_chunks.embedding` 当前是 `vector(1536)`，所以建议 `EMBEDDING_DIMENSIONS` 与 `EMBEDDING_VECTOR_DIM` 保持 `1536`。
- 若外部 embedding 接口不可用，且 `EMBEDDING_HASH_FALLBACK=true`，会自动回退本地 hash embedding，避免流程中断。

历史数据回填（抽取历史记忆 + 补齐向量）：
```bash
go run ./cmd/backfill-memory
```

仅回填向量：
```bash
go run ./cmd/backfill-memory -extract=false -embed=true
```

embedding 连通性自检（文本->向量 + pgvector 接收验证）：
```bash
go run ./cmd/embedding-smoke
```

## Prompt Builder（模块化）
- 架构流水线：`Module -> PolicyEngine -> TokenBudgeter -> Assembler -> Trace`
- 输入：`BuildRequest(user_id/session_id/user_message/now/options/persona/history)`
- 输出：`BuildResult(messages + trace)`，同一输入稳定产出相同结构。
- 模块优先级（高到低）：
  - `safety -> persona -> relationship -> boundaries -> profile -> preferences -> events -> rag -> emotion -> stm -> user_message`
- 冲突规则（已实现）：
  - `boundaries` 与 `rag` 冲突时，丢弃 `rag` block
- Token 预算（默认）：
  - 顶层：`system/memory/history = 35%/25%/40%`
  - 预留输出：`PROMPT_RESERVE_TOKENS=800`
- 裁剪策略（已实现）：
  - `hard` 按 block 属性优先保留（不是按比例裁）
  - `RAG` 走结构化降级链：去重 + `K` 递减（5→3→2→1）+ 紧凑化
  - `Profile` 走结构化降级链：topN 条目/核心字段，不做危险半截截断
  - `Emotion` 在预算紧张时降级为短模板或直接丢弃
  - `STM` 优先保留最近消息；旧消息聚合为 `EARLIER_STM_SUMMARY`
  - `stm_summary` 使用独立 `summary` bucket，避免与 profile/rag 直接抢预算
  - 启发式 token 估算增加 safety margin，并采用保守目标预算
- 可观测性：
  - 每次构建都有 `trace`（模块顺序、block 决策、预算使用、裁剪日志）

可通过环境变量调整预算：
```bash
PROMPT_MAX_TOKENS=3200
PROMPT_RESERVE_TOKENS=800
PROMPT_BUDGET_SYSTEM_RATIO=35
PROMPT_BUDGET_MEMORY_RATIO=25
PROMPT_BUDGET_HISTORY_RATIO=40
```
