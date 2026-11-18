package xymsrv

import (
	"testing"
	"turnrpg/internal/xym"
)

func TestEncodeTerrMsgs(t *testing.T) {
	resetTerr()
	c, p, _, foe := terrSetup(t, "terr4")
	c.sendExt("DONGFU_TERRITORY_PTO.C_TERRITORY_SIGN", xym.Msg{"vs_dongfu_id": foe, "gold": int64(0)})
	m := terrOpenMsg(t, c)
	for _, name := range []string{"S_TERRITORY_OPEN_MAIN", "S_TERRITORY_ALL_USER_LIST", "S_TERRITORY_RANK", "S_TERRITORY_AVAIL_VS_DONGFU_LIST", "S_TERRITORY_MONSTER_INFO", "S_TERRITORY_LINEUP_INFO", "S_TERRITORY_CHALLENGE_USER_LIST", "S_TERRITORY_EJECT_USER_LIST", "S_TERRITORY_RECORD_LIST"} {
		spec := xym.ByName("DONGFU_TERRITORY_PTO." + name)
		if spec == nil {
			t.Fatalf("missing spec %s", name)
		}
		var msg xym.Msg
		switch name {
		case "S_TERRITORY_OPEN_MAIN":
			msg = m
		case "S_TERRITORY_ALL_USER_LIST":
			msg = xym.Msg{"now_time": 1, "all_user_list": []xym.Msg{}, "gate_list": []xym.Msg{}}
		case "S_TERRITORY_RANK":
			msg = xym.Msg{"list": []xym.Msg{}}
		case "S_TERRITORY_AVAIL_VS_DONGFU_LIST":
			msg = xym.Msg{"min_gold": 0, "max_gold": 0, "status": 0, "list": []xym.Msg{}}
		case "S_TERRITORY_MONSTER_INFO":
			msg = xym.Msg{"left_time": 1, "num": 1, "max_num": 1}
		case "S_TERRITORY_LINEUP_INFO":
			msg = xym.Msg{"current_lineup_no": 1, "list": []xym.Msg{}}
		case "S_TERRITORY_CHALLENGE_USER_LIST":
			msg = xym.Msg{"challenge_type": 0, "cd_left_time": 0, "list": []xym.Msg{}}
		case "S_TERRITORY_EJECT_USER_LIST":
			msg = xym.Msg{"cd_left_time": 0, "list": []xym.Msg{}}
		case "S_TERRITORY_RECORD_LIST":
			msg = xym.Msg{"list": []xym.Msg{}}
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s encode: %v", name, r)
				}
			}()
			_ = spec.MustEncode(msg)
		}()
	}
	_ = p
}
