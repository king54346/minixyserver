package xymsrv

import (
	"strconv"
	"strings"

	"turnrpg/internal/xym"
)

// 零散的界面协议（服务器日志里玩家点到过、之前只靠兜底解锁的）。
//
//   - ETC_PTO.C_APPLY_TH：主界面 / 顶栏定时发；官方抓包（session-01/02 十余次）从不回包，这里也不回，只是不再记「unhandled」。
//   - ETC_PTO.C_OPEN_SYSTEM_SETTING{uid}：「系统设置」按钮，客户端收到 S_OPEN_SETTING_VIEW{list[val, key]} 才打开设置界面
//     （view/SettingView.lua；声音、光影、登出都是本地选项，list 是服务端开关，没有就发空表）。
//   - ETC_PTO.C_ON_QUERY_COST_MONEY_STATUS：元宝商铺 / 特权页打开时查询，回 S_SYNC_COST_MONEY_STATUS{status 0}（本服没有充值）。
//   - ETC_PTO.C_GET_SAVE_RECORD_INFO：人物信息页查询录像收藏数，回 S_SEND_SAVE_RECORD_INFO（本服不存录像：0 / 10）。
//   - SYSTEM_NOTICE_PTO.C_OPEN_ZHANLI_MATERIAL：「战力提升」界面第 2 页「获得材料」（view/ZhanliTishengView.lua；第 1 页是客户端本地数据）。
//     每项的跳转按钮文字交给 ItemDetailView.jumpByText，取道具表 auto/item.lua 的 tips_from；图标 icon/<model>.png。

// zlMaterial 是「获得材料」页的一项（名字、图标 model、说明、跳转）。
type zlMaterial struct {
	name  string
	model int
	desc  string
	from  string // 「、」分隔的跳转文字（item.lua tips_from）
}

var zlMaterials = []struct {
	title string
	list  []zlMaterial
}{
	{"护法", []zlMaterial{
		{"战力丹", 110001, "护法「培养」提升攻击力。", "功德殿、论道"},
		{"血气丹", 110001, "护法「培养」提升生命。", "功德殿、论道"},
		{"三星筑基丹", 110001, "三星护法飞升到四星的材料。", "功德殿、论道"},
		{"四星筑基丹", 110001, "四星护法飞升到五星的材料。", "功德殿、论道"},
		{"请神符", 110091, "招募护法。", "道具商城"},
	}},
	{"装备", []zlMaterial{
		{"太阳石", 110107, "武器 20 级后可镶嵌，增加物理攻击。", "每日礼包、论道、合成"},
		{"炼神石", 110106, "重置装备套装神通技效果。", "论道、道具商城"},
	}},
	{"法术", []zlMaterial{
		{"悟法随想", 110069, "增加法术强化经验。", "秘境"},
		{"悟法心得", 110065, "增加法术强化经验。", "秘境"},
		{"淬元沙", 110097, "洗炼成元气粒子，用于法术精修。", "洗炼、论道"},
	}},
}

func zhanliMaterialMsg() xym.Msg {
	cats := []xym.Msg{}
	for _, c := range zlMaterials {
		list := []xym.Msg{}
		for _, it := range c.list {
			tips := []xym.Msg{}
			for _, t := range strings.Split(it.from, "、") {
				tips = append(tips, xym.Msg{"tips_name": t})
			}
			list = append(list, xym.Msg{"icon": "icon/" + strconv.Itoa(it.model) + ".png", "name": it.name, "desc": it.desc, "tips": tips})
		}
		cats = append(cats, xym.Msg{"sub_title": c.title, "list": list})
	}
	return xym.Msg{"page": 2, "page_title": "获得材料", "categories": cats}
}

func init() {
	Handle("ETC_PTO.C_APPLY_TH", func(s *Session, p *Player, m xym.Msg) {})
	Handle("ETC_PTO.C_OPEN_SYSTEM_SETTING", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("ETC_PTO.S_OPEN_SETTING_VIEW", xym.Msg{"list": []xym.Msg{}})
	})
	Handle("ETC_PTO.C_ON_QUERY_COST_MONEY_STATUS", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("ETC_PTO.S_SYNC_COST_MONEY_STATUS", xym.Msg{"status": 0})
	})
	Handle("ETC_PTO.C_GET_SAVE_RECORD_INFO", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("ETC_PTO.S_SEND_SAVE_RECORD_INFO", xym.Msg{"current_num": 0, "max_num": 10, "avail_save_pos": 1})
	})
	Handle("SYSTEM_NOTICE_PTO.C_OPEN_ZHANLI_MATERIAL", func(s *Session, p *Player, m xym.Msg) {
		s.SendExt("SYSTEM_NOTICE_PTO.S_OPEN_ZHANLI_VIEW", zhanliMaterialMsg())
	})
}
