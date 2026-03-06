package ranking

import "context"

// Ranker 实现 Memory Ranking System（候选生成->过滤->评分->多样性选择->压缩）。
type Ranker struct {
	cfg Config
}

// NewRanker 创建记忆排序器。
func NewRanker(cfg Config) *Ranker {
	return &Ranker{cfg: cfg.Normalize()}
}

// Rank 执行完整排序流程，输出可注入记忆和可解释 trace。
func (r *Ranker) Rank(ctx context.Context, req RankRequest) RankResult {
	_ = ctx
	if req.K <= 0 {
		req.K = r.cfg.OutputK
	}

	traces := newTraceBook()
	candidates := r.generateCandidates(req, traces)
	filtered := r.filterCandidates(req, candidates, traces)
	scored := r.scoreCandidates(req, filtered, traces)
	selected := r.selectWithMMR(req, scored, traces)
	memories := r.compressMemories(selected)

	return RankResult{
		Memories: memories,
		Trace:    traces.toSlice(),
	}
}
