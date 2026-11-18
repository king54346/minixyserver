package battle

import "testing"

// 换位和阵亡补位都应先暴走；敌方火炮属于入阵反应，不能在暴走之前结算。
func TestFrontEntryRampageBeforeEntryReactions(t *testing.T) {
	for _, mode := range []string{"swap", "move_up", "healthy", "revived", "depth_limit"} {
		t.Run(mode, func(t *testing.T) {
			out := newUnit(fu(1, 0, 1000, 1), 0)
			in := newUnit(fuh(6, 100, 100, 1000, 1, 21054), 0)
			foe := newUnit(fu(101, 0, 10000, 1, levelID(famHuoPaoZhiShu, 4)), 1)
			foe.pellets = 10
			b := faqBattle(out, in, foe)
			if mode == "healthy" || mode == "revived" {
				in.hp = in.maxHP
				in.revived = mode == "revived"
			}
			if mode == "depth_limit" {
				b.enterDepth = 3 // 连锁入阵限制不能跳过暴走。
			}
			if mode == "move_up" {
				out.hp = 0
				b.moveUp(0)
			} else {
				b.swap(out, in)
			}
			want := mode != "healthy"
			if in.raged != want || (in.has(stImmune) != nil) != want {
				t.Fatalf("raged=%v immune=%v want=%v", in.raged, in.has(stImmune) != nil, want)
			}
			if mode == "depth_limit" || !want {
				return
			}
			rage, reaction := -1, -1
			for i, c := range mustDecode(t, b.w.b) {
				if c.ID == CmdActDo && c.Magic == 21054 {
					rage = i
				}
				if c.ID == CmdActDo && c.Magic == int64(foe.tals[famHuoPaoZhiShu].id) {
					reaction = i
				}
			}
			if rage < 0 || reaction <= rage {
				t.Fatalf("rampage command=%d entry reaction=%d", rage, reaction)
			}
		})
	}
}

func TestTianhuHuweiMaxHPFloor(t *testing.T) {
	for _, tj := range []int{0, 5} {
		t.Run(map[int]string{0: "normal", 5: "tianjiang"}[tj], func(t *testing.T) {
			fox := newUnit(tjf(fu(1, 0, 10000, 1, 22094), tj), 0)
			target := newUnit(fu(2, 0, 10000, 1), 0)
			foe := newUnit(fu(101, 0, 10000, 1), 1)
			b := faqBattle(fox, target, foe)
			// 五名暴走护法（含后阵），天将最多按四名计算。
			for _, pos := range []int{3, 4, 5, 6, 7} {
				b.units[0] = append(b.units[0], newUnit(fu(pos, 0, 10000, 1, 21054), 0))
			}
			b.beforeAttack(fox, foe)
			want := int64(10900 * (16 + 4*tj) / 100)
			if target.huweiBuff != 5644 || target.maxHP != 10900 {
				t.Fatalf("buff=%d maxHP=%d", target.huweiBuff, target.maxHP)
			}
			b.changeMaxHP(target, 1, -100000)
			b.changeMaxHP(target, 1, -100000) // 连续削减不能穿透保留线。
			if target.maxHP != want || target.hp != want {
				t.Fatalf("maxHP/hp=%d/%d want=%d", target.maxHP, target.hp, want)
			}
			target.Pos = 8
			b.backToBench(target)
			b.changeMaxHP(target, 1, -100000)
			if target.maxHP != want || target.huweiBuff != 5644 {
				t.Fatal("returning to bench lost Huwei")
			}
			target.hp = 0
			b.leaveClear(target, true)
			if target.huweiFloor != 0 || target.huweiBuff != 0 {
				t.Fatal("death did not clear Huwei")
			}
			b.changeMaxHP(target, 1, -100000)
			if target.maxHP != 1 {
				t.Fatalf("death retained floor: %d", target.maxHP)
			}
		})
	}
}

func TestTianhuHuweiSnapshotsEachApplication(t *testing.T) {
	fox := newUnit(fu(1, 0, 10000, 1, 22094), 0)
	target := newUnit(fu(2, 0, 10000, 1), 0)
	foe := newUnit(fu(101, 0, 10000, 1), 1)
	b := faqBattle(fox, target, foe)
	b.changeMaxHP(target, 1, 20000) // 入战1万，施法前已增到3万。
	b.beforeAttack(fox, foe)
	if target.maxHP != 32700 || target.huweiFloor != 5232 {
		t.Fatalf("first snapshot: max=%d floor=%d", target.maxHP, target.huweiFloor)
	}
	b.changeMaxHP(target, 1, 10000) // 施加后继续增长，不动态提高保留线。
	b.changeMaxHP(target, 1, -1000000)
	if target.maxHP != 5232 {
		t.Fatalf("snapshot moved with later maxHP: %d", target.maxHP)
	}
	b.changeMaxHP(target, 1, 10000)
	b.beforeAttack(fox, foe) // 同级再次释放：15232 + floor(15232×9%) = 16602。
	if target.maxHP != 16602 || target.huweiFloor != 2657 {
		t.Fatalf("replacement snapshot: max=%d floor=%d", target.maxHP, target.huweiFloor)
	}
	b.changeMaxHP(target, 1, -1000000)
	if target.maxHP != 2657 {
		t.Fatalf("old higher snapshot retained: %d", target.maxHP)
	}
}

func TestTianhuHuweiLimitsShihouTransfer(t *testing.T) {
	fox := newUnit(fu(1, 0, 10000, 1, 22093), 0)
	victim := newUnit(fu(2, 0, 10000, 1), 0)
	lion := newUnit(fu(101, 0, 10000, 1, int32(famShiHou)), 1)
	b := faqBattle(fox, victim, lion)
	b.beforeAttack(fox, lion)
	const floor = 1363 // 12.5% × 10900, rounded up to whole HP.
	if victim.huweiFloor != floor {
		t.Fatalf("floor=%d, want %d", victim.huweiFloor, floor)
	}
	b.changeMaxHP(victim, 1, -9500)
	k := lion.tal(famShiHou)
	setParam(k, 1, 100)
	b.shihouDrain(victim, lion, k)
	b.shihouDrain(victim, lion, k)
	if victim.maxHP != floor || lion.maxHP != 10037 {
		t.Fatalf("victim maxHP=%d, lion maxHP=%d", victim.maxHP, lion.maxHP)
	}
}
