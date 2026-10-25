package battle

import (
	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 上场单位：静态数据（Fighter，由阵容 / 关卡生成）与战斗中的运行时状态（unit）、神通条目（talent）。

// Fighter 是上场单位的静态数据（由阵容/关卡生成）。
type Fighter struct {
	ID      int32 // 我方 npcnum / 敌方怪物号
	Pos     int   // 我方 1-9，敌方 101-109
	Name    string
	Att     int32
	HP      int32
	MaxHP   int32
	Speed   int
	Icon    int32
	Race    int
	Grade   int     // 卡牌 / 怪物等级，参与法术伤害上限
	Talents []int32 // 神通号
	// TalentTJ 与 Talents 按下标对应：该神通的激发等级 0–5（S_UPDATE_PET tlxlv，见 jifa.go）；可短于 Talents。
	TalentTJ []int
	Magic    int32 // 法术号（0 = 无）
	MagicLv  int
	Books    [][2]int32 // 穿戴的法术书：{法术号, 等级}，与天生法术一起参与施放
	// BookRate 与 Books 按下标对应：该书施放几率的额外加成（百分点，天将每阶 +2）；可短于 Books。
	BookRate []int
	// BookJX 与 Books 按下标对应：法术书的精修次数（法术发动后对目标附加精修 buff，jingxiu.go）；可短于 Books。
	BookJX []int
	// MagicUp / HealUp：法术伤害 / 回复效果提升（百分点，试炼的魔法 / 治疗加成），与队长技同类加成相加。
	MagicUp, HealUp int
	// 以下只用于战报里的护法详情（S_FIGHT_RECORD.otherparam），不参与计算。
	Suits  [][5]int32 // 穿戴的装备 {装备位 1–3, 装备号, 强化等级, 天将阶, 镶嵌的宝石道具号}
	BookTJ []int      // 与 Books 按下标对应：法术书的天将阶
	// Gems：装备镶嵌的宝石加成（百分点）：普通伤害、法术伤害、普通伤害抗性、法术伤害抗性、被治疗效果、回复效果，
	// 与队长技同类加成相加（xymsrv/inlay.go）。
	Gems [6]int
	// SkillFull：三个神通都升满（战报 team 的 skillfull，客户端卡牌左上角显示神通满的种族徽标）。
	SkillFull bool
	// 洞府修为（xymsrv/dongfu_xiuwei.go）：ElemRes 按属性（0 道 1 土 2 水 3 火 4 雷）减免受到的属性伤害 %；
	// RaceDmg 按目标种族（1 人 2 魔 3 仙 4 兽）增加造成的伤害 %；DaoImmune 是免疫道属性伤害的几率 %。
	ElemRes   [5]int
	RaceDmg   [5]int
	DaoImmune int
}

func (f *Fighter) slot() int { return f.Pos % enemyBase }

func (f *Fighter) TeamMsg() xym.Msg {
	return xym.Msg{"id": f.ID, "pos": f.Pos, "name": f.Name, "att": f.Att, "hp": f.HP,
		"sp": f.Speed, "icon": f.Icon, "race": f.Race, "skillfull": b2i(f.SkillFull)}
}

// unit 是战斗中的单位（带运行时状态）。
type unit struct {
	*Fighter
	side         int   // 0 我方 1 敌方
	att, hp      int64 // 当前攻击 / 生命
	maxHP        int64
	baseAtt      int64 // 初始攻击（「提升 x% 初始攻击力」以它为基数）
	tals         map[family]*talent
	magics       []*castable // 天生法术在前，其后是法术书
	shield       int64       // 物理结界
	mshield      int64       // 属性结界
	poison       []poison
	hurtUp       int // 会猎：受到物理伤害增加 %
	tempAtt      int64
	dmgUp        int   // 本次攻击行为的伤害提高 %（灭法、异态；结算伤害时乘，attackAction 结束时还原）
	tempBase     int64 // 当前攻击行为开始时已有的临时加攻（外层攻击行为留下的），见 atkBase
	acted        bool
	gone         bool // 阵亡后槽位已被后阵补上：离场，不能再被复活
	status       map[stKey]*status
	bumiequ      int      // 不灭躯发动过的次数
	attachSoul   bool     // 本次攻击附带锢魂（魔煞）
	attachNoHeal int32    // 本次攻击附带禁疗的 buff（兽妖）
	burnShown    int32    // 当前显示的燃烧图标（burnIcon；0 = 无）
	shenliPct    int64    // 神之力累计提升初始攻击的 %（shenli）
	shenliAtt    int64    // 神之力累计实际加的攻击（回后阵重置时保留）
	shenliShown  int32    // 当前显示的神之力图标
	zfFixed      [3]int64 // 阵法阵位给的初始攻击 / 初始生命 / 先攻（zhenfa.go zfSync）
	zfLost       family   // 阵法阵位「失去神通」的族（在该阵位时不能发动）
	huweiBuff    int32    // 狐尾（九尾仙车）：图标、等级、生命上限减益时最少保留的 %（huwei）
	huweiLv      int
	huweiFloor   int
	huoshiBuff   int32                // 活尸图标（huoshi），死亡时去掉
	huilieBuff   int32                // 会猎当前显示的 buff 号
	huilie       int                  // 会猎：当前受物理伤害增加 %（并入 hurtUp）
	reviveAt     int                  // 「2 回合后复活」：在第几回合结束时复活（markRevive）
	reviveMark   int32                // 等待复活时挂的返死 / 还魂 buff（markRevive）
	reviveIn     int                  // 返死：还有几回合复活（0 = 无）
	revivePct    int                  // 返死复活血量 %（默认 reviveHPPct，群夭诀按 5551-5554 覆写）
	egg          bool                 // 归元火之卵化身（每回合开始放火系群法）
	doomIn       int                  // 不灭躯自杀倒计时（回合结束递减，归零自行死亡）
	charge       int                  // 蓄力（飞石、三板斧）
	devoured     bool                 // 吞噬（只能 1 次）
	raged        bool                 // 暴走已触发
	revived      bool                 // 被复活过（暴走条件之一）
	swords       int                  // 仙剑数
	dupTals      map[family][]*talent // 同族的其他来源（护法自带 + 套装各一个突袭等）：被主条目顶掉的那些，见 newUnit
	lead         leadBonus            // 本方队长技加成
	fired        map[family]int64     // 本次行动各族的临时加攻（tempUp 记录，行动结束清空）
	fengkuang    int                  // 疯狂触发次数（激发：满 5 次获得乱舞）
	skillDone    int                  // 铸剑 / 天地灵气持有者已响应过的触发种类（skZhasi | skDeath | skEnter）
	skillUps     int                  // 铸剑 / 天地灵气已重置的轮数（之后的效果按 $x% 增强）
	spd          int                  // 先攻加值（进化 / 蟠桃宴激发 +1，凝滞 −4 另见状态）
	dusha        int                  // 毒砂（精修）：每回合开始损失攻击力 %（持续到战斗结束，取最高级）
	dushaBuff    int32

	// 以下是个别神通族的运行时状态（见各 talents_*.go）
	peakAtt    int64              // 本场最高攻击（噬心龙咒）
	jianyi     int                // 剑意层数（紫青剑：回合结束时位于后阵 +1）
	pellets    int                // 不稳定弹丸（火炮之术）
	sacrificed int64              // 献祭的生命（十日圣母）
	xiuluo     int                // 修罗点（修罗隐身）
	tianmo     int                // 天魔觉醒已提升次数
	borrowed   []family           // 摄魂术借来的神通族（死亡时移除）
	roundTals  map[family]*talent // 本回合临时获得的神通族 → 原来的（回合结束还原，水之形）
	fpCut      int                // 返璞归真已生效次数（每次减原值 10%，10 次为 0）
	fashenAt   int                // 不灭法身上次复活的回合（冷却）
	leiying    int                // 雷影庇护已触发次数
	perRound   [3]int             // 每回合计数：横冲直撞、藏剑于身、回梦
	atkVictim  *unit              // 上一次攻击行为实际受击的单位
	atkTarget  *unit              // 上一次攻击行为原定的目标（守卫 / 元形代承前）
	atkLanded  bool               // 上一次普攻命中了（没被闪避 / 盲目躲开；挥扫用）
	atkDealt   int64              // 上一次普攻的伤害值（血气）
	shenyouAt  int                // 神佑：上次触发的回合
	zhasiAt    int                // 诈死：上次触发的回合（0 = 没触发过）
	swordKind  int                // 仙剑属性（第一次获得仙剑时随机，整场不变）：0 道 1 土 2 水 3 火 4 雷
	swordSet   bool               // 已确定仙剑属性
	eaten      bool               // 尸体被吞噬 / 锢魂离场（不能再被复活）
	dunru      bool               // 飞仙：已[遁入]过
	extraAtk   bool               // 天地灵气：下次攻击有概率额外攻击一次
	baigu      bool               // 情殇化身的白骨魔（奇数回合释放疯魔乱棍）
	buffSlot   map[int32]int      // 身上挂着的 buff → 客户端槽位（battle.buff）
	deaths     int                // 阵亡次数（DIE）
	splitTo    *unit              // 分击：这次攻击行为要同时打的第二个目标（attack 在主攻击扣血后结算）
	splitID    int32
	bossArmor  bool // 魔甲 / 藤甲（九幽首领）：免疫生命流失与按生命百分比结算的神通伤害
	vineArmor  bool // 藤甲：受到的法术伤害提高 20%
	mainKill   bool // 本次行动的主攻击击败了目标（横冲直撞）
	lastKill   bool // 最近一次攻击行为击败了实际受击者（横冲直撞连续追击）
	nextSlot   int
	molong     bool // 魔龙已获得神通
	moonGiven  bool // 月之暗面已获得神通
	sacDone    bool // 十日圣母已献祭
	pelletInit bool // 火炮之术已装弹
}

// castable 是单位身上一个可施放的法术（天生或法术书）及其剩余次数。
type castable struct {
	id    int32
	lv    int
	left  int
	bonus int // 施放几率额外加成（百分点）
	jx    int // 精修次数（jingxiu.go）
	*gamedata.MagicInfo
}

type talent struct {
	id  int32
	fam family
	tj  int // 激发等级 0–5（jifa.go）
	*gamedata.TalentInfo
}

// newTalent 按神通号建 talent（未知的号返回 nil）。
func newTalent(id int32) *talent {
	if ti := gamedata.Talents[id]; ti != nil && ti.FamilyID != 0 {
		return &talent{id: id, fam: family(ti.FamilyID), TalentInfo: ti}
	}
	return nil
}

// param 取第 i 个参数（取整），缺省 0。
func (t *talent) param(i int) int {
	if i < len(t.Param) {
		return int(t.Param[i])
	}
	return 0
}

func (u *unit) alive() bool { return u.hp > 0 }

// atkBase 是「攻击时提升 x% 攻击（攻击后清除）」类神通的基数：当前攻击去掉外层攻击行为留下的临时加攻。乱舞 / 连击这类
// 追加攻击在主攻击的临时加攻还没清除时进行，原来按含临时加攻的攻击算，灭法、突袭等会层层叠加（用户报：逆鳞白龙乱舞中
// 灭法从 +5012 叠成 +9522，攻击显示到 2 万多）。
func (u *unit) atkBase() int64 { return u.att - u.tempBase }

// speed 是当前先攻：基础 + 加值 − 凝滞，至少 1。
func (u *unit) speed() int {
	s := u.Speed + u.spd
	if st := u.has(stSlow); st != nil {
		s -= int(st.val)
	}
	return max(s, 1)
}

func (u *unit) front() bool { return !u.gone && u.slot() <= frontSlots }

// tal 返回单位的某族神通；僵化时神通都不能发动。
func (u *unit) tal(f family) *talent {
	if u.has(stStiff) != nil || u.zfLost != 0 && f == u.zfLost { // 僵化；阵法阵位「失去神通」
		return nil
	}
	return u.tals[f]
}

// talAll 是某族神通的所有来源（自带 + 套装各一个，见 dupTals）；僵化时为空。突袭、驭兵等加攻类每个来源各自判定。
func (u *unit) talAll(f family) []*talent {
	k := u.tal(f)
	if k == nil {
		return nil
	}
	return append([]*talent{k}, u.dupTals[f]...)
}

func newUnit(f *Fighter, side int) *unit {
	u := &unit{Fighter: f, side: side, att: int64(f.Att), hp: int64(f.HP), maxHP: int64(f.MaxHP),
		baseAtt: int64(f.Att), peakAtt: int64(f.Att), tals: map[family]*talent{}}
	for _, id := range f.Talents {
		switch id {
		case talentBossArmor:
			u.bossArmor = true
		case talentVineArmor:
			u.bossArmor, u.vineArmor = true, true
		}
	}
	for i, id := range f.Talents {
		if k := newTalent(id); k != nil {
			if i < len(f.TalentTJ) {
				k.applyTJ(f.TalentTJ[i])
			}
			if old := u.tals[k.fam]; old != nil { // 同族两个来源（自带 + 套装）：都留着，突袭等各自判定（用户报：只触发一次）
				if u.dupTals == nil {
					u.dupTals = map[family][]*talent{}
				}
				u.dupTals[k.fam] = append(u.dupTals[k.fam], old)
			}
			u.tals[k.fam] = k
		}
	}
	if m := gamedata.Magics[f.Magic]; m != nil && f.MagicLv > 0 {
		u.magics = append(u.magics, &castable{id: f.Magic, lv: f.MagicLv, left: m.Times, MagicInfo: m})
	}
	for i, bk := range f.Books {
		if m := gamedata.Magics[bk[0]]; m != nil {
			c := &castable{id: bk[0], lv: int(max(bk[1], 1)), left: m.Times, MagicInfo: m}
			if i < len(f.BookRate) {
				c.bonus = f.BookRate[i]
			}
			if i < len(f.BookJX) {
				c.jx = f.BookJX[i]
			}
			u.magics = append(u.magics, c)
		}
	}
	return u
}

// lastVictimOf 是 u 上一次普攻实际打到的单位（守卫代承时是守卫）。
func (u *unit) lastVictimOf() *unit { return u.atkVictim }

// freeSlot 是 t 身上下一个没用过的槽位（2 起；用到头后找空着的）。
func (t *unit) freeSlot() int {
	if t.nextSlot < 2 {
		t.nextSlot = 2
	}
	if t.nextSlot < 250 {
		t.nextSlot++
		return t.nextSlot - 1
	}
	used := map[int]bool{}
	for _, s := range t.buffSlot {
		used[s] = true
	}
	for s := 2; s < 250; s++ {
		if !used[s] {
			return s
		}
	}
	return 249
}

// EnemyTeam 按关卡阵型和等级参数生成敌方（与抓包逐项核对过：hp = hp + hpadd*param/1000）。
func EnemyTeam(mode gamedata.GateMode) []*Fighter {
	form, ok := gamedata.Formations[mode.CA]
	if !ok {
		return nil
	}
	var out []*Fighter
	for i, id := range form {
		n := gamedata.NPCs[id]
		if id == 0 || n == nil {
			continue
		}
		hp := int32(n.HP + n.HPAdd*mode.Param/1000)
		out = append(out, &Fighter{ID: id, Pos: enemyBase + i + 1, Name: n.Name,
			Att: int32(n.Att + n.AttAdd*mode.Param/1000), HP: hp, MaxHP: hp,
			Speed: n.Speed, Icon: n.Shape, Race: n.Race, Grade: n.Grade,
			Talents: n.Talents, Magic: n.Magic, MagicLv: n.MagicLv})
	}
	return out
}
