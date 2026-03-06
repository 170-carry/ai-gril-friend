package extractor

import "time"

// Request 是记忆抽取输入。
type Request struct {
	UserID              string
	UserMessage         string
	AssistantMessage    string
	ConversationContext string
	Now                 time.Time
}

// PreferenceMemory 表示偏好类记忆。
type PreferenceMemory struct {
	Category   string
	Value      string
	Confidence float64
	Importance int
}

// BoundaryMemory 表示边界/雷区记忆。
type BoundaryMemory struct {
	Topic       string
	Description string
	Confidence  float64
	Importance  int
}

// EventMemory 表示未来事件记忆。
type EventMemory struct {
	Title      string
	EventTime  time.Time
	Importance int
	Confidence float64
}

// FactMemory 表示稳定事实记忆。
type FactMemory struct {
	Key        string
	Value      string
	Confidence float64
	Importance int
}

// Result 是抽取统一输出结构。
type Result struct {
	Preferences []PreferenceMemory
	Boundaries  []BoundaryMemory
	Events      []EventMemory
	Facts       []FactMemory
}
