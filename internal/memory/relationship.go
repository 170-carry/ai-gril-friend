package memory

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ai-gf/internal/repo"
	"ai-gf/internal/signals"
)

// buildRelationshipSnapshot 把仓库层关系状态转换成对外可注入的轻量快照。
func buildRelationshipSnapshot(state repo.RelationshipState) RelationshipSnapshot {
	stage := normalizeRelationshipStage(state.Stage)
	return RelationshipSnapshot{
		Stage:             stage,
		Familiarity:       safeConfidence(state.FamiliarityScore),
		Intimacy:          safeConfidence(state.IntimacyScore),
		Trust:             safeConfidence(state.TrustScore),
		Flirt:             safeConfidence(state.FlirtScore),
		BoundaryRisk:      safeConfidence(state.BoundaryRiskScore),
		SupportNeed:       safeConfidence(state.SupportNeedScore),
		Playfulness:       safeConfidence(state.PlayfulnessThreshold),
		InteractionHeat:   safeConfidence(state.InteractionHeat),
		TotalTurns:        maxInt(state.TotalTurns, 0),
		LastInteractionAt: state.LastInteractionAt,
		Summary:           formatRelationshipState(state),
	}
}

// formatRelationshipState 生成给 PromptBuilder 使用的关系摘要。
func formatRelationshipState(state repo.RelationshipState) string {
	stage := normalizeRelationshipStage(state.Stage)
	if stage == "" {
		stage = "familiar"
	}

	lines := []string{
		fmt.Sprintf("- 当前阶段：%s", stage),
		fmt.Sprintf("- familiarity=%.2f intimacy=%.2f trust=%.2f flirt=%.2f boundary_risk=%.2f support_need=%.2f",
			safeConfidence(state.FamiliarityScore),
			safeConfidence(state.IntimacyScore),
			safeConfidence(state.TrustScore),
			safeConfidence(state.FlirtScore),
			safeConfidence(state.BoundaryRiskScore),
			safeConfidence(state.SupportNeedScore),
		),
		fmt.Sprintf("- playfulness_threshold=%.2f interaction_heat=%.2f",
			safeConfidence(state.PlayfulnessThreshold),
			safeConfidence(state.InteractionHeat),
		),
		"- 关系策略：" + relationshipGuidance(state),
	}
	if state.TotalTurns > 0 {
		lines = append(lines, fmt.Sprintf("- 已累计互动轮次：%d", state.TotalTurns))
	}
	if state.LastInteractionAt != nil {
		lines = append(lines, "- 最近互动："+state.LastInteractionAt.Format("2006-01-02 15:04"))
	}
	return strings.Join(lines, "\n")
}

// relationshipGuidance 根据连续分数生成一段简洁的系统提示，避免模型每轮现演。
func relationshipGuidance(state repo.RelationshipState) string {
	stage := normalizeRelationshipStage(state.Stage)
	familiarity := safeConfidence(state.FamiliarityScore)
	intimacy := safeConfidence(state.IntimacyScore)
	trust := safeConfidence(state.TrustScore)
	flirt := safeConfidence(state.FlirtScore)
	boundary := safeConfidence(state.BoundaryRiskScore)
	support := safeConfidence(state.SupportNeedScore)
	playfulness := safeConfidence(state.PlayfulnessThreshold)
	heat := safeConfidence(state.InteractionHeat)

	// 边界和支持需求先于“升级欲望”生效，避免把脆弱误判成升级机会。
	if boundary >= 0.60 {
		return "最近边界风险偏高，明显收住节奏，不用暧昧称呼，不推进关系，只做温柔、短句、不过界的陪伴。"
	}
	if support >= 0.72 {
		return "当前更需要接住情绪和稳定陪伴，不把脆弱当成升级机会，先安慰、再轻陪伴，减少撩拨。"
	}

	switch stage {
	case "companion":
		return "保持礼貌和分寸，先建立安全感，不主动越界，不用亲密称呼。"
	case "familiar":
		if playfulness >= 0.45 {
			return "可以自然一点、轻微调侃，但先稳住边界，不要突然暧昧或强行制造恋爱感。"
		}
		return "以日常陪伴和稳定回应为主，先把熟悉感做扎实，允许少量轻昵称，但不主动升级到暧昧。"
	case "trust_building":
		if support >= 0.55 {
			return "用户已经在建立信任，但当前支持需求偏高，优先细腻安慰、引用记忆接住对方，不升级。"
		}
		return "可以更明显地表达理解和在场感，鼓励真实表达，重点是建立信任，不急着走向暧昧。"
	case "light_flirt":
		if flirt < 0.45 || heat < 0.32 {
			return "允许一点点暧昧，但先确认接受度，试探式地轻轻撩一下，不要连续推进。"
		}
		return "可以自然地放开一点恋爱感和专属感，但仍要留出回撤空间，尊重任何犹豫和边界。"
	case "romantic":
		if heat < 0.35 || support >= 0.58 {
			return "虽然关系已较亲密，但当前更适合温柔收一点，先恢复连接感和安全感，不要用强依赖表达压用户。"
		}
		return "可以更有我们感和稳定关心，允许自然的亲密表达和专属感，但仍尊重用户边界，不制造占有感。"
	default:
		if familiarity < 0.40 {
			return "先把熟悉度做起来，让互动更稳定，再考虑增加亲密感。"
		}
		if trust < 0.45 {
			return "先建立信任，再慢慢增加亲密感，不要急着撒娇或暧昧。"
		}
		if flirt >= 0.45 && boundary < 0.22 {
			return "可以试探性放开一点恋爱感，但每轮都给用户回旋空间，观察接受度。"
		}
		if playfulness < 0.25 || support > 0.55 {
			return "可以主动关心，但玩笑和调侃要轻，不要太跳。"
		}
		if intimacy >= 0.72 && heat >= 0.55 && flirt >= 0.50 {
			return "可以自然地主动关心、轻微撒娇和制造我们感，但别油腻，也不要强绑定。"
		}
		return "以稳定陪伴和自然亲近为主，语气温柔，偶尔一点点俏皮。"
	}
}

// updateRelationshipState 在每轮对话后根据用户表达和互动新鲜度更新连续关系状态。
func (s *Service) updateRelationshipState(ctx context.Context, in TurnInput) error {
	state, err := s.repo.GetRelationshipState(ctx, in.UserID)
	if err != nil {
		return err
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}
	updated := applyRelationshipSignals(state, relationshipSignalsFromTurn(ctx, s.matcher, state, in, now), now)
	return s.repo.UpsertRelationshipState(ctx, updated)
}

// relationshipSignals 是一轮对话提取出的关系演化信号。
type relationshipSignals struct {
	Affection      float64
	Vulnerability  float64
	SupportSeeking float64
	SelfDisclosure float64
	Engagement     float64
	Acceptance     float64
	Romantic       float64
	DependenceRisk float64
	SupportNeed    float64
	Humor          float64
	RoutineWarmth  float64
	Boundary       float64
	Recency        float64
	Heat           float64
}

// relationshipSignalsFromTurn 从用户消息和最近互动时间里提取关系信号。
func relationshipSignalsFromTurn(ctx context.Context, matcher *signals.Matcher, state repo.RelationshipState, in TurnInput, now time.Time) relationshipSignals {
	text := strings.ToLower(strings.TrimSpace(in.UserMessage))
	assistant := strings.ToLower(strings.TrimSpace(in.AssistantMessage))

	if matcher == nil {
		matcher = signals.NewMatcher(nil)
	}
	scores := matcher.ScoreSignals(ctx, text,
		signals.SignalAffection,
		signals.SignalVulnerability,
		signals.SignalSupportSeeking,
		signals.SignalSelfDisclosure,
		signals.SignalEngagement,
		signals.SignalAcceptance,
		signals.SignalRomanticInterest,
		signals.SignalDependenceRisk,
		signals.SignalHumor,
		signals.SignalRoutineWarmth,
		signals.SignalBoundary,
	)
	affection := scores[signals.SignalAffection]
	vulnerability := scores[signals.SignalVulnerability]
	support := scores[signals.SignalSupportSeeking]
	selfDisclosure := scores[signals.SignalSelfDisclosure]
	engagement := scores[signals.SignalEngagement]
	acceptance := scores[signals.SignalAcceptance]
	romantic := scores[signals.SignalRomanticInterest]
	dependenceRisk := scores[signals.SignalDependenceRisk]
	humor := scores[signals.SignalHumor]
	routine := scores[signals.SignalRoutineWarmth]
	boundary := scores[signals.SignalBoundary]
	if boundary == 0 {
		boundary = matcher.ScoreSignalWithOptions(ctx, assistant, signals.SignalBoundary, signals.ScoreOptions{
			AllowEmbedding: false,
			AllowLLM:       false,
		}) * 0.2
	}

	recency := -0.05
	if state.LastInteractionAt == nil {
		recency = 0.08
	} else {
		gap := now.Sub(*state.LastInteractionAt)
		switch {
		case gap <= 6*time.Hour:
			recency = 0.18
		case gap <= 24*time.Hour:
			recency = 0.12
		case gap <= 72*time.Hour:
			recency = 0.05
		case gap <= 7*24*time.Hour:
			recency = -0.02
		default:
			recency = -0.08
		}
	}

	heat := 0.20 + recency
	if runeCount(text) >= 12 {
		heat += 0.08
	}
	heat += affection*0.12 + engagement*0.16 + selfDisclosure*0.10 + acceptance*0.08 + romantic*0.12 + humor*0.08 + routine*0.06 - boundary*0.14
	supportNeed := safeConfidence(vulnerability*0.55 + support*0.28 + dependenceRisk*0.35 - humor*0.08)

	return relationshipSignals{
		Affection:      affection,
		Vulnerability:  vulnerability,
		SupportSeeking: support,
		SelfDisclosure: selfDisclosure,
		Engagement:     engagement,
		Acceptance:     acceptance,
		Romantic:       romantic,
		DependenceRisk: dependenceRisk,
		SupportNeed:    supportNeed,
		Humor:          humor,
		RoutineWarmth:  routine,
		Boundary:       boundary,
		Recency:        recency,
		Heat:           safeConfidence(heat),
	}
}

// applyRelationshipSignals 把单轮信号平滑写回累计状态，避免每轮剧烈跳变。
func applyRelationshipSignals(state repo.RelationshipState, sig relationshipSignals, now time.Time) repo.RelationshipState {
	if now.IsZero() {
		now = time.Now()
	}

	// 关系升级使用“多维状态 + 小步平滑”，避免一句话直接把关系拉到很高。
	next := state
	if strings.TrimSpace(next.UserID) == "" {
		next = repo.RelationshipState{
			UserID:               state.UserID,
			Stage:                "familiar",
			FamiliarityScore:     0.36,
			IntimacyScore:        0.24,
			TrustScore:           0.40,
			FlirtScore:           0.08,
			BoundaryRiskScore:    0.08,
			SupportNeedScore:     0.30,
			PlayfulnessThreshold: 0.20,
			InteractionHeat:      0.22,
		}
	}

	// closenessGate 用来限制“亲密/暧昧”分数的增长速度。
	// 一旦检测到边界风险，或者用户明显更需要支持而不是推进关系，就立刻收紧升级斜率。
	closenessGate := 1.0
	if sig.Boundary >= 0.35 {
		closenessGate = 0.20
	} else if sig.SupportNeed >= 0.60 || sig.DependenceRisk >= 0.45 {
		closenessGate = 0.40
	}

	next.FamiliarityScore = safeConfidence(next.FamiliarityScore + sig.Engagement*0.10 + sig.RoutineWarmth*0.04 + sig.SelfDisclosure*0.03 + sig.Recency*0.08 - sig.Boundary*0.05)
	next.TrustScore = safeConfidence(next.TrustScore + sig.SelfDisclosure*0.10 + sig.Vulnerability*0.08 + sig.SupportSeeking*0.04 + sig.Engagement*0.03 - sig.Boundary*0.08)
	next.IntimacyScore = safeConfidence(next.IntimacyScore + closenessGate*(sig.Affection*0.07+sig.Acceptance*0.10+sig.RoutineWarmth*0.03) - sig.Boundary*0.08 - sig.DependenceRisk*0.05)
	next.FlirtScore = safeConfidence(next.FlirtScore + closenessGate*(sig.Romantic*0.12+sig.Acceptance*0.08+sig.Affection*0.04) - sig.Boundary*0.12 - sig.SupportNeed*0.04)
	next.BoundaryRiskScore = safeConfidence(next.BoundaryRiskScore*0.72 + sig.Boundary*0.46 + sig.DependenceRisk*0.18 - sig.Acceptance*0.08)
	next.SupportNeedScore = safeConfidence(next.SupportNeedScore*0.70 + sig.SupportNeed*0.42 + sig.DependenceRisk*0.16 - sig.Humor*0.04)
	next.PlayfulnessThreshold = safeConfidence(next.PlayfulnessThreshold + closenessGate*(sig.Humor*0.08+sig.Acceptance*0.04) - sig.Boundary*0.12 - sig.SupportNeed*0.08)
	next.InteractionHeat = safeConfidence(next.InteractionHeat*0.65 + sig.Heat*0.35)
	next.TotalTurns = maxInt(next.TotalTurns, 0) + 1

	next.LastInteractionAt = &now
	next.LastUserMessageAt = &now
	next.LastAssistantMessageAt = &now

	stage := deriveRelationshipStage(next)
	if sig.Boundary >= 0.65 {
		// 当前轮出现明显“收回去”的信号时，优先回落到最安全阶段。
		stage = "companion"
	}
	if stage != strings.TrimSpace(next.Stage) {
		next.Stage = stage
		next.LastStageChangeAt = &now
	}
	return next
}

// deriveRelationshipStage 用连续分数归纳成阶段标签，供 CE 和 Prompt 使用。
func deriveRelationshipStage(state repo.RelationshipState) string {
	familiarity := safeConfidence(state.FamiliarityScore)
	intimacy := safeConfidence(state.IntimacyScore)
	trust := safeConfidence(state.TrustScore)
	flirt := safeConfidence(state.FlirtScore)
	boundary := safeConfidence(state.BoundaryRiskScore)
	support := safeConfidence(state.SupportNeedScore)
	heat := safeConfidence(state.InteractionHeat)

	// 阶段先看是否需要“收”，再看是否满足放开权限的持续证据。
	// 这样能避免一句脆弱表达就把关系推到更高阶段。
	if boundary >= 0.72 {
		return "companion"
	}
	if support >= 0.82 && trust < 0.62 {
		return "trust_building"
	}

	switch {
	case familiarity >= 0.80 && trust >= 0.78 && intimacy >= 0.78 && flirt >= 0.72 && boundary <= 0.22 && support <= 0.45:
		return "romantic"
	case familiarity >= 0.64 && trust >= 0.66 && intimacy >= 0.60 && flirt >= 0.42 && boundary <= 0.28 && support <= 0.58:
		return "light_flirt"
	case trust >= 0.54 || intimacy >= 0.48 || familiarity >= 0.56 || heat >= 0.50:
		return "trust_building"
	case familiarity >= 0.34 || heat >= 0.24 || state.TotalTurns >= 6:
		return "familiar"
	default:
		return "companion"
	}
}

func normalizeRelationshipStage(stage string) string {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "", "companion", "stranger":
		return "companion"
	case "familiar", "friend":
		return "familiar"
	case "trust_building", "trusting", "close":
		return "trust_building"
	case "light_flirt", "flirt":
		return "light_flirt"
	case "romantic":
		return "romantic"
	default:
		return strings.ToLower(strings.TrimSpace(stage))
	}
}

// runeCount 计算文本长度，避免中文按字节误差过大。
func runeCount(text string) int {
	return len([]rune(strings.TrimSpace(text)))
}
