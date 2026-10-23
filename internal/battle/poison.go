package battle

import ()

// 持续掉血：中毒、毒爪、燃烧（燃烧多层合并成一个图标，见 burnIcon）。

type poison struct {
	from int32 // 神通号（演出用）
	pct  int   // 每回合流失上限生命的 %
	left int
	buff int32
	burn bool // 燃烧（狂怒焰）；其余是中毒（毒爪）
	// stacked：狂怒焰激发叠加的额外一层（与已有燃烧并存，不参与 addPoison 的合并；所有燃烧层合成一个图标，burnIcon）
	stacked bool
}

// burnMagic 是燃烧每回合结算时 SUB_HP 用的特效法术号（官方战报：ACT_DO(被烧者, 燃烧 BUFF 号) → SUB_HP 17531）。
const burnMagic = 17531

// addBurnStack 给 t 额外叠加一层燃烧（狂怒焰激发）：单独占一个 BUFF 槽位显示、回合结束单独结算。
func (b *battle) addBurnStack(t *unit, p poison) {
	p.burn, p.stacked = true, true
	t.poison = append(t.poison, p)
	b.burnIcon(t)
}

// rmPoisonIcon 去掉一层中毒的图标。燃烧不在这里处理：所有燃烧层合成一个图标，由 burnIcon 按剩下的层更新。
func (b *battle) rmPoisonIcon(t *unit, p poison) {
	if p.burn {
		return
	}
	if p.buff != 0 {
		b.rmBuff(t, p.buff)
	}
}

// burnMaxPct 是燃烧合计的上限：燃烧10（每回合 20%，客户端最高的燃烧图标；）。
const burnMaxPct = 20

// burnPct 是 t 身上所有燃烧层合计每回合流失上限的 %（至多 burnMaxPct）。
func (t *unit) burnPct() int {
	n := 0
	for _, p := range t.poison {
		if p.burn {
			n += p.pct
		}
	}
	return min(n, burnMaxPct)
}

// burnIcon：t 身上的燃烧（不叠加的那层 + 狂怒焰天将叠加层）合成一个图标显示——等级取合计 % 对应的燃烧N
// （燃烧N = 每回合 2N%，如燃烧4 + 燃烧4 = 16% 显示燃烧8；合计至多燃烧10 = 20%，超出的不生效）。
// 一个图标就不会有多个火焰特效叠加去不掉的问题，客户端原样显示，不需要补丁（用户：按理解合理展示）。
func (b *battle) burnIcon(t *unit) {
	var id int32
	if n := t.burnPct(); n > 0 {
		id = 5020 + int32(min(max((n+1)/2, 1), 10))
	}
	if id == t.burnShown {
		return
	}
	if t.burnShown != 0 {
		b.rmBuff(t, t.burnShown)
	}
	if id != 0 {
		b.buff(t, id, maxRounds)
	}
	t.burnShown = id
}

// addPoison 给 t 加中毒 / 燃烧：同一种（燃烧 / 中毒）只保留一个状态——再挂时刷新持续回合、取较高的比例，
// 不叠加成多份（与会猎同类：客户端只显示一个状态，叠加会每回合多扣几份血）。
// 同时负责 BUFF 显示：换成更高等级时先 RM_BUFF 旧图标，客户端始终只显示一个。
func (b *battle) addPoison(t *unit, p poison) {
	for i := range t.poison {
		if e := &t.poison[i]; e.burn == p.burn && !e.stacked {
			if p.pct > e.pct {
				if !p.burn && e.buff != 0 && e.buff != p.buff {
					b.rmBuff(t, e.buff)
				}
				e.pct, e.from, e.buff = p.pct, p.from, p.buff
			}
			e.left = max(e.left, p.left)
			if p.burn {
				b.burnIcon(t)
			} else if e.buff != 0 {
				b.buff(t, e.buff, e.left)
			}
			return
		}
	}
	t.poison = append(t.poison, p)
	if p.burn {
		b.burnIcon(t)
	} else if p.buff != 0 {
		b.buff(t, p.buff, p.left)
	}
}
