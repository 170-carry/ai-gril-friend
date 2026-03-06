package prompt

import (
	"context"
	"strings"

	"ai-gf/internal/llm"
)

// defaultAssembler 负责将 block 合成为模型请求 messages。
type defaultAssembler struct{}

// newDefaultAssembler 创建默认组装器。
func newDefaultAssembler() Assembler {
	return &defaultAssembler{}
}

// Assemble 将 system/history/user 三段按模型可读顺序组装。
func (a *defaultAssembler) Assemble(ctx context.Context, req BuildRequest, blocks []PromptBlock, trace *BuildTrace) []llm.Message {
	systemSections := make([]string, 0, 16)
	history := make([]llm.Message, 0, len(blocks))
	userMessages := make([]llm.Message, 0, 1)

	for _, block := range blocks {
		switch block.Kind {
		case MessageKindSystem, MessageKindDeveloper:
			// system/developer block 会拼接成一条 system message，保留模块标签。
			section := "[" + strings.ToUpper(block.ID) + "]\n" + strings.TrimSpace(block.Content)
			systemSections = append(systemSections, section)
		case MessageKindAssistant:
			history = append(history, llm.Message{Role: "assistant", Content: block.Content})
		case MessageKindUser:
			if block.Bucket == BucketUser {
				userMessages = append(userMessages, llm.Message{Role: "user", Content: block.Content})
			} else {
				history = append(history, llm.Message{Role: "user", Content: block.Content})
			}
		}
	}

	messages := make([]llm.Message, 0, len(history)+2)
	if len(systemSections) > 0 {
		messages = append(messages, llm.Message{
			Role:    "system",
			Content: strings.Join(systemSections, "\n\n"),
		})
	}
	messages = append(messages, history...)
	messages = append(messages, userMessages...)

	return messages
}
