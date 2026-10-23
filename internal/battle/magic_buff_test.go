package battle

import "testing"

// TestMagicCondRoundAndBuff：下凡（15364，cond_round 1,4,7，buff 5051 混乱 63%+4%×LV）只在 1、4、7 回合施放，
// 且会给目标挂混乱。
func TestMagicCondRoundAndBuff(t *testing.T) {
	f := fu(1, 100, 1000000, 10)
	f.Magic, f.MagicLv = 15364, 10
	confused := false
	for seed := uint64(0); seed < 30; seed++ {
		_, cmds := simCheck(t, []*Fighter{f}, []*Fighter{fu(101, 1, 1000000, 1), fu(102, 1, 1000000, 1)}, seed)
		round := 0
		for _, c := range cmds {
			if c.ID == CmdTurn {
				round++
			}
			if c.ID == CmdActDo && c.Magic == 15364 && round != 1 && round != 4 && round != 7 {
				t.Fatalf("下凡 cast in round %d", round)
			}
		}
		confused = confused || hasCmd(cmds, CmdBuff, 5051, -1, -1)
	}
	if !confused {
		t.Fatal("下凡 never applied 混乱")
	}
}

// TestMagicExtSP：沐浴日晖（15412，cond_round 3,5，ext_sp 回复 13%+1%×LV）在第 3 回合给全体回血。
func TestMagicExtSP(t *testing.T) {
	f := fuh(1, 1, 500, 1000, 10)
	f.Magic, f.MagicLv = 15412, 10
	_, cmds := simCheck(t, []*Fighter{f}, []*Fighter{fu(101, 0, 1000000, 1)}, 1)
	if !hasCmd(cmds, CmdAddHP, 15412, 1, 230) {
		t.Fatal("沐浴日晖 no 23% heal")
	}
}

// TestGuardTarget：守卫代为承受时，攻击方的 ACT_DO 指向护卫者（客户端按它决定冲向谁、在谁身上播扣血）。
func TestGuardTarget(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fuh(1, 1, 100, 1000, 1), fu(2, 1, 100000, 1, 20752)},
		[]*Fighter{fu(101, 50, 100000, 10)}, 1)
	for i, c := range cmds {
		if c.ID == CmdActDo && c.Actor == 2 && c.Magic == attackGuard && c.Targets[0][0] == 1 {
			if n := cmds[i+1]; n.ID != CmdActDo || n.Actor != 101 || n.Targets[0][0] != 2 {
				t.Fatalf("attack after guard: %+v", n)
			}
			return
		}
	}
	t.Fatal("no guard")
}

// TestChargePower：飞石受击蓄力输出 ADD_POWER（op 1）。
func TestChargePower(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fu(1, 1, 100000, 1, 20791)}, []*Fighter{fu(101, 10, 100000, 10)}, 1)
	for _, c := range cmds {
		if c.ID == CmdAddPower {
			return
		}
	}
	t.Fatal("no ADD_POWER")
}

// TestHengChongChain：横冲直撞4（100%，每回合 4 次）击败目标后追击，追击又击败时继续——第 1 回合一次行动打死 5 个敌人。
func TestHengChongChain(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fu(1, 1000, 100000, 10, 22206)},
		[]*Fighter{fu(101, 0, 10, 1), fu(102, 0, 10, 1), fu(103, 0, 10, 1), fu(104, 0, 10, 1), fu(105, 0, 10, 1)}, 1)
	deaths := 0
	for _, c := range cmds {
		if c.ID == CmdTurn && deaths > 0 {
			break
		}
		if c.ID == CmdDie {
			deaths += len(c.Targets)
		}
	}
	if deaths != 5 {
		t.Fatalf("round 1 deaths %d, want 5 (1 kill + 4 chained chases)", deaths)
	}
}

// TestBuMieFaShenChargeOnMagic：不灭法身被法术（无普攻：攻击 0 的施法者）打中也蓄力（ADD_POWER 在 SUB_HP 之前）。
func TestBuMieFaShenChargeOnMagic(t *testing.T) {
	caster := fu(101, 0, 100000, 10)
	caster.Magic, caster.MagicLv = 12101, 10 // 火球术
	caster.Att = 500
	for seed := uint64(0); seed < 20; seed++ {
		_, cmds := simCheck(t, []*Fighter{fu(1, 0, 1000000, 1, 22041)}, []*Fighter{caster}, seed)
		for i, c := range cmds {
			if c.ID == CmdAddPower && c.Targets[0][0] == 1 && i+1 < len(cmds) && cmds[i+1].ID == CmdSubHP {
				return
			}
		}
	}
	t.Fatal("不灭法身 never charged before SUB_HP")
}

// TestTuoMaoTuiDi：脱帽退敌4（100%）挨够 4 下蓄满后，攻击前令敌方攻击最高者与后排攻击最低者换位（ACT_DO + MOVE）。
func TestTuoMaoTuiDi(t *testing.T) {
	foes := []*Fighter{fu(101, 50, 100000, 10), fu(102, 60, 100000, 10), fu(103, 70, 100000, 10), fu(104, 80, 100000, 10),
		fu(105, 90, 100000, 10), fu(106, 5, 100000, 1)}
	_, cmds := simCheck(t, []*Fighter{fu(1, 10, 10000000, 1, 21778)}, foes, 1)
	for i, c := range cmds {
		if c.ID == CmdActDo && c.Magic == 21778 {
			if m := cmds[i+1]; m.ID != CmdMove || m.Actor != 105 || m.Magic != 106 {
				t.Fatalf("after 脱帽退敌: %+v", m)
			}
			return
		}
	}
	t.Fatal("脱帽退敌 never released")
}

// TestBiZhenSwap：避阵10 治疗濒危（15%）且有入阵技（蟠桃宴）的队友；回血后不再濒危也照样有一半概率与后场换位
// （濒危在回血之前判断，官方：羲和 14% → 回血后 39% 仍换位）。
func TestBiZhenSwap(t *testing.T) {
	for seed := uint64(0); seed < 30; seed++ {
		_, cmds := simCheck(t, []*Fighter{fu(1, 1, 100000, 1, 20450), fuh(2, 1, 150, 1000, 1, 20955), fu(3, 1, 100000, 1),
			fu(4, 1, 100000, 1), fu(5, 1, 100000, 1), fu(6, 1, 1000, 1)},
			[]*Fighter{fu(101, 1, 100000, 1)}, seed)
		for i, c := range cmds {
			if c.ID == CmdActDo && c.Magic == 20450 && i+2 < len(cmds) && cmds[i+2].ID == CmdMove && cmds[i+2].Actor == 2 {
				return
			}
		}
	}
	t.Fatal("避阵 never swapped the endangered 蟠桃宴 holder")
}

// TestWanxianSword：仙帝10（100% 群体回复）+ 万仙来朝4（受回复者 16% 得仙剑）；队友满血没有实际回血也判定，
// 能拿到仙剑（SWORD_EARTH）。
func TestWanxianSword(t *testing.T) {
	god := func(f *Fighter) *Fighter { return raced(f, raceGod) }
	for seed := uint64(0); seed < 20; seed++ {
		_, cmds := simCheck(t, []*Fighter{god(fu(1, 10, 100000, 10, 20540)), god(fu(2, 10, 100000, 1, 22004)),
			god(fu(3, 10, 100000, 1)), god(fu(4, 10, 100000, 1))}, []*Fighter{fu(101, 0, 1000000, 1)}, seed)
		for _, c := range cmds {
			if c.ID == CmdSwordEarth {
				return
			}
		}
	}
	t.Fatal("仙帝 + 万仙来朝 never gave a sword")
}

// TestHengChongShenYou：横冲直撞打死的目标被神佑当场复活也算击败，继续追击；每次追击前判定突袭（突袭10 必出），
// 追击打完突袭加攻清除。
func TestHengChongShenYou(t *testing.T) {
	for seed := uint64(0); seed < 40; seed++ {
		_, cmds := simCheck(t, []*Fighter{fu(1, 1000, 100000, 10, 22206, 20120)},
			[]*Fighter{fu(101, 0, 10, 1, 20500), fu(102, 0, 10, 1), fu(103, 0, 10, 1), fu(104, 0, 10, 1), fu(105, 0, 10, 1)}, seed)
		revived, chase, tuxiAfterChase := false, 0, 0
		for i, c := range cmds {
			if c.ID == CmdTurn && i > 0 && (revived || chase > 0) {
				break
			}
			if c.ID == CmdRelive && c.Magic == 20500 && chase == 0 {
				revived = true
			}
			if c.ID == CmdActDo && c.Magic == 22206 {
				chase++
				if i+1 < len(cmds) && cmds[i+1].ID == CmdActDo && cmds[i+1].Magic == 20120 {
					tuxiAfterChase++
				}
			}
		}
		if !revived {
			continue
		}
		if chase == 0 || tuxiAfterChase != chase {
			t.Fatalf("seed %d: chase %d tuxi %d after 神佑", seed, chase, tuxiAfterChase)
		}
		return
	}
	t.Fatal("神佑 never revived the first kill")
}

// TestFenJiSameHit：分击——主目标与先攻最高的敌人同一下、同样的伤害（a% 攻击力），第二下紧跟主攻击的扣血。
func TestFenJiSameHit(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fu(1, 1000, 100000, 10, 20769)},
		[]*Fighter{fu(101, 0, 100000, 1), fu(102, 0, 100000, 9)}, 1)
	for i, c := range cmds {
		if c.ID != CmdSubHP || c.Magic != attackNormal || len(c.Targets) != 1 {
			continue
		}
		main := c.Targets[0]
		if i+2 >= len(cmds) || cmds[i+1].ID != CmdActDo || cmds[i+1].Magic != 20769 || cmds[i+2].ID != CmdSubHP {
			t.Fatalf("split hit not right after main: %+v %+v", cmds[i+1], cmds[i+2])
		}
		second := cmds[i+2].Targets[0]
		if second[0] != 102 || second[1] != main[1] || main[1] != 950 {
			t.Fatalf("main %v second %v", main, second)
		}
		return
	}
	t.Fatal("no attack")
}

// TestHengChongDeathSkill：敌方带死亡技（狂怒焰、遗香、罗网、归元、还魂、魂守）时横冲直撞照样连续追击。
func TestHengChongDeathSkill(t *testing.T) {
	for _, d := range []int32{21141, 21081, 21091, 21461, 20501, 20651} {
		_, cmds := simCheck(t, []*Fighter{fu(1, 1000, 100000, 10, 22206)},
			[]*Fighter{fu(101, 0, 10, 1, d), fu(102, 0, 10, 1, d), fu(103, 0, 10, 1, d), fu(104, 0, 10, 1, d), fu(105, 0, 10, 1, d)}, 1)
		deaths, chase := 0, 0
		for _, c := range cmds {
			if c.ID == CmdTurn && deaths > 0 {
				break
			}
			if c.ID == CmdDie {
				deaths += len(c.Targets)
			}
			if c.ID == CmdActDo && c.Magic == 22206 {
				chase++
			}
		}
		if chase != 4 {
			t.Fatalf("death skill %d: chase %d, want 4", d, chase)
		}
	}
}

// TestElementAttackDebuff：破岩 / 踏浪 / 驱雷 / 蹈火触发后给受击者挂毒砂5 / 僵化 / 麻痹 / 燃烧5。
func TestElementAttackDebuff(t *testing.T) {
	for _, c := range []struct{ tal, buff int32 }{{20160, 5035}, {20150, 5001}, {20140, 5011}, {20170, 5025}} {
		found := false
		for seed := uint64(0); seed < 10 && !found; seed++ {
			_, cmds := simCheck(t, []*Fighter{fu(1, 10, 100000, 10, c.tal)}, []*Fighter{fu(101, 10, 100000, 1)}, seed)
			found = hasCmd(cmds, CmdBuff, int64(c.buff), 101, -1)
		}
		if !found {
			t.Fatalf("talent %d never applied buff %d", c.tal, c.buff)
		}
	}
}

// TestRenWuFiveTargets：刃舞满 5 层蓄力挥扫 5 个目标（可含主目标）。
func TestRenWuFiveTargets(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fu(5, 10, 1000000, 1, 21534)}, // 5 号位：敌方 1-5 号位先各打它一下，攒满 5 层
		[]*Fighter{fu(101, 1, 100000, 10), fu(102, 1, 100000, 10), fu(103, 1, 100000, 10), fu(104, 1, 100000, 10), fu(105, 1, 100000, 10)}, 1)
	for _, c := range cmds {
		if c.ID == CmdActDo && c.Magic == 21534 {
			if len(c.Targets) != 5 {
				t.Fatalf("刃舞 targets %d, want 5", len(c.Targets))
			}
			return
		}
	}
	t.Fatal("刃舞 never released")
}

// TestZhuangJiGuard：撞击打的是实际受击者——被守卫保护的目标不受撞击伤害。
func TestZhuangJiGuard(t *testing.T) {
	_, cmds := simCheck(t, []*Fighter{fuh(1, 0, 100, 100000, 1), fu(2, 0, 1000000, 1, 20759)},
		[]*Fighter{fu(101, 100, 1000000, 10, 120481)}, 1)
	hitGuard := false
	for _, c := range cmds {
		if c.ID == CmdSubHP && c.Magic == 120481 {
			for _, x := range c.Targets {
				if x[0] == 1 {
					t.Fatal("protected unit took 撞击 damage")
				}
				hitGuard = hitGuard || x[0] == 2
			}
		}
	}
	if !hitGuard {
		t.Fatal("撞击 never hit the guard")
	}
}
