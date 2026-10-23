package gamedata

import (
	"reflect"
	"testing"
)

func TestCSVTables(t *testing.T) {
	for name, n := range map[string]int{"magic": len(Magics), "suit": len(Suits), "pet": len(Pets),
		"talent": len(Talents), "talentset": len(TalentSets)} {
		want := map[string]int{"magic": 935, "suit": 125, "pet": 448, "talent": 2713, "talentset": 190}[name]
		if n != want {
			t.Errorf("%s: %d rows, want %d", name, n, want)
		}
	}

	p := Pets[11001]
	if p == nil || p.Name != "南郭先生" || p.Race != 1 || p.Star != 1 || p.Magic != 15001 ||
		!reflect.DeepEqual(p.Talents[0], []int32{20321, 20322}) || len(p.Talents[2]) != 0 ||
		!reflect.DeepEqual(p.Pools, []string{"银两", "友情点"}) || p.HP(1) != 8800/1000+88 {
		t.Errorf("pet 11001 %+v", p)
	}
	if p := Pets[15029]; p == nil || p.Name != "薛仁贵" || p.Star != 5 { // 原表跨行的一条
		t.Errorf("pet 15029 %+v", p)
	}
	if m := Magics[11101]; m == nil || m.Name != "混水腿法" || m.Hurt != [2]float64{2222, 141} ||
		m.Target != 8 || m.Rate != 20 || m.Times != 2 || m.CondZY != 40 || m.CostZY != [2]float64{2, 0} {
		t.Errorf("magic 11101 %+v", m)
	}
	if s := Suits[1001]; s == nil || s.Name != "松木锤" || s.Kind != "ITK_WEAPON" || s.Prop != "att" ||
		s.Add != [2]float64{10, 8} || s.Bonus(1) != 18 {
		t.Errorf("suit 1001 %+v", s)
	}
	if x := Talents[20001]; x == nil || x.Family != "霆威" || !reflect.DeepEqual(x.Param, []float64{10, 2}) {
		t.Errorf("talent 20001 %+v", x)
	}
	if x := TalentSets[20001]; x == nil || x.Name != "霆威" || x.StartID != 20001 || x.EndID != 20010 || x.Trigger != "选取目标" {
		t.Errorf("talentset 20001 %+v", x)
	}
	if n := len(BooksByStar[1]) + len(BooksByStar[2]) + len(BooksByStar[3]) + len(BooksByStar[4]) + len(BooksByStar[5]); n == 0 {
		t.Error("no magic books")
	}
}
