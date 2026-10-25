package battle

// 仙剑（布剑）：按属性的添剑命令（SWORD_DAO / EARTH / WATER / FIRE / LIGHT）；攻击后 SWORD_ATTACK + 每把剑一下伤害。

const (
	CmdSwordEarth  = 31
	CmdSwordAttack = 32
	magicEarthHurt = 10003 // 普通土属性伤害（官方战报剑伤用它）
	magicFireHurt  = 10002 // 普通火属性伤害（官方战报血池攻击用它）
	maxSwords      = 5
)

// swordCmd 发一把剑的添剑命令：按仙剑属性用 SWORD_DAO 39 / EARTH 31 / WATER 30 / FIRE 29 / LIGHT 28（雷）
// （官方战报：雷剑 SWORD_LIGHT + SUB_HP 10001、道剑 SWORD_DAO + 10006…；之前一律发 31，客户端全显示成土剑）。
func (b *battle) swordCmd(x *unit) {
	b.w.u8(swordCmds[x.swordKind])
	b.w.u8(x.Pos)
}

// reshowSwords：后阵的护法补位 / 换位到前阵时客户端会把它的小头像换成大卡，身上的剑不跟过去，
// 重新发一遍添剑命令让新卡显示出来（不改剑数）。
func (b *battle) reshowSwords(x *unit) {
	for range x.swords {
		b.swordCmd(x)
	}
}

var swordCmds = [5]int{39, CmdSwordEarth, 30, 29, 28}

// swordMagic / swordBuffs 按仙剑属性（0 道 1 土 2 水 3 火 4 雷）：伤害招式号、5% 附带的状态。
var (
	swordMagic = [5]int32{10006, magicEarthHurt, 10004, magicFireHurt, 10001}
	swordBuffs = [5]int32{0, 5035, 5001, 5025, 5011}
)

func (b *battle) addSword(u *unit, k *talent) {
	if u.swords >= maxSwords {
		return
	}
	b.show(u, k.id, u)
	b.giveSword(u)
}

// giveSword 给 x 添一柄仙剑（满 5 柄不再加），返回是否加了。
func (b *battle) giveSword(x *unit) bool {
	if x.swords >= maxSwords {
		return false
	}
	if !x.swordSet {
		// 第一次获得仙剑时确定属性（道 土 水 火 雷，FAQ 布剑），整场不变：剑攻击后消散再拿新剑时如果重新随机，
		// 客户端会在同一张卡上显示两种剑（用户报：逆鳞白龙两种剑）
		x.swordKind, x.swordSet = b.rng.IntN(5), true
	}
	x.swords++
	b.swordCmd(x) // 官方只发属性剑命令，不挂 BUFF 5361：挂了客户端会多画一把旧样式的剑（「双色剑」，实际只有一把）
	return true
}

// swordStrike：攻击后所有仙剑以 50% 初始攻击各打一次，之后剑消散。
func (b *battle) swordStrike(u, t *unit) {
	if u.swords == 0 || !t.alive() {
		return
	}
	b.w.u8(CmdSwordAttack)
	b.w.u8(u.Pos)
	b.w.u8(t.Pos)
	b.w.u8(u.swords)
	// 每把剑各打一下 50% 初始攻击（官方战报：SWORD_ATTACK{count 3} 后 3 条 SUB_HP，目标死了就停）。
	// 仙剑属性决定伤害类型（官方战报：SUB_HP 10001 雷 / 10002 火 / 10003 土 / 10004 水 / 10006 道，雷剑触发仙衣）；
	// 道属性仙剑是生命流失（FAQ）。每把剑攻击后 5% 概率附带：水 僵化、火 燃烧5、雷 麻痹、土 毒砂5。
	id := swordMagic[u.swordKind]
	for i := range u.swords {
		if b.over {
			break
		}
		if !t.alive() { // 打死当前目标后剩下的仙剑改打当前对位继续出手（用户：有几把放几次）
			if t = b.pickTarget(u); t == nil {
				break
			}
			b.w.u8(CmdSwordAttack)
			b.w.u8(u.Pos)
			b.w.u8(t.Pos)
			b.w.u8(u.swords - i)
		}
		n := u.baseAtt / 2
		if k := u.tal(famBuJian); k != nil && k.tjOn() && b.chance(k.tjPct()) {
			n *= 2 // 布剑激发：飞剑 $x% 概率双倍伤害
		}
		if k := b.wanxian(u.side); u.swordKind == 0 || k != nil && k.tjOn() && b.chance(k.tjPct()) {
			b.loseHP(u, t, id, n) // 万仙来朝激发：仙剑伤害 $x% 概率变为生命流失
		} else {
			b.propertyHurt(u, t, id, n, u.swordKind)
		}
		if buff := swordBuffs[u.swordKind]; buff != 0 && t.alive() && b.chance(5) {
			b.applyBuff(u, t, buff) // FAQ「每次仙剑攻击后有5%几率产生buff」：攻击后，在伤害之后
		}
	}
	swords := u.swords
	u.swords = 0
	for _, c := range b.allies(u) {
		k := c.tal(famZiQingJian)
		if k == nil || !k.tjOn() || !t.alive() || !b.chance(int(k.tjX())*swords) {
			continue
		}
		for _, x := range b.allies(u) { // 合击：非本护法的仙剑伤害为原伤害 60%
			if x != u && x.swords > 0 && t.alive() {
				b.show(c, k.id, t)
				if n := x.baseAtt * int64(x.swords) / 2 * 60 / 100; x.swordKind == 0 { // 伤害来源与属性仍是该仙剑的拥有者（FAQ）
					b.loseHP(x, t, swordMagic[0], n)
				} else {
					b.propertyHurt(x, t, swordMagic[x.swordKind], n, x.swordKind)
				}
			}
		}
		break
	}
}
