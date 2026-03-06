package ranking

import "time"

// CandidateKind 标记候选记忆来源类型。
type CandidateKind string

const (
	CandidateStructured CandidateKind = "structured"
	CandidateSemantic   CandidateKind = "semantic"
)

// Candidate 是排序前的记忆候选。
type Candidate struct {
	ID          string
	SourceID    int64
	Kind        CandidateKind
	Topic       string
	Content     string
	ContentShort string
	Similarity  float64
	Importance  int
	Confidence  float64
	Pinned      bool
	AccessCount int
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	Superseded  bool
}

// RankRequest 是排序请求输入。
type RankRequest struct {
	Now              time.Time
	UserMessage      string
	K                int
	InitialCandidates []Candidate
	BoundaryKeywords []string
}

// MemoryItem 是排序后可注入 prompt 的记忆条目。
type MemoryItem struct {
	ID          string
	SourceID    int64
	Kind        CandidateKind
	Topic       string
	Content     string
	Score       float64
	Similarity  float64
	Importance  int
	Confidence  float64
}

// TraceItem 记录单条候选在各阶段的状态，便于调试。
type TraceItem struct {
	CandidateID string      `json:"candidate_id"`
	SourceID    int64       `json:"source_id"`
	Kind        CandidateKind `json:"kind"`
	Topic       string      `json:"topic"`
	Generated   bool        `json:"generated"`
	Filtered    bool        `json:"filtered"`
	FilterReason string     `json:"filter_reason,omitempty"`
	Score       float64     `json:"score"`
	Selected    bool        `json:"selected"`
	Rank        int         `json:"rank,omitempty"`
	Notes       []string    `json:"notes,omitempty"`
}

// RankResult 是排序结果与 trace。
type RankResult struct {
	Memories []MemoryItem
	Trace    []TraceItem
}

// Config 是 MRS（Memory Ranking System）参数配置。
type Config struct {
	InitialTopK         int
	OutputK             int
	SimilarityThreshold float64
	DedupThreshold      float64
	TopicMaxPer         int
	MMRLambda           float64
	WSim                float64
	WImp                float64
	WRec                float64
	WUse                float64
	WPin                float64
	WRed                float64
}

// DefaultConfig 返回默认排序参数。
func DefaultConfig() Config {
	return Config{
		InitialTopK:         30,
		OutputK:             5,
		SimilarityThreshold: 0.75,
		DedupThreshold:      0.90,
		TopicMaxPer:         2,
		MMRLambda:           0.7,
		WSim:                0.45,
		WImp:                0.25,
		WRec:                0.15,
		WUse:                0.10,
		WPin:                0.20,
		WRed:                0.25,
	}
}

// Normalize 对参数做边界处理。
func (c Config) Normalize() Config {
	def := DefaultConfig()
	if c.InitialTopK <= 0 {
		c.InitialTopK = def.InitialTopK
	}
	if c.OutputK <= 0 {
		c.OutputK = def.OutputK
	}
	if c.SimilarityThreshold <= 0 || c.SimilarityThreshold > 1 {
		c.SimilarityThreshold = def.SimilarityThreshold
	}
	if c.DedupThreshold <= 0 || c.DedupThreshold > 1 {
		c.DedupThreshold = def.DedupThreshold
	}
	if c.TopicMaxPer <= 0 {
		c.TopicMaxPer = def.TopicMaxPer
	}
	if c.MMRLambda <= 0 || c.MMRLambda >= 1 {
		c.MMRLambda = def.MMRLambda
	}
	if c.WSim == 0 {
		c.WSim = def.WSim
	}
	if c.WImp == 0 {
		c.WImp = def.WImp
	}
	if c.WRec == 0 {
		c.WRec = def.WRec
	}
	if c.WUse == 0 {
		c.WUse = def.WUse
	}
	if c.WPin == 0 {
		c.WPin = def.WPin
	}
	if c.WRed == 0 {
		c.WRed = def.WRed
	}
	return c
}
