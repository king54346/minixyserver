package gamedata

import "fmt"

// 官方服用 hotfix 补进客户端的道具（analysis/data/hotfix/11_1.5.403.lua）：客户端表里没有，服务端照着补，
// 登录时同一份 hotfix 会下发给客户端，两边一致。
//
//	10452 五级宝石袋（复制 10404 四级宝石袋，etc 56328）：「打开100%获得一颗五级宝石！」，道具商城 1620 元宝。
//	56328 这条奖励客户端也没有；照 24177（四级宝石袋：GPXBS4:10000:1）写成 GPXBS5。
func addHotfixItems() {
	if Items[10452] == nil {
		if src := Items[10404]; src != nil {
			it := *src
			it.Name, it.Etc, it.Value = "五级宝石袋", 56328, 169
			Items[10452] = &it
		}
	}
	if Rewards[56328] == nil {
		Rewards[56328] = &RewardInfo{Drops: []string{"GPXBS5:10000:1"}, Desc: "五级宝石袋"}
	}
	// 阵法书（用户 zhenfa.md：荡妖 200 功德一本）：荡妖功德商店「随机阵法书」每轮必上架、限购 10，开出八本阵法书之一。
	if Rewards[RewardRandomZhenfaBook] == nil {
		drops := ""
		for t := 10432; t <= 10439; t++ {
			if drops != "" {
				drops += ","
			}
			drops += fmt.Sprintf("WP%d:1250:1", t)
		}
		Rewards[RewardRandomZhenfaBook] = &RewardInfo{Drops: []string{"", drops}, Desc: "随机阵法书"}
		Dangyao.Shop = append(Dangyao.Shop, DangyaoShopItem{Type: 3, Pos: 50, Name: "随机阵法书", Price: 200, Left: 10,
			Order: 1, Need: 1, CA: RewardRandomZhenfaBook, Icon: 311326, Desc: "开启将获得一本随机阵法书", Weight: [3]int{1000, 0, 0}})
	}
}

// RewardRandomZhenfaBook 是本服加的「随机阵法书」奖励号（客户端奖励表里没有）。
const RewardRandomZhenfaBook = 90001

// HuanjingOnly 是只能在奇缘幻境里得到的法术书 / 装备（客户端 magic / suit 表 from = 「幻境」）：天地元阳 14506、
// 魂飞魄散 14507、烛龙之息 14508、放下屠刀 14509、射日弓 1016。不进宝箱等随机掉落池（BooksByStar / SuitsByStar）。
var HuanjingOnly = map[int]bool{14506: true, 14507: true, 14508: true, 14509: true, 1016: true}
