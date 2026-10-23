package xymsrv

import (
	"testing"

	"turnrpg/internal/xym"
)

// TestMiscPanels：系统设置、充值状态、录像收藏数、战力提升「获得材料」页都回客户端等的消息。
func TestMiscPanels(t *testing.T) {
	c, p, _ := actWorld(t, "miscer")
	c.sendExt("ETC_PTO.C_OPEN_SYSTEM_SETTING", xym.Msg{"uid": int64(p.UID)})
	c.expectExt("ETC_PTO.S_OPEN_SETTING_VIEW")
	c.sendExt("ETC_PTO.C_ON_QUERY_COST_MONEY_STATUS", nil)
	c.expectExt("ETC_PTO.S_SYNC_COST_MONEY_STATUS")
	c.sendExt("ETC_PTO.C_GET_SAVE_RECORD_INFO", nil)
	if m := c.expectExt("ETC_PTO.S_SEND_SAVE_RECORD_INFO"); num(m, "max_num") != 10 {
		t.Fatalf("save record %v", m)
	}
	c.sendExt("SYSTEM_NOTICE_PTO.C_OPEN_ZHANLI_MATERIAL", nil)
	m := c.expectExt("SYSTEM_NOTICE_PTO.S_OPEN_ZHANLI_VIEW")
	cats := m["categories"].([]xym.Msg)
	first := cats[0]["list"].([]xym.Msg)[0]
	if num(m, "page") != 2 || len(cats) != 3 || first["icon"] != "icon/110001.png" || len(first["tips"].([]xym.Msg)) != 2 {
		t.Fatalf("zhanli %v", m)
	}
}

// TestRuleTips：各界面的「?」都有规则说明（客户端用到的 text_id 全覆盖），未知编号也回一条。
func TestRuleTips(t *testing.T) {
	c, _, _ := actWorld(t, "ruletips")
	for _, id := range []int64{1001, 1004, 1009, 1011, 1013, 1029, 9999} {
		c.sendExt("ETC_PTO.C_OPEN_RULE_TIPS", xym.Msg{"text_id": id})
		if m := c.expectExt("ETC_PTO.S_OPEN_RULE_TIPS"); m["text"] == "" {
			t.Fatalf("rule tips %d empty", id)
		}
	}
}
