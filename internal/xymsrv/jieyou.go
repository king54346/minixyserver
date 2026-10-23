package xymsrv

import (
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 解忧（ETC_PTO C_OPEN_JOKE / C_MAKE_JOKE）：每天一位三界丽人，每天能「打招呼」一次（点亮一颗心、得一份回赠），
// 点过之后当天 finish = 1：客户端隐藏按钮、显示当天的笑话。心按天累计，第 4 颗心领大奖，之后（第二天）从 0 颗重新累计。
//
// 依据：view/JokingMainView.lua（role 是丽人的卡牌号、max 颗心、times 已点亮、finish 1 隐藏「打招呼」按钮并显示笑话；
// 点按钮后 refreshState = 1，收到 S_OPEN_JOKE 时播放第 times 颗心飞入的动画）；
// 抓包 S_OPEN_JOKE{role 13002 / 23005, max 4, times 4, finish 1}（连续两天、两位不同的 4 星丽人，都是 4 颗心且已点过）；
// 装备表里 1501–1506 六双鞋的来源只写「解忧」。
//
// 估计（没有抓到 C_MAKE_JOKE 的回包）：丽人按日期在 4 星丽人里轮换；前 3 颗心各给 2000 × 心数 银两，
// 30% 再给一双 1 星鞋；第 4 颗心必给一双鞋（1 / 2 / 3 星 50% / 35% / 15%）。

const jokeMax = 4

// jokeRoles 是轮换的丽人（抓包里出现过 13002 貂蝉、23005 玉面公主）。封面是 char/big/<role>.png，
// 只挑客户端包里自带大图（script/char_big_<role>.plist）的女性卡，否则封面空白：
// 貂蝉、西施、女王、铁扇公主、嫦娥、碧霞元君、王母娘娘、观音姐姐。
var jokeRoles = []int32{13002, 13003, 14003, 24005, 33006, 34003, 34007, 34009}

// jokeShoes 是各星级的鞋（suit 1501–1506，来源「解忧」）。
var jokeShoes = map[int][]int{1: {1501, 1502}, 2: {1503, 1504}, 3: {1505, 1506}}

// JokeState 是解忧存档（Player.Joke）：Day 是最近一次打招呼的日期，Times 是累计的心（≤ 4）。
type JokeState struct {
	Day   string `json:"day"`
	Times int    `json:"times"`
}

func (p *Player) joke() *JokeState {
	if p.Joke == nil {
		p.Joke = &JokeState{}
	}
	if j := p.Joke; j.Times >= jokeMax && j.Day != today() { // 满心那天已领奖，跨天后重新累计（用户要求）
		j.Times = 0
	}
	return p.Joke
}

func (j *JokeState) doneToday() bool { return j.Day == today() && j.Times > 0 }

func jokeRole(now time.Time) int32 {
	day := (now.Unix() + 8*3600) / 86400 // 北京时间的日序（客户端选笑话也这么算）
	return jokeRoles[day%int64(len(jokeRoles))]
}

func (s *Session) jokeMsg(p *Player) {
	j := p.joke()
	s.SendExt("ETC_PTO.S_OPEN_JOKE", xym.Msg{"role": jokeRole(time.Now()), "max": jokeMax, "times": j.Times,
		"finish": b2i(j.doneToday())})
}

func init() {
	Handle("ETC_PTO.C_OPEN_JOKE", func(s *Session, p *Player, m xym.Msg) { s.jokeMsg(p) })
	Handle("ETC_PTO.C_MAKE_JOKE", onMakeJoke)
}

// onMakeJoke：点亮一颗心并发回赠（背包更新、S_ATTR_UPDATE、S_REWARD_MSG{type 1}），再回 S_OPEN_JOKE 播放动画。
func onMakeJoke(s *Session, p *Player, m xym.Msg) {
	j := p.joke()
	if j.doneToday() { // 按钮已隐藏，正常点不到；不回 S_OPEN_JOKE（会重播点亮动画）
		s.retMsg("佳丽今天已经很开心了，明天再来吧。")
		return
	}
	j.Day, j.Times = today(), min(j.Times+1, jokeMax)
	p.track(evJoke, 1)
	rng := s.actRng()
	delta := &bagDelta{}
	cash := int32(0)
	shoe := 0
	if j.Times < jokeMax {
		cash = int32(2000 * j.Times)
		if rng.IntN(100) < 30 {
			shoe = 1
		}
	} else {
		shoe = randStar([5]int{50, 35, 15}, rng)
	}
	if l := jokeShoes[shoe]; len(l) > 0 && gamedata.Suits[l[0]] != nil {
		p.giveSuit(l[rng.IntN(len(l))], delta)
	}
	p.saveLog(s)
	if cash > 0 {
		s.roleUpdate(func(ro *Role) bool { ro.Cash += cash; return true }, "cash")
	}
	list := s.sendDelta(p, delta)
	r := rewardMsg(1, list)
	r["cash"] = cash
	s.SendExt("ETC_PTO.S_REWARD_MSG", r)
	s.jokeMsg(p)
}
