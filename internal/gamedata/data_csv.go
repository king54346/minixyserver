package gamedata

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
)

// 静态表 CSV：data/*.csv 是可手改的唯一数据源（首行表头，UTF-8 可带 BOM，列表字段用 ; 分隔），
// 字段说明见 docs/DATA.md。改完重启服务端即生效（go:embed，需重新编译）。

// csvTable 是读进来的一张表：按列名取值，解析错误记在 err 里，读完一行后统一检查。
type csvTable struct {
	name string
	cols map[string]int
	rows [][]string
	row  []string
	line int
	err  error
}

func loadCSV(name string) *csvTable {
	b, err := dataFS.ReadFile("data/" + name)
	if err != nil {
		panic(err)
	}
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
	recs, err := r.ReadAll()
	if err != nil {
		panic("xymsrv: " + name + ": " + err.Error())
	}
	if len(recs) == 0 {
		panic("xymsrv: " + name + ": empty")
	}
	t := &csvTable{name: name, cols: map[string]int{}, rows: recs[1:]}
	for i, c := range recs[0] {
		t.cols[strings.TrimSpace(c)] = i
	}
	return t
}

// each 逐行调用 fn；某格解析失败时 panic 并指出表名、行号、列名。
func (t *csvTable) each(fn func()) {
	for i, r := range t.rows {
		t.row, t.line = r, i+2 // 1 起，含表头
		fn()
		if t.err != nil {
			panic(t.err)
		}
	}
}

func (t *csvTable) str(col string) string {
	i, ok := t.cols[col]
	if !ok {
		panic(fmt.Sprintf("xymsrv: %s: no column %q", t.name, col))
	}
	if i >= len(t.row) {
		return ""
	}
	return t.row[i]
}

func (t *csvTable) fail(col, v string, err error) {
	if t.err == nil {
		t.err = fmt.Errorf("xymsrv: %s line %d column %s=%q: %v", t.name, t.line, col, v, err)
	}
}

func (t *csvTable) num(col string) float64 {
	v := strings.TrimSpace(t.str(col))
	if v == "" {
		return 0
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		t.fail(col, v, err)
	}
	return f
}

func (t *csvTable) int(col string) int { return int(t.num(col)) }

func (t *csvTable) int32(col string) int32 { return int32(t.num(col)) }

func (t *csvTable) list(col string) []string {
	v := strings.TrimSpace(t.str(col))
	if v == "" {
		return []string{}
	}
	return strings.Split(v, ";")
}

func (t *csvTable) int32s(col string) []int32 {
	out := []int32{}
	for _, s := range t.list(col) {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 32)
		if err != nil {
			t.fail(col, s, err)
		}
		out = append(out, int32(n))
	}
	return out
}

func (t *csvTable) floats(col string) []float64 {
	out := []float64{}
	for _, s := range t.list(col) {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			t.fail(col, s, err)
		}
		out = append(out, f)
	}
	return out
}

// TalentSetInfo 是一个神通族（auto/talentset.lua）：该族各级神通号为 StartID..EndID。
type TalentSetInfo struct {
	Name    string
	Kind    int
	Pri     int
	StartID int32
	EndID   int32
	Need    int
	Trigger string
	Desc    string
}

func loadPetCSV() map[int32]*PetInfo {
	out := map[int32]*PetInfo{}
	t := loadCSV("pet.csv")
	t.each(func() {
		p := &PetInfo{Type: t.int32("type"), Name: t.str("name"), Race: t.int("race"), Star: t.int("star"),
			Cost: t.int("cost"), HPA: t.int("hp_a"), HPB: t.int("hp_b"), AttA: t.int("att_a"), AttB: t.int("att_b"),
			Speed: t.int("speed"), Magic: t.int32("magic"), LeaderSkill: t.int32("leaderskill"), Pools: t.list("pools"),
			Relate: t.int32s("relate"), Special: t.int("is_special") == 1,
			Upgrade: t.int32("upgrade")}
		for i := range p.Talents {
			p.Talents[i] = t.int32s(fmt.Sprintf("talent%d", i+1))
			p.ExTalents[i] = t.int32s(fmt.Sprintf("ex_talent%d", i+1))
		}
		out[p.Type] = p
	})
	return out
}

func loadMagicCSV() map[int32]*MagicInfo {
	out := map[int32]*MagicInfo{}
	t := loadCSV("magic.csv")
	t.each(func() {
		out[t.int32("id")] = &MagicInfo{Name: t.str("name"), Star: t.int("star"), Kind: t.int("kind"),
			Target: t.int("target"), Hurt: [2]float64{t.num("hurt_a"), t.num("hurt_b")}, Rate: t.int("rate"),
			Times: t.int("times"), CondZY: t.int("cond_zy"), CostZY: [2]float64{t.num("cost_zy_a"), t.num("cost_zy_b")},
			SP: [3]int{t.int("sp_type"), t.int("sp_param"), t.int("sp_grow")}, Buff: t.int32("buffid"),
			BuffRate: [2]float64{t.num("buff_rate_a"), t.num("buff_rate_b")},
			JXCost:   t.int("jingxiu_cost"), JXBuff: t.int32("jingxiu_buff"), TargetParam: t.int32s("target_param")}
		m := out[t.int32("id")]
		for _, r := range strings.Split(t.str("cond_round"), ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(r)); err == nil {
				m.CondRound = append(m.CondRound, n)
			}
		}
		typ, par, grow := t.floats("ext_sp_type"), t.floats("ext_sp_param"), t.floats("ext_sp_grow")
		for i := range typ {
			e := [3]int{int(typ[i]), 0, 0}
			if i < len(par) {
				e[1] = int(par[i])
			}
			if i < len(grow) {
				e[2] = int(grow[i])
			}
			m.ExtSP = append(m.ExtSP, e)
		}
	})
	return out
}

func loadSuitCSV() map[int]*SuitInfo {
	out := map[int]*SuitInfo{}
	t := loadCSV("suit.csv")
	t.each(func() {
		out[t.int("id")] = &SuitInfo{Name: t.str("name"), Star: t.int("star"), Kind: t.str("kind"),
			Prop: t.str("prop"), Add: [2]float64{t.num("add_a"), t.num("add_b")}, Speed: t.int("speed")}
	})
	return out
}

func loadTalentCSV() map[int32]*TalentInfo {
	out := map[int32]*TalentInfo{}
	t := loadCSV("talent.csv")
	t.each(func() {
		out[t.int32("id")] = &TalentInfo{Family: t.str("fname"), Param: t.floats("param"), Buff: t.int32("buffid"),
			BuffRate: t.int("buffrate"), BuffTarget: t.int("bufftarget"), TJParam: t.floats("tjparam"), Need: t.int("need")}
	})
	first := map[string]int32{} // 族名 -> 该族最小的神通号
	for id, ti := range out {
		if f, ok := first[ti.Family]; ti.Family != "" && (!ok || id < f) {
			first[ti.Family] = id
		}
	}
	for _, ti := range out {
		ti.FamilyID = first[ti.Family]
	}
	return out
}

// LeaderSkillInfo 是一个队长技（auto/leaderskill.lua）。原表只有文字描述，导出时拆成：
// Race 生效种族（0 = 所有单位，1 人 2 魔 3 仙 4 兽）、Effect 效果键、Tier 1 少量 / 2 中量 / 3 大量。
type LeaderSkillInfo struct {
	Name   string
	Race   int
	Effect string // dmg mdmg ndmg crit hit res mres nres dodge critres healout healin
	Tier   int
}

// RelateInfo 是一条缘分（auto/relate.lua）。条件三选一：NPet（这些护法都在出战阵容）、NEquip（穿着其中一件装备）、
// NMagic（穿着其中一本法术书）；加成 Att / HP 是百分比，下标与条件对应（第 1 件 / 第 2 件…，护法类只有 1 个）。
// NEquip / NMagic 也是神通激发的「有缘」判定。大缘（together_*）未实现。
type RelateInfo struct {
	Name   string
	NEquip []int32
	NMagic []int32
	NPet   []string
	Att    []float64
	HP     []float64
	// 同修（大缘）：together_npet 的第一名也在场时进阶，加成取 together_att / together_hp（xymsrv/relate.go）。
	TogetherNPet []string
	TogetherAtt  []float64
	TogetherHP   []float64
}

func loadRelateCSV() map[int32]*RelateInfo {
	out := map[int32]*RelateInfo{}
	t := loadCSV("relate.csv")
	t.each(func() {
		r := &RelateInfo{Name: t.str("name"), NEquip: t.int32s("nequip"), NMagic: t.int32s("nmagic"),
			Att: t.floats("att"), HP: t.floats("hp")}
		if s := t.str("npet"); s != "" {
			r.NPet = strings.Split(s, ",")
		}
		if s := t.str("together_npet"); s != "" {
			r.TogetherNPet = strings.Split(s, ",")
			r.TogetherAtt, r.TogetherHP = t.floats("together_att"), t.floats("together_hp")
		}
		out[t.int32("id")] = r
	})
	return out
}

// loadRelateRepCSV：缘分护法名替换（auto/relate_rep.lua），双向登记。
func loadRelateRepCSV() map[string]string {
	out := map[string]string{}
	t := loadCSV("relate_rep.csv")
	t.each(func() {
		a, b := t.str("name"), t.str("rep")
		out[a], out[b] = b, a
	})
	return out
}

// RelatedEquip / RelatedMagic 报告卡牌 pet 与装备 / 法术书类型 typ 是否有缘（relate_mgr.isSuitRelatedByPet 等）。
func (p *PetInfo) RelatedEquip(typ int32) bool { return p.related(typ, false) }
func (p *PetInfo) RelatedMagic(typ int32) bool { return p.related(typ, true) }

func (p *PetInfo) related(typ int32, magic bool) bool {
	for _, id := range p.Relate {
		r := Relates[id]
		if r == nil {
			continue
		}
		l := r.NEquip
		if magic {
			l = r.NMagic
		}
		for _, x := range l {
			if x == typ {
				return true
			}
		}
	}
	return false
}

func loadLeaderSkillCSV() map[int32]*LeaderSkillInfo {
	out := map[int32]*LeaderSkillInfo{}
	t := loadCSV("leaderskill.csv")
	t.each(func() {
		out[t.int32("id")] = &LeaderSkillInfo{Name: t.str("name"), Race: t.int("race"), Effect: t.str("effect"),
			Tier: t.int("tier")}
	})
	return out
}

func loadTalentSetCSV() map[int32]*TalentSetInfo {
	out := map[int32]*TalentSetInfo{}
	t := loadCSV("talentset.csv")
	t.each(func() {
		out[t.int32("id")] = &TalentSetInfo{Name: t.str("name"), Kind: t.int("kind"), Pri: t.int("pri"),
			StartID: t.int32("startid"), EndID: t.int32("endid"), Need: t.int("need"), Trigger: t.str("trigger"),
			Desc: t.str("desc")}
	})
	return out
}
