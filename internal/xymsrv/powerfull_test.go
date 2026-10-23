package xymsrv

import (
	"testing"

	"turnrpg/internal/battle"
)

// TestRecordPowerFull：战报 otherparam idx 1 带上每张蓄力卡的满蓄力层数（百草 5、六艺 3），没有蓄力神通的不写。
func TestRecordPowerFull(t *testing.T) {
	team := []*battle.Fighter{{Pos: 1, Talents: []int32{20864}}, {Pos: 2, Talents: []int32{21514}}, {Pos: 3, Talents: []int32{20121}}}
	ps := recordParams(team)
	if len(ps) == 0 || ps[0]["idx"] != 1 || ps[0]["value"] != "1:5|2:3" {
		t.Fatalf("params %v", ps)
	}
}
