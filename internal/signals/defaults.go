package signals

import "regexp"

const (
	SignalAffection      = "affection"
	SignalVulnerability  = "vulnerability"
	SignalSupportSeeking = "support_seeking"
	SignalHumor          = "humor"
	SignalRoutineWarmth  = "routine_warmth"
	SignalBoundary       = "boundary"
	SignalAdviceSeeking  = "advice_seeking"
	SignalSmallTalk      = "small_talk"
	SignalStorySharing   = "story_sharing"
	SignalPlanningEvent  = "planning_event"
	SignalMetaProduct    = "meta_product"
	SignalSafety         = "safety"
	SignalSelfDisclosure = "self_disclosure"
	SignalEngagement     = "engagement"
	SignalAcceptance     = "acceptance_of_closeness"
	SignalRomanticInterest = "romantic_interest"
	SignalDependenceRisk = "dependence_risk"
)

type PhraseRule struct {
	Text   string
	Weight float64
}

type RegexRule struct {
	Pattern string
	Weight  float64
	re      *regexp.Regexp
}

type TemplateRule struct {
	Parts  []string
	MaxGap int
	Weight float64
}

type SignalRule struct {
	Key                 string
	Description         string
	Phrases             []PhraseRule
	Regexes             []RegexRule
	Templates           []TemplateRule
	EmbeddingSeeds      []string
	EmbeddingThreshold  float64
	EmbeddingMaxScore   float64
	EmbeddingMinLexical float64
	NegationSensitive   bool
	NegationWords       []string
	NegationWindow      int
}

type SignalBlend struct {
	Signal string
	Weight float64
}

type IntentRule struct {
	Key     string
	Signals []SignalBlend
}

type Catalog struct {
	Signals map[string]SignalRule
	Intents map[string]IntentRule
}

func DefaultCatalog() Catalog {
	return Catalog{
		Signals: defaultSignalRules(),
		Intents: defaultIntentRules(),
	}
}

func defaultSignalRules() map[string]SignalRule {
	negations := []string{"不", "没", "没有", "不是", "不再", "已经不", "不太", "不怎么", "没那么", "无需", "不用"}
	return map[string]SignalRule{
		SignalAffection: {
			Key:         SignalAffection,
			Description: "亲密、想念、撒娇、温柔关系拉近表达。",
			Phrases: []PhraseRule{
				phrase("想你", 0.42),
				phrase("抱抱", 0.40),
				phrase("贴贴", 0.40),
				phrase("亲亲", 0.40),
				phrase("爱你", 0.45),
				phrase("喜欢你", 0.42),
				phrase("陪着你", 0.36),
				phrase("在你身边", 0.36),
			},
			Regexes: []RegexRule{
				regex(`(?:好想|很想)\s*你`, 0.46),
				regex(`(?:早安|晚安)(?:呀|啦|哦|喔|~)?`, 0.24),
			},
			Templates: []TemplateRule{
				template(0.34, 4, "想", "抱抱"),
				template(0.34, 4, "想", "亲亲"),
			},
			EmbeddingSeeds:      []string{"好想你呀", "想抱抱你", "今天也想和你说晚安"},
			EmbeddingThreshold:  0.78,
			EmbeddingMaxScore:   0.55,
			EmbeddingMinLexical: 0.34,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalVulnerability: {
			Key:         SignalVulnerability,
			Description: "脆弱、难受、焦虑、低落、睡不好等情绪困扰表达。",
			Phrases: []PhraseRule{
				phrase("焦虑", 0.40),
				phrase("难受", 0.38),
				phrase("委屈", 0.38),
				phrase("崩溃", 0.42),
				phrase("害怕", 0.38),
				phrase("失眠", 0.38),
				phrase("紧张", 0.38),
				phrase("低落", 0.38),
				phrase("孤独", 0.38),
				phrase("想哭", 0.42),
				phrase("压力大", 0.42),
				phrase("心累", 0.40),
			},
			Regexes: []RegexRule{
				regex(`(?:睡不着|睡不太着|睡不好)`, 0.42),
				regex(`(?:撑不住|顶不住|扛不住)`, 0.44),
				regex(`心里(?:发慌|发空|发紧|堵得慌)`, 0.44),
			},
			Templates: []TemplateRule{
				template(0.42, 3, "想", "哭"),
				template(0.42, 3, "压力", "大"),
				template(0.40, 4, "睡", "不着"),
			},
			EmbeddingSeeds:      []string{"整个人有点撑不住", "心里空落落的", "最近总是睡不着"},
			EmbeddingThreshold:  0.76,
			EmbeddingMaxScore:   0.62,
			EmbeddingMinLexical: 0.40,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalSupportSeeking: {
			Key:         SignalSupportSeeking,
			Description: "明确向对方请求陪伴、倾听、安慰或帮助。",
			Phrases: []PhraseRule{
				phrase("陪我", 0.42),
				phrase("你在吗", 0.34),
				phrase("帮帮我", 0.44),
				phrase("可以陪我", 0.46),
				phrase("听我说", 0.44),
				phrase("想找你聊聊", 0.46),
				phrase("说说话", 0.34),
			},
			Regexes: []RegexRule{
				regex(`(?:可以|可不可以|能不能|能).{0,6}(?:陪|听).{0,4}我`, 0.46),
				regex(`(?:想|想要|想找你).{0,6}(?:聊聊|说说话|说一说)`, 0.44),
			},
			Templates: []TemplateRule{
				template(0.42, 4, "陪", "我"),
				template(0.44, 6, "听", "我说"),
				template(0.44, 6, "找", "你", "聊"),
				template(0.40, 6, "跟", "你", "说"),
			},
			EmbeddingSeeds:      []string{"可以陪我待一会吗", "你能听我讲讲吗", "想找个人说说话"},
			EmbeddingThreshold:  0.75,
			EmbeddingMaxScore:   0.60,
			EmbeddingMinLexical: 0.38,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalHumor: {
			Key:         SignalHumor,
			Description: "明显的玩笑、笑闹、轻松调侃或发笑表达。",
			Phrases: []PhraseRule{
				phrase("哈哈", 0.26),
				phrase("hh", 0.22),
				phrase("hhh", 0.24),
				phrase("笑死", 0.34),
				phrase("逗你", 0.28),
				phrase("开玩笑", 0.26),
				phrase("好好笑", 0.30),
				phrase("乐死", 0.32),
			},
			Regexes: []RegexRule{
				regex(`哈{2,}`, 0.32),
				regex(`笑死我了`, 0.40),
			},
			Templates: []TemplateRule{
				template(0.30, 4, "开", "玩笑"),
			},
			EmbeddingSeeds:      []string{"真的笑死我了", "哈哈哈哈太好笑了"},
			EmbeddingThreshold:  0.78,
			EmbeddingMaxScore:   0.45,
			EmbeddingMinLexical: 0.26,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      4,
		},
		SignalRoutineWarmth: {
			Key:         SignalRoutineWarmth,
			Description: "日常问候、惦记、报平安和轻度陪伴感表达。",
			Phrases: []PhraseRule{
				phrase("早安", 0.28),
				phrase("晚安", 0.28),
				phrase("吃饭了吗", 0.34),
				phrase("到家了", 0.34),
				phrase("在干嘛", 0.30),
				phrase("今天怎么样", 0.32),
				phrase("起床了", 0.28),
				phrase("睡了吗", 0.30),
			},
			Regexes: []RegexRule{
				regex(`(?:早安|晚安)(?:呀|啦|哦|喔|~)?`, 0.30),
				regex(`(?:吃饭了吗|到家了吗|睡了吗)`, 0.34),
			},
			Templates: []TemplateRule{
				template(0.32, 4, "今天", "怎么样"),
				template(0.34, 4, "到家", "了"),
			},
			EmbeddingSeeds:      []string{"到家了吗记得跟我说一声", "早安呀今天怎么样"},
			EmbeddingThreshold:  0.77,
			EmbeddingMaxScore:   0.45,
			EmbeddingMinLexical: 0.28,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      4,
		},
		SignalBoundary: {
			Key:         SignalBoundary,
			Description: "收边界、拒绝、降温、要求停止某种互动。",
			Phrases: []PhraseRule{
				phrase("别这样", 0.46),
				phrase("别闹", 0.42),
				phrase("别撒娇", 0.44),
				phrase("不想聊", 0.48),
				phrase("别问", 0.48),
				phrase("不要提", 0.48),
				phrase("太肉麻", 0.40),
				phrase("离我远点", 0.48),
				phrase("先这样", 0.36),
				phrase("别再提", 0.48),
			},
			Regexes: []RegexRule{
				regex(`(?:别|不要|先别).{0,4}(?:问|提|聊)`, 0.48),
				regex(`(?:太|有点)肉麻`, 0.38),
			},
			Templates: []TemplateRule{
				template(0.48, 4, "不想", "聊"),
				template(0.48, 4, "别", "问"),
				template(0.48, 4, "不要", "提"),
			},
			EmbeddingSeeds:      []string{"这个话题先别聊了", "太肉麻了收一点", "离我远一点"},
			EmbeddingThreshold:  0.74,
			EmbeddingMaxScore:   0.62,
			EmbeddingMinLexical: 0.42,
			NegationSensitive:   false,
		},
		SignalAdviceSeeking: {
			Key:         SignalAdviceSeeking,
			Description: "请求建议、方案、步骤或行动判断。",
			Phrases: []PhraseRule{
				phrase("怎么办", 0.46),
				phrase("怎么做", 0.44),
				phrase("建议", 0.40),
				phrase("方案", 0.36),
				phrase("步骤", 0.34),
				phrase("帮我想", 0.42),
				phrase("如何", 0.36),
				phrase("要不要", 0.34),
				phrase("能不能", 0.34),
			},
			Regexes: []RegexRule{
				regex(`(?:该|要).{0,4}怎么办`, 0.46),
				regex(`给我.{0,4}(?:建议|方案|步骤)`, 0.42),
			},
			Templates: []TemplateRule{
				template(0.42, 4, "帮", "我想"),
			},
			EmbeddingSeeds:      []string{"我该怎么做比较好", "给我一个可执行步骤"},
			EmbeddingThreshold:  0.77,
			EmbeddingMaxScore:   0.55,
			EmbeddingMinLexical: 0.34,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalSmallTalk: {
			Key:         SignalSmallTalk,
			Description: "轻量打招呼、闲聊开场、陪伴式寒暄。",
			Phrases: []PhraseRule{
				phrase("在吗", 0.34),
				phrase("你好", 0.28),
				phrase("哈喽", 0.30),
				phrase("哈哈", 0.22),
				phrase("聊聊", 0.28),
				phrase("最近怎么样", 0.34),
			},
			Regexes: []RegexRule{
				regex(`(?:在吗|哈喽|hello)`, 0.34),
			},
			Templates: []TemplateRule{
				template(0.34, 4, "最近", "怎么样"),
				template(0.28, 4, "随便", "聊"),
			},
			EmbeddingSeeds:      []string{"在吗想跟你随便聊聊", "最近怎么样呀"},
			EmbeddingThreshold:  0.78,
			EmbeddingMaxScore:   0.42,
			EmbeddingMinLexical: 0.24,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      4,
		},
		SignalStorySharing: {
			Key:         SignalStorySharing,
			Description: "讲述今天发生的事、经历或想分享的故事。",
			Phrases: []PhraseRule{
				phrase("我今天", 0.34),
				phrase("刚刚", 0.30),
				phrase("发生", 0.28),
				phrase("经历", 0.28),
				phrase("想跟你说", 0.42),
				phrase("告诉你", 0.34),
			},
			Regexes: []RegexRule{
				regex(`(?:我今天|刚刚).{0,10}(?:发生|遇到|经历)`, 0.42),
				regex(`(?:想跟你说|告诉你).{0,10}(?:件事|一个事)`, 0.44),
			},
			Templates: []TemplateRule{
				template(0.40, 6, "告诉", "你"),
			},
			EmbeddingSeeds:      []string{"想跟你说一件事", "刚刚发生了个事情"},
			EmbeddingThreshold:  0.77,
			EmbeddingMaxScore:   0.52,
			EmbeddingMinLexical: 0.32,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalPlanningEvent: {
			Key:         SignalPlanningEvent,
			Description: "提到未来安排、时间点、提醒、日程或计划。",
			Phrases: []PhraseRule{
				phrase("明天", 0.34),
				phrase("下周", 0.34),
				phrase("今晚", 0.28),
				phrase("面试", 0.40),
				phrase("会议", 0.34),
				phrase("提醒", 0.34),
				phrase("日程", 0.32),
				phrase("安排", 0.32),
				phrase("计划", 0.30),
			},
			Regexes: []RegexRule{
				regex(`(?:明天|后天|下周|今晚|明早|下午).{0,10}(?:面试|会议|约会|安排|计划)`, 0.46),
				regex(`\d{1,2}\s*(?:点|号|:\d{2})`, 0.34),
			},
			Templates: []TemplateRule{
				template(0.42, 8, "提醒", "我"),
			},
			EmbeddingSeeds:      []string{"明天下午有个面试", "下周有个安排想提醒我"},
			EmbeddingThreshold:  0.76,
			EmbeddingMaxScore:   0.58,
			EmbeddingMinLexical: 0.34,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      5,
		},
		SignalMetaProduct: {
			Key:         SignalMetaProduct,
			Description: "询问产品、模型、系统、能力或工作方式。",
			Phrases: []PhraseRule{
				phrase("你是谁", 0.46),
				phrase("你能做什么", 0.46),
				phrase("怎么工作", 0.42),
				phrase("模型", 0.30),
				phrase("prompt", 0.30),
				phrase("系统", 0.28),
			},
			Regexes: []RegexRule{
				regex(`(?:你是(?:谁|什么)|你能做什么|怎么工作)`, 0.46),
			},
			Templates: []TemplateRule{
				template(0.42, 6, "你", "能做什么"),
				template(0.38, 6, "什么", "模型"),
			},
			EmbeddingSeeds:      []string{"你是什么模型", "你能做什么"},
			EmbeddingThreshold:  0.80,
			EmbeddingMaxScore:   0.48,
			EmbeddingMinLexical: 0.30,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      4,
		},
		SignalSafety: {
			Key:         SignalSafety,
			Description: "自伤、自杀、结束生命等高风险安全表达。",
			Phrases: []PhraseRule{
				phrase("轻生", 0.62),
				phrase("自杀", 0.64),
				phrase("伤害自己", 0.62),
				phrase("结束生命", 0.66),
			},
			Regexes: []RegexRule{
				regex(`(?:不想活了|活着没意思|想结束生命)`, 0.66),
			},
			Templates: []TemplateRule{
				template(0.62, 6, "伤害", "自己"),
				template(0.66, 6, "不想", "活"),
			},
			EmbeddingSeeds:      []string{"我不想活了", "有点想伤害自己"},
			EmbeddingThreshold:  0.78,
			EmbeddingMaxScore:   0.70,
			EmbeddingMinLexical: 0.48,
			NegationSensitive:   false,
		},
		SignalSelfDisclosure: {
			Key:         SignalSelfDisclosure,
			Description: "用户主动暴露隐私、脆弱经历、家庭背景或不轻易说的内心内容。",
			Phrases: []PhraseRule{
				phrase("其实我", 0.24),
				phrase("我一直", 0.20),
				phrase("小时候", 0.42),
				phrase("童年", 0.42),
				phrase("家里", 0.28),
				phrase("前任", 0.32),
				phrase("秘密", 0.40),
				phrase("不敢跟别人说", 0.46),
				phrase("只跟你说", 0.46),
			},
			Regexes: []RegexRule{
				regex(`(?:其实|说实话|坦白说).{0,12}(?:我|自己)`, 0.30),
				regex(`(?:不太敢|不敢).{0,8}(?:跟别人说|告诉别人)`, 0.46),
			},
			Templates: []TemplateRule{
				template(0.42, 6, "只", "跟你说"),
				template(0.42, 6, "不敢", "别人说"),
			},
			EmbeddingSeeds:      []string{"这些话我一般不会跟别人说", "我想把很私人的事告诉你", "小时候的事情我很少提"},
			EmbeddingThreshold:  0.76,
			EmbeddingMaxScore:   0.62,
			EmbeddingMinLexical: 0.32,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalEngagement: {
			Key:         SignalEngagement,
			Description: "用户主动来找你、主动续聊、主动把注意力放回你身上。",
			Phrases: []PhraseRule{
				phrase("我又来找你了", 0.46),
				phrase("想继续聊", 0.40),
				phrase("还想跟你说", 0.42),
				phrase("第一个想到你", 0.46),
				phrase("一有空就来找你", 0.48),
			},
			Regexes: []RegexRule{
				regex(`(?:又来找你|来找你聊聊|继续聊)`, 0.42),
				regex(`(?:一有空|第一时间).{0,6}(?:想到你|来找你)`, 0.46),
			},
			Templates: []TemplateRule{
				template(0.40, 6, "继续", "聊"),
				template(0.46, 8, "想到", "你"),
			},
			EmbeddingSeeds:      []string{"我又想来找你说说话", "一有空就会想到你", "还想继续跟你聊"},
			EmbeddingThreshold:  0.77,
			EmbeddingMaxScore:   0.55,
			EmbeddingMinLexical: 0.32,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalAcceptance: {
			Key:         SignalAcceptance,
			Description: "用户对亲昵称呼、轻暧昧、被哄、被撩表现出接受或正反馈。",
			Phrases: []PhraseRule{
				phrase("这样叫我也可以", 0.46),
				phrase("被你哄到了", 0.48),
				phrase("继续", 0.20),
				phrase("我喜欢你这么说", 0.48),
				phrase("被你撩到了", 0.48),
				phrase("这样也挺好", 0.34),
			},
			Regexes: []RegexRule{
				regex(`(?:你这么说|这样叫我).{0,6}(?:也可以|我喜欢|挺好)`, 0.46),
				regex(`(?:被你哄到了|被你撩到了)`, 0.48),
			},
			Templates: []TemplateRule{
				template(0.46, 6, "喜欢", "这么说"),
				template(0.46, 6, "这样叫我", "可以"),
			},
			EmbeddingSeeds:      []string{"你这样叫我我会开心", "被你这么哄一下还挺受用", "你继续这样说也没关系"},
			EmbeddingThreshold:  0.77,
			EmbeddingMaxScore:   0.60,
			EmbeddingMinLexical: 0.34,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalRomanticInterest: {
			Key:         SignalRomanticInterest,
			Description: "用户对恋爱、在一起、双向暧昧或明显 romantic 关系表现兴趣。",
			Phrases: []PhraseRule{
				phrase("喜欢你", 0.40),
				phrase("想和你在一起", 0.52),
				phrase("想谈恋爱", 0.46),
				phrase("你真会撩", 0.28),
				phrase("想亲你", 0.48),
				phrase("想跟你谈恋爱", 0.54),
			},
			Regexes: []RegexRule{
				regex(`(?:想和你在一起|想跟你谈恋爱)`, 0.54),
				regex(`(?:你真会撩|好会撩)`, 0.28),
			},
			Templates: []TemplateRule{
				template(0.52, 6, "和你", "在一起"),
				template(0.48, 6, "谈", "恋爱"),
			},
			EmbeddingSeeds:      []string{"我想和你在一起", "如果可以的话我想跟你谈恋爱", "你有点让我心动"},
			EmbeddingThreshold:  0.78,
			EmbeddingMaxScore:   0.66,
			EmbeddingMinLexical: 0.34,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
		SignalDependenceRisk: {
			Key:         SignalDependenceRisk,
			Description: "高情绪依赖或过度绑定风险，应该转为稳定支持而不是升级关系。",
			Phrases: []PhraseRule{
				phrase("只有你陪我", 0.54),
				phrase("只有你了", 0.56),
				phrase("离不开你", 0.60),
				phrase("没有你不行", 0.60),
				phrase("只剩你了", 0.60),
			},
			Regexes: []RegexRule{
				regex(`(?:只有你|只剩你).{0,6}(?:陪我|懂我|了)`, 0.58),
				regex(`(?:离不开你|没有你不行)`, 0.60),
			},
			Templates: []TemplateRule{
				template(0.58, 6, "只有", "你"),
				template(0.60, 6, "离不开", "你"),
			},
			EmbeddingSeeds:      []string{"最近好像只有你能陪我了", "我感觉自己越来越离不开你", "没有你我就不知道怎么办"},
			EmbeddingThreshold:  0.78,
			EmbeddingMaxScore:   0.70,
			EmbeddingMinLexical: 0.40,
			NegationSensitive:   true,
			NegationWords:       negations,
			NegationWindow:      6,
		},
	}
}

func defaultIntentRules() map[string]IntentRule {
	return map[string]IntentRule{
		"emotional_support": {
			Key: "emotional_support",
			Signals: []SignalBlend{
				{Signal: SignalVulnerability, Weight: 1.0},
				{Signal: SignalSupportSeeking, Weight: 0.85},
			},
		},
		"advice_problem_solving": {
			Key: "advice_problem_solving",
			Signals: []SignalBlend{
				{Signal: SignalAdviceSeeking, Weight: 1.0},
			},
		},
		"small_talk": {
			Key: "small_talk",
			Signals: []SignalBlend{
				{Signal: SignalSmallTalk, Weight: 1.0},
				{Signal: SignalHumor, Weight: 0.25},
				{Signal: SignalRoutineWarmth, Weight: 0.20},
			},
		},
		"story_sharing": {
			Key: "story_sharing",
			Signals: []SignalBlend{
				{Signal: SignalStorySharing, Weight: 1.0},
			},
		},
		"planning_event": {
			Key: "planning_event",
			Signals: []SignalBlend{
				{Signal: SignalPlanningEvent, Weight: 1.0},
			},
		},
		"relationship_intimacy": {
			Key: "relationship_intimacy",
			Signals: []SignalBlend{
				{Signal: SignalAffection, Weight: 1.0},
				{Signal: SignalRoutineWarmth, Weight: 0.25},
			},
		},
		"boundary_safety": {
			Key: "boundary_safety",
			Signals: []SignalBlend{
				{Signal: SignalBoundary, Weight: 1.0},
				{Signal: SignalSafety, Weight: 1.2},
			},
		},
		"meta_product": {
			Key: "meta_product",
			Signals: []SignalBlend{
				{Signal: SignalMetaProduct, Weight: 1.0},
			},
		},
	}
}

func phrase(text string, weight float64) PhraseRule {
	return PhraseRule{
		Text:   text,
		Weight: weight,
	}
}

func regex(pattern string, weight float64) RegexRule {
	return RegexRule{
		Pattern: pattern,
		Weight:  weight,
		re:      regexp.MustCompile(pattern),
	}
}

func template(weight float64, maxGap int, parts ...string) TemplateRule {
	return TemplateRule{
		Parts:  parts,
		MaxGap: maxGap,
		Weight: weight,
	}
}
