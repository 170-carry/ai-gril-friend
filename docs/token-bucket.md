分桶与预算公式
promptCap   = MaxPromptTokens - ReserveTokens
nonUserCap  = promptCap - userTokens
system/memory/stm = 按 ratio(35/25/40) 分 nonUserCap
然后再细分成桶：

hard = system * 0.7
profile = memory * 0.45
rag = memory * 0.40
emotion = memory - profile - rag（最少 24）
stm = historyBudget
user = userTokens（用户输入必留）
优先级裁剪（核心顺序）
先经过 PolicyEngine，按 priority 升序排序（数值越小越高优先）
非 STM block 按当前顺序进入预算：
hard=true 直接保留
能放下就保留
放不下且可裁剪（redactable）就截断到剩余预算
仍不行则丢弃
STM 单独处理：按时间序（seq）从新到旧尝试放入预算，能放就留，放不下就丢
摘要接口（当前实现）
不是外部 API，是内部函数 summarizeSTM(...)
当 STM 有被丢弃时，生成 EARLIER_STM_SUMMARY：
最多采样 6 条被丢消息
每条裁到约 24 token
整体再按摘要预算裁剪
摘要以 stm_summary block 回填到 profile 桶
二次溢出裁剪
完成桶内分配后，若仍超 nonUserCap，进入 shrinkOverflow：
找最低价值候选（优先删低优先级；STM 更偏向删旧）
先尝试裁短到约 70%
不行就丢
最后再做 hardCapPrompt 兜底
当前策略特点（你现在实际行为）
HARD 和 USER 基本不动，优先保护
STM 优先保最近上下文，老内容变摘要
RAG/PROFILE/EMOTION 超预算会被裁或丢
token 估算是启发式（非官方 tokenizer）：中文按字符，ASCII 约 4 字符≈1 token；每条消息额外 +4 token