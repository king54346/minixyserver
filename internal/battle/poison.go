package battle

// 持续掉血：中毒、毒爪、燃烧。
//
// 燃烧（用户规则）：所有燃烧都叠加——每挂一次就是新的一层，各占一个 BUFF 槽位、各显示一个图标（燃烧4 两层就是两个燃烧4，
// 燃烧4 和燃烧5 同时显示），回合结束逐层结算，合计不封顶。客户端战斗详情的「状态」原本同一种 BUFF 只列一次，
// 客户端 pet_mgr 按燃烧层分别显示。中毒（毒爪、毒化）同一种只保留一个（取较高比例、刷新回合）。

type poison struct {
	from int32 // 神通号（演出用）
	pct  int   // 每回合流失上限生命的 %
	left int
	buff int32
	burn bool // 燃烧；其余是中毒（毒爪）
	slot int  // 燃烧：这一层自己的 BUFF 槽位
}

// burnMagic 是燃烧每回合结算时 SUB_HP 用的特效法术号（官方战报：ACT_DO(被烧者, 燃烧 BUFF 号) → SUB_HP 17531）。
const burnMagic = 17531

// addBurn 给 t 叠一层燃烧：单独占一个 BUFF 槽位显示、回合结束单独结算。
func (b *battle) addBurn(t *unit, p poison) {
	p.burn, p.slot = true, t.freeSlot()
	t.poison = append(t.poison, p)
	b.buffCmd(t, p.buff, p.slot, maxRounds)
}

// addBurnStack 同 addBurn（狂怒焰激发等额外的一层；现在所有燃烧都叠加，与 addPoison 的燃烧一样）。
func (b *battle) addBurnStack(t *unit, p poison) { b.addBurn(t, p) }

// rmPoisonIcon 去掉一层持续掉血的图标（燃烧按这一层的槽位去）。
func (b *battle) rmPoisonIcon(t *unit, p poison) {
	if p.burn {
		if p.slot != 0 {
			b.rmSlot(t, p.slot)
		}
		return
	}
	if p.buff != 0 {
		b.rmBuff(t, p.buff)
	}
}

// burnPct 是 t 身上所有燃烧层合计每回合流失上限的 %（不封顶）。
func (t *unit) burnPct() int {
	n := 0
	for _, p := range t.poison {
		if p.burn {
			n += p.pct
		}
	}
	return n
}

// addPoison 给 t 加中毒 / 燃烧。燃烧每次都叠一层（addBurn）；中毒同一种只保留一个状态——再挂时刷新持续回合、
// 取较高的比例（与会猎同类：客户端只显示一个状态），换成更高等级时先 RM_BUFF 旧图标。
func (b *battle) addPoison(t *unit, p poison) {
	if p.burn {
		b.addBurn(t, p)
		return
	}
	for i := range t.poison {
		if e := &t.poison[i]; !e.burn {
			if p.pct > e.pct {
				if e.buff != 0 && e.buff != p.buff {
					b.rmBuff(t, e.buff)
				}
				e.pct, e.from, e.buff = p.pct, p.from, p.buff
			}
			e.left = max(e.left, p.left)
			if e.buff != 0 {
				b.buff(t, e.buff, e.left)
			}
			return
		}
	}
	t.poison = append(t.poison, p)
	if p.buff != 0 {
		b.buff(t, p.buff, p.left)
	}
}
