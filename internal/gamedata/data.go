package gamedata

import (
	"embed"
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"

	"turnrpg/internal/xym"
)

// 静态数据：pet / magic / suit / talent / talentset 五张表是 data/*.csv（可手改的唯一数据源，
// 加载见 data_csv.go，字段见 docs/DATA.md）；其余 data/*.json 由 analysis/tools/export_tables.py
// 从客户端配置表和官方抓包导出，改数据请重新导出。
//
//go:embed data/*.json data/*.csv
var dataFS embed.FS

// PetInfo 是一种卡牌的静态数据（client2 auto/pet.lua）。
type PetInfo struct {
	Type        int32  `json:"type"`
	Name        string `json:"name"`
	Race        int    `json:"race"` // 1 人 2 魔 3 仙 4 兽（config.lua RACE_NAME）
	Star        int    `json:"star"`
	Cost        int    `json:"cost"` // 上阵所需领导力
	HPA         int    `json:"hp_a"`
	HPB         int    `json:"hp_b"`
	AttA        int    `json:"att_a"`
	AttB        int    `json:"att_b"`
	Speed       int    `json:"speed"`
	Magic       int32  `json:"magic"`
	LeaderSkill int32  `json:"leaderskill"`
	// Talents 是三个神通槽，每槽按等级排的天赋号（卡牌的 tlgradeN 为该槽等级，1 起）。
	Talents [3][]int32 `json:"talents"`
	// ExTalents：第四神通的三个候选（各级神通号；客户端 ex_talent1-3，只有五星卡有；pet.csv ex_talent1-3）
	ExTalents [3][]int32 `json:"-"`
	Special   bool       `json:"-"`      // 稀有卡（客户端 is_special；化形元神价、仙灵热点等用）
	Upgrade   int32      `json:"-"`      // 飞升后的卡类型（客户端 pet_upgrade_relation；0 = 不能飞升）
	Pools     []string   `json:"pools"`  // 可招募的池（from 字段「【银两、友情点…】招募」）
	Relate    []int32    `json:"relate"` // 缘分号（relate.csv）
}

// HP / Att：客户端公式 LV*a/1000+b 取整。官方服的数值还叠加了天赋点、洞府修为等加成
// （抓包里 1 级卡约高 2%-5%），这些系统接入前按基础值算。
func (pi *PetInfo) HP(grade int) int32  { return int32(grade*pi.HPA/1000 + pi.HPB) }
func (pi *PetInfo) Att(grade int) int32 { return int32(grade*pi.AttA/1000 + pi.AttB) }

// GateInfo 是一个关卡的普通 / 精英两套配置（auto/gate.lua，精英为 jy_ 前缀）。
type GateInfo struct {
	Task int    `json:"task"`
	Name string `json:"name"`
	Mode [2]GateMode
}

type GateMode struct {
	Cost   int   // 体力
	CA     int32 // 阵型 fight_ca
	Param  int   // 怪物等级参数：属性 = base + add*param/1000
	Reward int32
	Limit  int // 每日次数
	// Achieve 是成就挑战（2 星、3 星）的成就号（AchieveInfo）；客户端 gate.lua achieve1 / achieve2（精英 jy_ 前缀）。
	Achieve [2]int32
}

// AchieveInfo 是一个成就条件（auto/tachieve.lua）：胜利且回合数 < Roundless、阵亡数 < Dieless（0 = 不限）、
// 上阵数 < Countless（0 = 不限）；种族 / 星级要求（"数量,种族"）客户端选阵时已校验。
type AchieveInfo struct {
	Roundless    int    `json:"roundless"`
	Dieless      int    `json:"dieless"`
	Countless    int    `json:"countless"`
	NoHelp       int    `json:"nohelp"`
	NoRep        int    `json:"norep"`
	OtRaceCount  string `json:"otracecount"`
	RaceLess     string `json:"raceless"`
	OtClassCount string `json:"otclasscount"`
	Reward       int32  `json:"reward"`
}

func (g *GateInfo) UnmarshalJSON(b []byte) error {
	var r struct {
		Task     int      `json:"task"`
		Name     string   `json:"name"`
		Cost     int      `json:"cost"`
		CA       int32    `json:"ca"`
		Param    int      `json:"param"`
		Reward   int32    `json:"reward"`
		Limit    int      `json:"limit"`
		JyCost   int      `json:"jy_cost"`
		JyCA     int32    `json:"jy_ca"`
		JyParam  int      `json:"jy_param"`
		JyReward int32    `json:"jy_reward"`
		JyLimit  int      `json:"jy_limit"`
		Achieve  [2]int32 `json:"achieve"`
		JyAch    [2]int32 `json:"jy_achieve"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	g.Task, g.Name = r.Task, r.Name
	g.Mode[0] = GateMode{r.Cost, r.CA, r.Param, r.Reward, r.Limit, r.Achieve}
	g.Mode[1] = GateMode{r.JyCost, r.JyCA, r.JyParam, r.JyReward, r.JyLimit, r.JyAch}
	return nil
}

// NpcInfo 是一个怪物（auto/fight_npc.lua）。
type NpcInfo struct {
	Shape  int32  `json:"shape"`
	Name   string `json:"name"`
	Race   int    `json:"race"`
	HP     int    `json:"hp"`
	HPAdd  int    `json:"hpadd"`
	Att    int    `json:"att"`
	AttAdd int    `json:"attadd"`
	Speed  int    `json:"speed"`
	Grade  int    `json:"grade"` // 参与法术伤害上限（见 MagicInfo）

	Talents []int32 `json:"talents"`
	Magic   int32   `json:"magic"`
	MagicLv int     `json:"magic_lv"`
}

// ItemInfo 是一种道具（auto/item.lua）。kind：0 不能使用，3 多选一（choose 为可选的奖励号），
// 4 碎片合成；etc 非 0 表示使用后发放该奖励（礼包）。
type ItemInfo struct {
	Name    string  `json:"name"`
	Kind    int     `json:"kind"`
	Overlap int32   `json:"overlap"` // 每格堆叠上限
	Value   int     `json:"value"`
	Etc     int32   `json:"etc"`
	Choose  []int32 `json:"choose"`
	Quality int     `json:"quality"`
	Cash    int     `json:"cash"` // 银锭类：使用后得的银两（描述「价值N两银子」）
}

// SuitInfo 是一件装备（auto/suit.lua）：武器 / 衣服 / 鞋，加攻击或生命，加成 = add[0] + add[1]*强化等级。
type SuitInfo struct {
	Name string     `json:"name"`
	Star int        `json:"star"`
	Kind string     `json:"kind"` // ITK_WEAPON / ITK_SHIRT / ITK_SHOE
	Prop string     `json:"prop"` // att / hp
	Add  [2]float64 `json:"add"`
	// Speed 是战靴加的先攻（suit.csv speed；logic/pet_prop.lua：鞋子的 speed 直接加到先攻上）
	Speed int `json:"speed"`
}

// Bonus 是该装备在 grade 级时的属性加成（suit_mgr.calcSuitAddProp）。
func (si *SuitInfo) Bonus(grade int) int32 { return int32(si.Add[0] + si.Add[1]*float64(grade)) }

// TalentInfo 是一个神通（auto/talent.lua）：同一族（Family，如「闪避」）按等级有多条，参数不同。
type TalentInfo struct {
	Family     string    `json:"f"`
	TJParam    []float64 `json:"-"` // 激发参数（talent_mgr.getTianjiangDesc 的 a、b…）
	FamilyID   int32     `json:"-"` // 族号 = 该族最小的神通号（代码按族号判断，族名只用于显示）；无族名为 0
	Param      []float64 `json:"p"`
	Buff       int32     `json:"buff"`
	BuffRate   int       `json:"buff_rate"`
	BuffTarget int       `json:"buff_target"`
	Need       int       `json:"-"` // 升到下一级要的神通点（talent.csv need）
}

// MagicInfo 是一个法术（auto/magic.lua）。线性函数存成 [a, b]，值 = a + b*LV。
type MagicInfo struct {
	Name     string     `json:"name"`
	Star     int        `json:"star"`
	Kind     int        `json:"kind"`   // 1 土 2 水 3 火 4 雷（属性伤害），5 治疗，0 物理
	Target   int        `json:"target"` // 目标规则，见 magicTargets
	Hurt     [2]float64 `json:"hurt"`   // 伤害/治疗的千分比
	Rate     int        `json:"rate"`   // 每次行动的施放概率 %
	Times    int        `json:"times"`  // 每场次数
	CondZY   int        `json:"cond_zy"`
	CostZY   [2]float64 `json:"cost_zy"`
	SP       [3]int     `json:"sp"` // 特效：类型、基值、每级增长/10
	Buff     int32      `json:"buff"`
	BuffRate [2]float64 `json:"buff_rate"`
	JXCost   int        `json:"jingxiu_cost"` // 精修每次消耗的元素数基数（客户端 getCostNum：⌊次数²×cost/10⌋）
	JXBuff   int32      `json:"jingxiu_buff"` // 精修效果的 buff（buff.json；0 = 不能精修）
	// CondRound：只在这些回合施放（空 = 不限，客户端 cond_round）
	CondRound []int `json:"cond_round"`
	// ExtSP：附加特效（ext_sp_type / ext_sp_param / ext_sp_grow，与 SP 同义：类型、基值、每级增长/10）
	ExtSP [][3]int `json:"ext_sp"`
	// TargetParam：目标规则 17「本方带某神通的护法」的神通号（客户端 target_param；如烈焰怒吼 20061 乱舞）
	TargetParam []int32 `json:"target_param"`
}

// BuffInfo 是一条 buff（auto/buff.lua）。精修 buff 名字带数字的随精修次数升级（buff 号 = jingxiu_buff + 次数 - 1），
// 其余按「次数×10% 几率」挂上。
type BuffInfo struct {
	Name  string `json:"name"`
	Param string `json:"param"`
}

// Upgrades：名字带数字（燃烧1 … 燃烧10），精修次数决定 buff 级别；否则按几率。
func (b *BuffInfo) Upgrades() bool { return strings.IndexAny(b.Name, "0123456789") >= 0 }

// Value 是 param 的整数值（空为 0）。
// Value 是 buff 参数的第一个数（参数可能是「10, 3」这种逗号分隔的多个数）。
func (b *BuffInfo) Value() int {
	first, _, _ := strings.Cut(b.Param, ",")
	n, _ := strconv.Atoi(strings.TrimSpace(first))
	return n
}

// RewardInfo 是关卡奖励（auto/reward.lua）里本服用到的几列。
type RewardInfo struct {
	Drops []string `json:"drops"` // 掉落编码串（item / ritemN），见 bag.go rollDrops
	Soul  int      `json:"soul"`
	Desc  string   `json:"desc"`
	CashA int      `json:"casha"`
	CashB int      `json:"cashb"`
	MExpA int      `json:"mexpa"`
	MExpB int      `json:"mexpb"`
	PExpA int      `json:"pexpa"`
	PExpB int      `json:"pexpb"`
}

// 与抓包核对过的口径：铜钱 cashb、卡牌经验 pexpb、主角经验 mexpa/2+mexpb（关卡 60305：739/1339/636）。
func (r *RewardInfo) Cash() int { return r.CashA + r.CashB }
func (r *RewardInfo) MExp() int { return r.MExpA/2 + r.MExpB }
func (r *RewardInfo) PExp() int { return r.PExpA + r.PExpB }

var (
	Gates      map[int]*GateInfo
	Formations map[int32][9]int32
	NPCs       map[int32]*NpcInfo
	Rewards    map[int32]*RewardInfo
	Achieves   map[int32]*AchieveInfo // 成就挑战条件（gates.json achieves）
	TaskGates  = map[int][]int{}      // 任务号 -> 关卡号（升序）

	Talents      map[int32]*TalentInfo
	TalentSets   map[int32]*TalentSetInfo   // 神通族
	LeaderSkills map[int32]*LeaderSkillInfo // 队长技
	Dangyao      DangyaoInfo                // 荡妖 / 功德商店
	Wish         map[int32][]int32          // 缘魂：卡牌 -> 有缘护法（auto/wish.lua）
	Relates      map[int32]*RelateInfo      // 缘分
	RelateReps   map[string]string          // 缘分护法名替换（双向）
	// ExtendRelates：护法可结缘的缘分（客户端 pet.lua extend_relate，analysis/tools/export_extend_relate.py 导出）。
	ExtendRelates map[int32][]int32
	Magics        map[int32]*MagicInfo

	Items       map[int]*ItemInfo
	Suits       map[int]*SuitInfo
	BooksByStar = map[int][]int32{} // 法术书（FS 掉落）按星级
	SuitsByStar = map[int][]int{}   // 装备（ZB 掉落）按星级
	PetsByStar  = map[int][]int32{} // 卡牌（DZ 掉落）按星级
	Buffs       map[int32]*BuffInfo // buff 表（buff.json：名字、参数）

	expTable struct {
		Card     []float64 `json:"card"`      // 3 星卡 lv→lv+1 所需经验（pet_mgr.lua）
		CardRate []float64 `json:"card_rate"` // 1~5 星倍率
		Role     []int32   `json:"role"`      // 人物 lv→lv+1 所需经验（CMainView.lua）
	}
)

const (
	MaxRoleGrade = 50  // 人物满级（官方 50 级 next_exp = 999999）
	MaxPetGrade  = 100 // config.lua MAX_PET_GRADE
)

// NewbieTask 是一条主线 / 成长任务（客户端 auto/newbietask_new.lua：名字、奖励表号；完成条件在服务端按名字判定）。
type NewbieTask struct {
	ID     int32    `json:"id"`
	Name   string   `json:"name"`
	Reward int32    `json:"reward"` // 客户端奖励表号（显示用）；内容展开在下面几项
	Gold   int32    `json:"gold"`
	Cash   int32    `json:"cash"`
	Soul   int32    `json:"soul"`
	Drops  []string `json:"drops"` // 掉落编码，同 RewardInfo.Drops
}

// NewbieTasks 按 id 升序。
var NewbieTasks []NewbieTask

// CardNeed 是 star 星卡牌从 grade 级升到下一级所需经验（pet_mgr.getUpgradeExp，界面取整显示）；
// 满级返回 0。
func CardNeed(star, grade int) int32 {
	if star < 1 || star > len(expTable.CardRate) || grade < 1 || grade >= MaxPetGrade || grade > len(expTable.Card) {
		return 0
	}
	return int32(expTable.Card[grade-1] * expTable.CardRate[star-1])
}

// RoleNeed 是人物从 grade 级升到下一级所需经验；满级返回 0。
func RoleNeed(grade int) int32 {
	if grade < 1 || grade >= MaxRoleGrade || grade > len(expTable.Role) {
		return 0
	}
	return expTable.Role[grade-1]
}

var (
	Pets     map[int32]*PetInfo
	MapTasks = map[int]int{} // 地图号 -> 任务数；任务号 = 地图号*100 + 1..n
	Static   struct {
		Entrance    []xym.Msg      `json:"entrance"`     // S_UPDATE_ENTRANCE.list
		QuestSwitch map[string]int `json:"quest_switch"` // 功能开关
	}
)

func MustLoad(name string, v any) {
	b, err := dataFS.ReadFile("data/" + name)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		panic("xymsrv: " + name + ": " + err.Error())
	}
}

func init() {
	Pets = loadPetCSV()
	Magics, Suits = loadMagicCSV(), loadSuitCSV()
	Talents, TalentSets = loadTalentCSV(), loadTalentSetCSV()
	LeaderSkills = loadLeaderSkillCSV()
	Relates, RelateReps = loadRelateCSV(), loadRelateRepCSV()
	var maps []struct{ ID, Count int }
	MustLoad("maps.json", &maps)
	for _, m := range maps {
		MapTasks[m.ID] = m.Count
	}
	MustLoad("static.json", &Static)
	MustLoad("newbietask.json", &NewbieTasks)

	var g struct {
		Gates      map[int]*GateInfo      `json:"gates"`
		Formations map[int32][9]int32     `json:"formations"`
		NPCs       map[int32]*NpcInfo     `json:"npcs"`
		Rewards    map[int32]*RewardInfo  `json:"rewards"`
		Achieves   map[int32]*AchieveInfo `json:"achieves"`
	}
	MustLoad("gates.json", &g)
	Gates, Formations, NPCs, Rewards, Achieves = g.Gates, g.Formations, g.NPCs, g.Rewards, g.Achieves
	for gid, gi := range Gates {
		TaskGates[gi.Task] = append(TaskGates[gi.Task], gid)
	}
	for _, l := range TaskGates {
		sort.Ints(l)
	}

	MustLoad("exp.json", &expTable)
	MustLoad("dangyao.json", &Dangyao)
	MustLoad("buff.json", &Buffs)
	MustLoad("wish.json", &Wish)
	MustLoad("extend_relate.json", &ExtendRelates)
	loadZhenfa()

	var bag struct {
		Items map[int]*ItemInfo `json:"items"`
	}
	MustLoad("bag.json", &bag)
	Items = bag.Items
	addHotfixItems()
	for id, m := range Magics { // 法术书（FS 掉落）是 11000-14999 号法术；幻境专属的不进随机池
		if id >= 11000 && id < 15000 && !HuanjingOnly[int(id)] {
			BooksByStar[m.Star] = append(BooksByStar[m.Star], id)
		}
	}
	for _, l := range BooksByStar {
		slices.Sort(l)
	}
	for id, si := range Suits {
		if si.Prop != "" && !HuanjingOnly[id] {
			SuitsByStar[si.Star] = append(SuitsByStar[si.Star], id)
		}
	}
	for _, l := range SuitsByStar {
		sort.Ints(l)
	}
	for _, pi := range Pets {
		PetsByStar[pi.Star] = append(PetsByStar[pi.Star], pi.Type)
	}
	for _, l := range PetsByStar {
		slices.Sort(l)
	}
}

// DangyaoInfo 是荡妖的配置（dangyao.json，来源 client logic/dangyao_mgr.lua 与 fight_ca）。
type DangyaoInfo struct {
	Shop       []DangyaoShopItem `json:"shop"`
	Levels     []DangyaoLevel    `json:"levels"`     // 功德称号：下标 0 = 1 级
	Floors     map[string]int    `json:"floors"`     // 妖窟号（1–12 普通、101–112 特殊）-> 层数
	Formations []int32           `json:"formations"` // 荡妖怪物阵型（fight_ca）
}

// DangyaoLevel：功德称号一级。Max 是持有功德上限（重置妖窟时超出的作废），Need 是升到下一级要累计消耗的功德。
type DangyaoLevel struct {
	Max  int   `json:"max"`
	Need int64 `json:"need"`
}

// DangyaoShopItem 是功德商店的一件商品（ItemList）：Type 1 护法殿 / 2 法器殿 / 3 修为殿；Need 是要求的功德称号等级；
// CA 是奖励号（reward 表，0 = 本周固定魂魄）；Left 每次刷新可买次数（-1 不限）；Weight 是刷新时上架的权重（‰），
// 依次为 jichu / yaoyao / zhongyao。
type DangyaoShopItem struct {
	Type   int    `json:"type"`
	Pos    int    `json:"pos"`
	Name   string `json:"name"`
	Price  int    `json:"price"`
	Left   int    `json:"left"`
	Order  int    `json:"order"`
	Need   int    `json:"need"`
	CA     int32  `json:"ca"`
	Icon   int32  `json:"icon"`
	Desc   string `json:"desc"`
	Weight [3]int `json:"weight"`
}
