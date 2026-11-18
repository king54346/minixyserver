package xymsrv

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"time"

	"turnrpg/internal/gamedata"
)

// GM 工具（Desktop/mnxy/GM，本地网页 → SSH → 本机管理接口）用的管理接口：
//
//	GET  /catalog                 可发放物品目录 [{code, name, kind, star}]（WP 道具 / DZ 护法卡 / ZB 装备 / FS 法术书）
//	GET  /player?uid=20005        角色详情：角色数值 + 背包概况
//	POST /role {"uid": 20005, "set": {"gold": 1000, "grade": 100, …}}
//	            改角色数值（gold cash soulexp friendpoint grade exp vip tpoint hpoint）；在线的先踢下线；返回改后的角色
//	POST /kick {"uid": 20005}     踢下线
//	GET  /pets?uid=20005          护法列表（含培养 attadd / hpadd、结缘 combine / 同修 together / 可结缘总数 extend），按 npcnum 排序
//	POST /pet_point {"uid": 20005, "npcnum": 10001, "attadd": 202, "hpadd": 202}
//	POST /pet_relate {"uid": 20005, "petnums": [10001, 10002]}   一键开满选中护法的结缘与同修（仅 5 星护法，助人海星除外：结缘 = 全部人物缘（初始缘分 + 可结缘），同修 = 其中可同修的）；踢下线后返回护法列表
//	            改护法培养次数（两个值都可选，缺省不改；范围 0..9999）；在线的先踢下线；返回改后的护法
//	POST /account {"urs": "test002", "pwd": "123456", "code": ""}
//	            注册账号（code 可省，空 = 不设安全码）；成功返回 {uid, urs}，失败 400 {error}
//	GET  /accounts                账号列表 [{uid, urs, created, device, has_role, online}]（按 uid 排序）
//	POST /account_pwd {"urs": "test003", "pwd": "654321"}
//	            重设密码（重新生成盐）；返回 {uid}
// POST /account_delete {"urs": "test005", "confirm": "test005"}
//	            删账号：confirm 必须与 urs 完全一致；在线的先踢下线；连带删角色与玩家存档，
//	            返回 {ok: "deleted", uid}。删除后不可恢复，GM 工具要求手输账号名确认。
//	GET  /dongfus                  洞府列表（只读，不落盘）：[{id, name, lv, members, cap, house_lv, pool_lv, builds:[{type, name, lv, exp}]}]
//	POST /dongfu_build {"dongfu_id": 1, "set": {"attack": 10, "hp": 5, "house": 3, "pool": 2}}
//	            改指定洞府的建筑等级（key 限六座之一，值 0..25 且不超 cap；house/pool 值 1..5 且不超洞府等级；
//	            建筑经验清零；立即写回存档）；返回改后的洞府行
//	POST /dongfu_lv {"dongfu_id": 1, "lv": 3}   改洞府等级（1..5），返回改后的洞府行
//	POST /suit_all {"uid": 20001, "itemnums": [2009, 2012]}   给选中装备赋予全部可用炼神效果（4 星 20 种 / 5 星 22 种：主效果 + 备用列表；3 星及以下拒绝）；踢下线后返回同 GET /bag
//	GET  /bag?uid=20001            角色背包（只读）：{uid, items:[{pos, type, name, amount}], suits:[{itemnum, pos, type, name, grade, star, skill, skill_name}], books:[{pos, type, name, grade}]}
//	POST /bag_add {"uid": 20001, "kind": "item", "id": 101, "n": 10}   添加道具（kind item, n 数量）/ suit / book（id 装备号 / 法术号）；踢下线后返回同 GET /bag
//	POST /bag_del {"uid": 20001, "kind": "item", "key": 3}            删除单格 / 单件（item/book 的 key 是格位 pos，suit 的 key 是 itemnum）
//	POST /bag_del {"uid": 20001, "kind": "item", "type": 10345}       按物品批量删除：删掉该道具号的全部格子（不传 key）；穿戴中的装备 / 法术书拒绝删除；踢下线后返回同 GET /bag
//	GET  /progress?uid=20001       角色关卡进度（只读）：{uid, maps:[{id, count}], common:{last, map, index, stars}, elite:{...}}
//	POST /progress_set {"uid": 20001, "typ": 0, "task": 205}
//	            把普通（typ 0）/ 精英（typ 1）关卡进度设到任务 task（0 = 清空），补全此前任务 1 星通关记录；返回同 GET /progress
//	GET  /xiuwei?uid=20001         角色六座个人修炼（只读）：{uid, grade, limit, list:[{type, name, lv, exp, curr_exp, effect_lv, cap}]}
//	POST /xiuwei_set {"uid": 20001, "set": {"attack": 300, "hp": 0}}
//	            按累计经验设置角色六座修炼（key 限六座之一，值 0..xiuweiTotal(25)=4400，立即写盘，不踢下线）；返回同 GET /xiuwei
//
// 发放物品走 POST /mail（admin.go）。

// gmBuildNames 是六座洞府修为建筑的 key -> 中文名（顺序即 GM 工具里的显示顺序）。
var gmBuildNames = []struct{ Key, Name string }{
	{"attack", "战神山"},
	{"god", "缥缈峰"},
	{"human", "先祖阁"}, // 代码注释里作「先祖祠」
	{"ghost", "噬魂窟"},
	{"hp", "长生殿"},
	{"beast", "蛮荒谷"},
}

const (
	gmBuildLvMax  = 25 // 六座修为建筑上限（洞府满级 5 × 5）
	gmDongfuLvMax = 5  // 洞府等级上限
	gmHouseLvMax  = 5  // 房舍 / 灵池上限（同时不得超过洞府等级）
)

// gmBuildRow 是 GM 接口返回的一座建筑。
type gmBuildRow struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Lv   int    `json:"lv"`
	Exp  int32  `json:"exp"`
}

// gmDongfu 是 GM 接口返回的洞府概况（洞府列表中的一行）。
type gmDongfu struct {
	ID      int32        `json:"id"`
	Name    string       `json:"name"`
	Lv      int32        `json:"lv"`
	Members int          `json:"members"`
	Cap     int          `json:"cap"`
	HouseLv int32        `json:"house_lv"`
	PoolLv  int32        `json:"pool_lv"`
	Builds  []gmBuildRow `json:"builds"`
}

// gmDongfuOf 把 df 转成返回结构（调用方持锁）。读的时候不调用 df.build()，避免凭空创建建筑条目。
func gmDongfuOf(df *Dongfu) gmDongfu {
	out := gmDongfu{ID: df.ID, Name: df.Name, Lv: df.Lv, Members: len(df.Members), Cap: df.buildCap(),
		HouseLv: df.houseLv(), PoolLv: df.poolLv(),
		Builds: make([]gmBuildRow, 0, len(gmBuildNames))}
	for _, b := range gmBuildNames {
		row := gmBuildRow{Type: b.Key, Name: b.Name}
		if got := df.Builds[b.Key]; got != nil {
			row.Lv, row.Exp = got.Lv, got.Exp
		}
		out.Builds = append(out.Builds, row)
	}
	return out
}

// gmDongfus 列出全部洞府及六座建筑等级（只读：do 返回 false，不触发存档）。
func (srv *Server) gmDongfus() []gmDongfu {
	out := []gmDongfu{}
	srv.dongfu.do(func() bool {
		for _, df := range srv.dongfu.List {
			out = append(out, gmDongfuOf(df))
		}
		return false
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// gmSetDongfuBuild 改指定洞府的六座建筑等级（经验清零）或房舍 / 灵池等级，返回改后的洞府行。
// 校验：洞府存在；六座建筑 0..25 且不超过建筑上限 cap；house/pool 1..5 且不超过洞府等级。
func (srv *Server) gmSetDongfuBuild(id int32, set map[string]int) (gmDongfu, error) {
	if len(set) == 0 {
		return gmDongfu{}, errors.New("nothing to set")
	}
	known := make(map[string]bool, len(gmBuildNames))
	for _, b := range gmBuildNames {
		known[b.Key] = true
	}
	for k, v := range set {
		if k == "house" || k == "pool" {
			if v < 1 || v > gmHouseLvMax {
				return gmDongfu{}, errors.New("bad value for " + k + " (1..5)")
			}
			continue
		}
		if !known[k] {
			return gmDongfu{}, errors.New("unknown build " + k)
		}
		if v < 0 || v > gmBuildLvMax {
			return gmDongfu{}, errors.New("bad value for " + k + " (0..25)")
		}
	}
	var out gmDongfu
	var err error
	srv.dongfu.do(func() bool {
		df := srv.dongfu.List[id]
		if df == nil {
			err = errors.New("no such dongfu")
			return false
		}
		// Validate every requested field before mutating any of them.
		for k, v := range set {
			if (k == "house" || k == "pool") && v > int(df.Lv) || k != "house" && k != "pool" && v > df.buildCap() {
				err = errors.New("build level exceeds dongfu capacity")
				return false
			}
		}
		for k, v := range set {
			switch k {
			case "house", "pool":
				if v > int(df.Lv) {
					err = errors.New(k + " lv cannot exceed dongfu lv " + strconv.Itoa(int(df.Lv)))
					return false
				}
				if k == "house" {
					df.HouseLv = int32(v)
				} else {
					df.PoolLv = int32(v)
				}
			default:
				if bcap := df.buildCap(); v > bcap {
					err = errors.New("build lv cannot exceed cap " + strconv.Itoa(bcap) + " (dongfu lv x 5)")
					return false
				}
				b := df.build(k)
				b.Lv, b.Exp = v, 0
			}
		}
		out = gmDongfuOf(df)
		return true
	})
	return out, err
}

// gmSetDongfuLv 改洞府等级（1..5），返回改后的洞府行。
func (srv *Server) gmSetDongfuLv(id, lv int32) (gmDongfu, error) {
	if lv < 1 || lv > gmDongfuLvMax {
		return gmDongfu{}, errors.New("bad dongfu lv (1..5)")
	}
	var out gmDongfu
	var err error
	srv.dongfu.do(func() bool {
		df := srv.dongfu.List[id]
		if df == nil {
			err = errors.New("no such dongfu")
			return false
		}
		df.Lv = lv
		out = gmDongfuOf(df)
		return true
	})
	return out, err
}

// gmXiuweiRow 是 GM 接口返回的一座个人修炼。
type gmXiuweiRow struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Lv      int    `json:"lv"`        // 修炼等级（由累计经验算出）
	Exp     int32  `json:"exp"`       // 当前级内经验
	CurrExp int32  `json:"curr_exp"`  // 累计经验
	Effect  int    `json:"effect_lv"` // 生效等级：min(等级, 洞府建筑等级, 人物等级上限)
	Cap     int    `json:"cap"`       // 所在洞府该建筑等级（0 = 不在洞府）
}

// gmXiuwei 是 GM 接口返回的角色修炼概况。
type gmXiuwei struct {
	UID   int32         `json:"uid"`
	Grade int           `json:"grade"`
	Limit int           `json:"limit"` // 人物等级允许的修炼上限（memberLimit）
	List  []gmXiuweiRow `json:"list"`
}

// gmXiuweiOf 汇总角色六座修炼（调用方持 p.mu）。cap 为洞府实时建筑等级，生效等级 = min(等级, cap, 人物上限)。
func gmXiuweiOf(p *Player, grade int, dfLv map[string]int) gmXiuwei {
	d := p.df()
	limit := memberLimit(grade)
	out := gmXiuwei{UID: p.UID, Grade: grade, Limit: limit, List: make([]gmXiuweiRow, 0, len(gmBuildNames))}
	for _, b := range gmBuildNames {
		lv, exp := xiuweiLv(d.Exp[b.Key])
		cap := dfLv[b.Key]
		out.List = append(out.List, gmXiuweiRow{
			Type: b.Key, Name: b.Name, Lv: lv, Exp: exp, CurrExp: d.Exp[b.Key],
			Effect: min(lv, cap, limit), Cap: cap,
		})
	}
	return out
}

// dongfuBuildLv 读角色所在洞府的六座建筑实时等级；不在洞府返回空 map。
func (srv *Server) dongfuBuildLv(uid int32) map[string]int {
	out := map[string]int{}
	srv.dongfu.do(func() bool {
		df, _ := srv.dongfu.of(uid)
		if df == nil {
			return false
		}
		for _, b := range gmBuildNames {
			if got := df.Builds[b.Key]; got != nil {
				out[b.Key] = got.Lv
			}
		}
		return false
	})
	return out
}

// gmGetXiuwei 读角色六座个人修炼（只读）。
func (srv *Server) gmGetXiuwei(uid int32) (gmXiuwei, error) {
	ro := srv.roles.Get(uid)
	if ro == nil {
		return gmXiuwei{}, errors.New("no such role")
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmXiuwei{}, err
	}
	dfLv := srv.dongfuBuildLv(uid) // 先取洞府数据（锁外），避免 p.mu → dongfu 锁嵌套
	p.mu.Lock()
	defer p.mu.Unlock()
	return gmXiuweiOf(p, ro.Grade, dfLv), nil
}

// gmSetXiuwei 按累计经验设置角色六座修炼，返回改后的概况。
// 校验：角色存在、key 是六座之一、值 0..xiuweiTotal(25)；改完立即写盘，不踢下线。
func (srv *Server) gmSetXiuwei(uid int32, set map[string]int) (gmXiuwei, error) {
	if len(set) == 0 {
		return gmXiuwei{}, errors.New("nothing to set")
	}
	ro := srv.roles.Get(uid)
	if ro == nil {
		return gmXiuwei{}, errors.New("no such role")
	}
	known := make(map[string]bool, len(gmBuildNames))
	for _, b := range gmBuildNames {
		known[b.Key] = true
	}
	maxExp := int(xiuweiTotal(gmBuildLvMax))
	for k, v := range set {
		if !known[k] {
			return gmXiuwei{}, errors.New("unknown xiuwei " + k)
		}
		if v < 0 || v > maxExp {
			return gmXiuwei{}, errors.New("bad value for " + k + " (exp 0.." + strconv.Itoa(maxExp) + ")")
		}
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmXiuwei{}, err
	}
	dfLv := srv.dongfuBuildLv(uid) // 先取洞府数据（锁外），避免 p.mu → dongfu 锁嵌套
	p.mu.Lock()
	defer p.mu.Unlock()
	oldAtt, oldHP := p.dfPanelPct()
	d := p.df()
	d.Caps = dfLv
	for k, v := range set {
		d.Exp[k] = int32(v)
	}
	if err := p.save(); err != nil {
		return gmXiuwei{}, err
	}
	if s := srv.sessionOf(uid); s != nil {
		s.pushXiuweiPanel(p, oldAtt, oldHP)
	}
	return gmXiuweiOf(p, ro.Grade, dfLv), nil
}

// xiuweiTotal 是修炼等级 lv 对应的累计经验（本级经验 0）。
func xiuweiTotal(lv int) int32 {
	var n int32
	for i := 0; i < lv; i++ {
		n += xiuweiNeed(i)
	}
	return n
}

// gmMapRow 是关卡章节表的一行（地图号 / 任务数）。
type gmMapRow struct {
	ID    int `json:"id"`
	Count int `json:"count"`
}

// gmProgressRow 是一种模式（普通 / 精英）的关卡进度。
type gmProgressRow struct {
	Last  int `json:"last"`  // 最后通关的任务号
	Map   int `json:"map"`   // 章节
	Index int `json:"index"` // 章内序号
	Stars int `json:"stars"` // 已通关任务数
}

// gmProgress 是 GM 接口返回的角色关卡进度。
type gmProgress struct {
	UID    int32         `json:"uid"`
	Maps   []gmMapRow    `json:"maps"`
	Common gmProgressRow `json:"common"` // 普通（Prog[0]）
	Elite  gmProgressRow `json:"elite"`  // 精英（Prog[1]）
}

func gmProgressRowOf(pr *Progress) gmProgressRow {
	out := gmProgressRow{Last: pr.Last, Stars: len(pr.Stars)}
	if pr.Last > 0 {
		out.Map, out.Index = pr.Last/100, pr.Last%100
	}
	return out
}

func gmProgressOf(p *Player) gmProgress {
	maps := make([]gmMapRow, 0, len(gamedata.MapTasks))
	for id, n := range gamedata.MapTasks {
		maps = append(maps, gmMapRow{ID: id, Count: n})
	}
	sort.Slice(maps, func(i, j int) bool { return maps[i].ID < maps[j].ID })
	return gmProgress{UID: p.UID, Maps: maps, Common: gmProgressRowOf(p.Prog[0]), Elite: gmProgressRowOf(p.Prog[1])}
}

// gmGetProgress 读角色关卡进度（只读）。
func (srv *Server) gmGetProgress(uid int32) (gmProgress, error) {
	if srv.roles.Get(uid) == nil {
		return gmProgress{}, errors.New("no such role")
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmProgress{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return gmProgressOf(p), nil
}

// gmSetProgress 把指定模式的关卡进度设到任务 task（0 = 清空），并补全此前所有任务 / 关卡的 1 星通关记录。
// 校验：角色存在、typ 0/1、task 必须是存在的任务号；改完立即写盘，不踢下线。
func (srv *Server) gmSetProgress(uid int32, typ, task int) (gmProgress, error) {
	if typ != 0 && typ != 1 {
		return gmProgress{}, errors.New("bad typ (0 = common, 1 = elite)")
	}
	if task < 0 {
		return gmProgress{}, errors.New("bad task")
	}
	if task > 0 {
		if m, i := task/100, task%100; i < 1 || i > gamedata.MapTasks[m] {
			return gmProgress{}, errors.New("bad task " + strconv.Itoa(task))
		}
	}
	if srv.roles.Get(uid) == nil {
		return gmProgress{}, errors.New("no such role")
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmProgress{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pr := p.Prog[typ]
	pr.Last = task
	pr.Stars = map[int]int{}
	pr.Gates = map[int]int{}
	pr.GateStars = map[int]int{}
	if task > 0 {
		ids := make([]int, 0, len(gamedata.MapTasks))
		for id := range gamedata.MapTasks {
			ids = append(ids, id)
		}
		sort.Ints(ids)
	loop:
		for _, m := range ids {
			for i := 1; i <= gamedata.MapTasks[m]; i++ {
				t := m*100 + i
				if t > task {
					break loop
				}
				pr.Stars[t] = 1
				for _, gid := range gamedata.TaskGates[t] {
					pr.Gates[gid] = 1
				}
			}
		}
	}
	if err := p.save(); err != nil {
		return gmProgress{}, err
	}
	return gmProgressOf(p), nil
}

// gmBagItem / gmBagSuit / gmBagBook 是 GM 接口返回的背包条目。
type gmBagItem struct {
	Pos    int    `json:"pos"`
	Type   int    `json:"type"`
	Name   string `json:"name"`
	Amount int32  `json:"amount"`
}

type gmBagSuit struct {
	ItemNum   int32  `json:"itemnum"`
	Pos       int    `json:"pos"`
	Type      int    `json:"type"`
	Name      string `json:"name"`
	Grade     int    `json:"grade"`
	Star      int    `json:"star"`                 // 装备星级（1..5，4 星起可设套装）
	Skill     int32  `json:"skill"`                // 炼神效果（套装神通号，0 = 无）
	SkillName string `json:"skill_name,omitempty"` // 炼神效果族名
	Saved     int    `json:"saved"`                // 备用炼神效果数量
}

type gmBagBook struct {
	Pos   int    `json:"pos"`
	Type  int32  `json:"type"`
	Name  string `json:"name"`
	Grade int    `json:"grade"`
}

// gmBag 是 GM 接口返回的角色背包（道具 / 装备 / 法术书）。
type gmBag struct {
	UID   int32       `json:"uid"`
	Items []gmBagItem `json:"items"`
	Suits []gmBagSuit `json:"suits"`
	Books []gmBagBook `json:"books"`
}

// gmItemNameOf 返回道具名（道具 / 装备 / 法术书 ID 有重叠，必须分表查）。
func gmItemNameOf(typ int) string {
	if it := gamedata.Items[typ]; it != nil {
		return it.Name
	}
	return ""
}

// gmSuitNameOf 返回装备名。
func gmSuitNameOf(typ int) string {
	if si := gamedata.Suits[typ]; si != nil {
		return si.Name
	}
	return ""
}

// gmBookNameOf 返回法术书名。
func gmBookNameOf(typ int32) string {
	if mi := gamedata.Magics[typ]; mi != nil {
		return mi.Name
	}
	return ""
}

func gmBagOf(p *Player) gmBag {
	out := gmBag{UID: p.UID, Items: make([]gmBagItem, 0, len(p.Items)),
		Suits: make([]gmBagSuit, 0, len(p.Suits)), Books: make([]gmBagBook, 0, len(p.Books))}
	for _, it := range p.Items {
		out.Items = append(out.Items, gmBagItem{Pos: it.Pos, Type: it.Type, Name: gmItemNameOf(it.Type), Amount: it.Amount})
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Pos < out.Items[j].Pos })
	for _, su := range p.Suits {
		out.Suits = append(out.Suits, gmBagSuit{ItemNum: su.ItemNum, Pos: su.Pos, Type: su.Type, Name: gmSuitNameOf(su.Type), Grade: su.Grade,
			Star: suitStar(su.Type), Skill: su.Skill, SkillName: gmSuitSkillName(su.Skill), Saved: len(su.Saved)})
	}
	sort.Slice(out.Suits, func(i, j int) bool { return out.Suits[i].ItemNum < out.Suits[j].ItemNum })
	for _, b := range p.Books {
		out.Books = append(out.Books, gmBagBook{Pos: b.Pos, Type: b.Type, Name: gmBookNameOf(b.Type), Grade: b.Grade})
	}
	sort.Slice(out.Books, func(i, j int) bool { return out.Books[i].Pos < out.Books[j].Pos })
	return out
}

// gmGetBag 读角色背包（只读）。
func (srv *Server) gmGetBag(uid int32) (gmBag, error) {
	if srv.roles.Get(uid) == nil {
		return gmBag{}, errors.New("no such role")
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmBag{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return gmBagOf(p), nil
}

// gmAddBag 给角色背包添加道具 / 装备 / 法术书。kind = item（id 道具号, n 数量）/ suit（id 装备号）/ book（id 法术号）。
// 背包是客户端强缓存，改完踢下线以便重新登录拉取。
func (srv *Server) gmAddBag(uid int32, kind string, id int, n int32) (gmBag, error) {
	if srv.roles.Get(uid) == nil {
		return gmBag{}, errors.New("no such role")
	}
	switch kind {
	case "item":
		if gamedata.Items[id] == nil {
			return gmBag{}, errors.New("no such item " + strconv.Itoa(id))
		}
		if n < 1 || n > 30000 {
			return gmBag{}, errors.New("bad amount (1..30000)")
		}
	case "suit":
		if gamedata.Suits[id] == nil {
			return gmBag{}, errors.New("no such suit " + strconv.Itoa(id))
		}
	case "book":
		if gamedata.Magics[int32(id)] == nil {
			return gmBag{}, errors.New("no such book " + strconv.Itoa(id))
		}
	default:
		return gmBag{}, errors.New("bad kind (item / suit / book)")
	}
	if err := srv.kickWait(uid); err != nil {
		return gmBag{}, err
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmBag{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch kind {
	case "item":
		p.addItem(id, n)
	case "suit":
		p.addSuit(id)
	case "book":
		p.addBook(int32(id))
	}
	if err := p.save(); err != nil {
		return gmBag{}, err
	}
	return gmBagOf(p), nil
}

// gmDelBag 删除背包条目：kind = item（key 格位 pos 删整格；key=0 且 typ>0 删该物品的全部格子）/
// suit（key 装备号 itemnum）/ book（key 格位 pos）。正被阵容穿戴的装备 / 法术书拒绝删除。背包是客户端强缓存，改完踢下线。
func (srv *Server) gmDelBag(uid int32, kind string, key, typ int) (gmBag, error) {
	if srv.roles.Get(uid) == nil {
		return gmBag{}, errors.New("no such role")
	}
	if err := srv.kickWait(uid); err != nil {
		return gmBag{}, err
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmBag{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch kind {
	case "item":
		if key > 0 {
			idx := -1
			for i, it := range p.Items {
				if it.Pos == key {
					idx = i
					break
				}
			}
			if idx < 0 {
				return gmBag{}, errors.New("no such item pos " + strconv.Itoa(key))
			}
			p.Items = append(p.Items[:idx], p.Items[idx+1:]...)
		} else if typ > 0 {
			kept := p.Items[:0]
			n := 0
			for _, it := range p.Items {
				if it.Type == typ {
					n++
					continue
				}
				kept = append(kept, it)
			}
			if n == 0 {
				return gmBag{}, errors.New("no such item type " + strconv.Itoa(typ))
			}
			p.Items = kept
		} else {
			return gmBag{}, errors.New("need key (pos) or type")
		}
	case "suit":
		idx := -1
		for i, su := range p.Suits {
			if su.ItemNum == int32(key) {
				idx = i
				break
			}
		}
		if idx < 0 {
			return gmBag{}, errors.New("no such suit " + strconv.Itoa(key))
		}
		pos := p.Suits[idx].Pos
		if lu := p.wornLineup(pos, 0); lu > 0 {
			return gmBag{}, errors.New("suit is worn by lineup " + strconv.Itoa(lu))
		}
		p.Suits = append(p.Suits[:idx], p.Suits[idx+1:]...)
	case "book":
		idx := -1
		for i, b := range p.Books {
			if b.Pos == key {
				idx = i
				break
			}
		}
		if idx < 0 {
			return gmBag{}, errors.New("no such book pos " + strconv.Itoa(key))
		}
		if lu := p.wornLineup(key, 1); lu > 0 {
			return gmBag{}, errors.New("book is worn by lineup " + strconv.Itoa(lu))
		}
		p.Books = append(p.Books[:idx], p.Books[idx+1:]...)
	default:
		return gmBag{}, errors.New("bad kind (item / suit / book)")
	}
	if err := p.save(); err != nil {
		return gmBag{}, err
	}
	return gmBagOf(p), nil
}

// gmSuitSkill5Star 是只有 5 星装备才能拥有的炼神效果（闪避 / 突袭）。
var gmSuitSkill5Star = map[int32]bool{21611: true, 21621: true}

// gmSuitSkillsFor 返回某星级装备可用的全部炼神效果：4 星除闪避 / 突袭（20 种），5 星全部（22 种）。
func gmSuitSkillsFor(star int) []int32 {
	var out []int32
	for _, f := range lsFamilies {
		for _, b := range f.bases {
			if gmSuitSkill5Star[b] && star < 5 {
				continue
			}
			out = append(out, b)
		}
	}
	return out
}

// gmSuitSkillName 返回炼神效果的族名；不是炼神效果返回空。
func gmSuitSkillName(skill int32) string {
	if skill == 0 {
		return ""
	}
	if base, _ := lsInfo(skill); base != 0 {
		if t := gamedata.Talents[base]; t != nil {
			return t.Family
		}
	}
	return ""
}

// gmGiveAllSuitSkills 给选中装备赋予全部可用炼神效果：主效果 Skill = 第一个，其余全部进备用 Saved
// （4 星 20 种、5 星 22 种）。仅 4 星及以上装备可设；踢下线。
func (srv *Server) gmGiveAllSuitSkills(uid int32, itemnums []int32) (gmBag, error) {
	if srv.roles.Get(uid) == nil {
		return gmBag{}, errors.New("no such role")
	}
	if len(itemnums) == 0 {
		return gmBag{}, errors.New("empty itemnums")
	}
	if err := srv.kickWait(uid); err != nil {
		return gmBag{}, err
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return gmBag{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, num := range itemnums {
		su := p.suitByNum(num)
		if su == nil {
			return gmBag{}, errors.New("no such suit " + strconv.Itoa(int(num)))
		}
		if star := suitStar(su.Type); star < 4 {
			return gmBag{}, errors.New("suit " + strconv.Itoa(int(num)) + " is " + strconv.Itoa(star) + "-star (need 4+ star)")
		}
	}
	for _, num := range itemnums {
		su := p.suitByNum(num)
		all := gmSuitSkillsFor(suitStar(su.Type))
		su.Skill = all[0]
		su.Saved = append([]int32{}, all[1:]...)
	}
	p.refreshShownSkills()
	if err := p.save(); err != nil {
		return gmBag{}, err
	}
	return gmBagOf(p), nil
}

// wornLineup 检查背包位 pos 是否被某阵容穿戴；which = 0 装备 / 1 法术书。返回阵容号（0 = 未穿戴）。
func (p *Player) wornLineup(pos int, which int) int {
	if pos <= 0 {
		return 0
	}
	for _, lu := range p.Lineups {
		if lu == nil {
			continue
		}
		for _, sl := range lu.Slots {
			if sl == nil {
				continue
			}
			if which == 0 {
				for _, sp := range sl.Suit {
					if sp == pos {
						return lu.No
					}
				}
			} else {
				for _, bp := range sl.Book {
					if bp == pos {
						return lu.No
					}
				}
			}
		}
	}
	return 0
}

type catalogRow struct {
	Code string `json:"code"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Star int    `json:"star,omitempty"`
}

func gmCatalog() []catalogRow {
	var out []catalogRow
	for id, it := range gamedata.Items {
		if it != nil && it.Name != "" {
			out = append(out, catalogRow{"WP" + strconv.Itoa(id), it.Name, "道具", 0})
		}
	}
	for id, pi := range gamedata.Pets {
		if pi != nil && pi.Name != "" {
			out = append(out, catalogRow{"DZ" + strconv.Itoa(int(id)), pi.Name, "护法", pi.Star})
		}
	}
	for id, si := range gamedata.Suits {
		if si != nil && si.Name != "" {
			out = append(out, catalogRow{"ZB" + strconv.Itoa(id), si.Name, "装备", si.Star})
		}
	}
	for id, mi := range gamedata.Magics {
		if mi != nil && mi.Name != "" && id >= 11000 && id < 15000 {
			out = append(out, catalogRow{"FS" + strconv.Itoa(int(id)), mi.Name, "法术书", mi.Star})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// kickWait 把在线的 uid 踢下线并等会话结束（存档在会话结束时写完），最多 5 秒。
func (srv *Server) kickWait(uid int32) error {
	s := srv.sessionOf(uid)
	if s == nil {
		return nil
	}
	s.Close()
	for range 50 {
		if srv.sessionOf(uid) == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("role still online")
}

func (srv *Server) gmPlayer(uid int32) (map[string]any, error) {
	ro := srv.roles.Get(uid)
	if ro == nil {
		return nil, errors.New("no such role")
	}
	out := map[string]any{"role": ro, "online": srv.sessionOf(uid) != nil}
	if p, err := srv.players.Get(uid); err == nil {
		p.mu.Lock()
		p.role = ro
		out["pets"], out["suits"], out["books"], out["items"] = len(p.Pets), len(p.Suits), len(p.Books), len(p.Items)
		out["bag_limit"] = p.BagLimit
		// 当前出战阵容总战力（与 TASK_PTO.S_SEND_FIGHT_POINT 同一公式 teamCap）
		power := 0
		if lu := p.lineup(p.FightNo); lu != nil {
			var pets []*Pet
			for _, sl := range lu.Slots {
				if sl == nil {
					continue
				}
				if pet := p.petAtBag(sl.Pet); pet != nil {
					pets = append(pets, pet)
				}
			}
			power = p.teamCap(pets)
		}
		out["fight_point"] = power
		out["fno"] = p.FightNo
		powers := map[int]int{}
		for _, lu := range p.Lineups {
			if lu == nil {
				continue
			}
			var lpets []*Pet
			for _, sl := range lu.Slots {
				if sl == nil {
					continue
				}
				if pet := p.petAtBag(sl.Pet); pet != nil {
					lpets = append(lpets, pet)
				}
			}
			powers[lu.No] = p.teamCap(lpets)
		}
		out["lineup_powers"] = powers
		var detail []map[string]any
		var sumBase, sumCap int64
		if lu := p.lineup(p.FightNo); lu != nil {
			for i, sl := range lu.Slots {
				if sl == nil {
					continue
				}
				pet := p.petAtBag(sl.Pet)
				if pet == nil {
					continue
				}
				att, hp := p.petStats(pet)
				cap := int(p.petCap(pet))
				mr, tr := 1.0, 1.0
				if pi := gamedata.Pets[pet.Type]; pi != nil {
					mr, tr = floor2(p.capMagicRate(pet, pi)), floor2(p.capTalentRate(pet, pi))
				}
				sumBase += int64(att*4 + hp)
				sumCap += int64(cap)
				detail = append(detail, map[string]any{"pos": i + 1, "bag_pos": pet.BagPos, "type": pet.Type,
					"grade": pet.Grade, "att": att, "hp": hp, "att4hp": att*4 + hp, "mRate": mr, "tRate": tr, "cap": cap})
			}
		}
		out["lineup_detail"] = detail
		out["sum_att4hp"] = sumBase
		out["sum_cap"] = sumCap
		p.mu.Unlock()
	}
	return out, nil
}

// gmSetRole 改角色数值。可改的键见 gmRoleKeys；数值不能为负，等级不超过满级。
func (srv *Server) gmSetRole(uid int32, set map[string]int64) (*Role, error) {
	if srv.roles.Get(uid) == nil {
		return nil, errors.New("no such role")
	}
	for k, v := range set {
		if _, ok := gmRoleKeys[k]; !ok {
			return nil, errors.New("unknown field " + k)
		}
		if v < 0 || v > 2_000_000_000 {
			return nil, errors.New("bad value for " + k)
		}
		if k == "stop_upgrade" && v > 1 || k == "xgrade" && v > int64(gamedata.MaxXiuweiGrade) || k == "xiuwei" && v >= gamedata.XiuweiNeed(0) {
			return nil, errors.New("bad value for " + k)
		}
	}
	if err := srv.kickWait(uid); err != nil {
		return nil, err
	}
	return srv.roles.Update(uid, func(ro *Role) bool {
		for k, v := range set {
			gmRoleKeys[k](ro, v)
		}
		return true
	})
}

var gmRoleKeys = map[string]func(ro *Role, v int64){
	"stop_upgrade": func(ro *Role, v int64) { ro.StopUpgrade = int8(v) },
	"xgrade":       func(ro *Role, v int64) { ro.XGrade = int(v) },
	"xiuwei":       func(ro *Role, v int64) { ro.Xiuwei = v },
	"friend_point": func(ro *Role, v int64) { ro.FriendPoint = int32(v) },
	"gold":         func(ro *Role, v int64) { ro.Gold = int32(v) },
	"cash":         func(ro *Role, v int64) { ro.Cash = int32(v) },
	"soulexp":      func(ro *Role, v int64) { ro.SoulExp = int32(v) },
	"friendpoint":  func(ro *Role, v int64) { ro.FriendPoint = int32(v) },
	"grade":        func(ro *Role, v int64) { ro.Grade = int(min(max(v, 1), int64(gamedata.MaxRoleGrade))) },
	"exp":          func(ro *Role, v int64) { ro.Exp = int32(v) },
	"vip":          func(ro *Role, v int64) { ro.VIP = int(min(v, 15)) },
	"tpoint":       func(ro *Role, v int64) { ro.TPoint = int(min(v, 9999)) },
	"hpoint":       func(ro *Role, v int64) { ro.HPoint = int(min(v, 9999)) },
}

// gmPetRow 是 GM 接口返回的一行护法（名字 / 星级取 gamedata.Pets）。
type gmPetRow struct {
	NpcNum   int32  `json:"npcnum"`
	Type     int32  `json:"type"`
	Name     string `json:"name"`
	Star     int    `json:"star"`
	Grade    int    `json:"grade"`
	AttAdd   int    `json:"attadd"`
	HPAdd    int    `json:"hpadd"`
	BagPos   int    `json:"bag_pos"`
	Locked   bool   `json:"locked"`
	Combine  int    `json:"combine"`  // 已结缘数
	Together int    `json:"together"` // 已同修数
	Extend   int    `json:"extend"`   // 缘分总数（初始 + 可结缘）
}

// petAllRelates 是护法的全部人物缘：初始缘分（gamedata.Pets 的 relate）+ 可结缘（ExtendRelates），去重。
func petAllRelates(typ int32) []int32 {
	var out []int32
	seen := map[int32]bool{}
	if pi := gamedata.Pets[typ]; pi != nil {
		for _, rid := range pi.Relate {
			if !seen[rid] {
				seen[rid] = true
				out = append(out, rid)
			}
		}
	}
	for _, rid := range gamedata.ExtendRelates[typ] {
		if !seen[rid] {
			seen[rid] = true
			out = append(out, rid)
		}
	}
	return out
}

func gmPetRowOf(pet *Pet) gmPetRow {
	row := gmPetRow{NpcNum: pet.NpcNum, Type: pet.Type, Grade: pet.Grade, AttAdd: pet.AttAdd, HPAdd: pet.HPAdd,
		BagPos: pet.BagPos, Locked: pet.Locked, Combine: len(pet.Combine), Together: len(pet.Together),
		Extend: len(petAllRelates(pet.Type))}
	if pi := gamedata.Pets[pet.Type]; pi != nil {
		row.Name, row.Star = pi.Name, pi.Star
	}
	return row
}

// gmRelateBanned 判断护法是否禁止一键开满结缘 + 同修：助人海星没有人物缘（pet.csv relate 为空），
// “开满”只会把它现有的缘清空，因此排除。
func gmRelateBanned(typ int32) bool {
	pi := gamedata.Pets[typ]
	return pi != nil && pi.Name == "助人海星"
}

// gmPetRelateAll 给选中护法一键开满结缘 + 同修（仅 5 星护法，助人海星除外）：
// 结缘 = 该护法的全部人物缘（初始缘分 + 可结缘）；同修 = 其中可同修的（TogetherNPet 非空）。踢下线。
func (srv *Server) gmPetRelateAll(uid int32, petnums []int32) ([]gmPetRow, error) {
	if srv.roles.Get(uid) == nil {
		return nil, errors.New("no such role")
	}
	if len(petnums) == 0 {
		return nil, errors.New("empty petnums")
	}
	if err := srv.kickWait(uid); err != nil {
		return nil, err
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, num := range petnums {
		pet := p.petByNpc(num)
		if pet == nil {
			return nil, errors.New("no such pet " + strconv.Itoa(int(num)))
		}
		if petStar(pet.Type) != 5 {
			return nil, errors.New("pet " + strconv.Itoa(int(num)) + " is not 5-star")
		}
		if gmRelateBanned(pet.Type) {
			return nil, errors.New("护法 " + strconv.Itoa(int(num)) + "（助人海星）没有人物缘，不能一键开满")
		}
	}
	for _, num := range petnums {
		pet := p.petByNpc(num)
		all := petAllRelates(pet.Type)
		pet.Combine = append([]int32{}, all...)
		pet.Together = pet.Together[:0]
		for _, rid := range all {
			if r := gamedata.Relates[rid]; r != nil && len(r.TogetherNPet) > 0 {
				pet.Together = append(pet.Together, rid)
			}
		}
	}
	if err := p.save(); err != nil {
		return nil, err
	}
	out := make([]gmPetRow, 0, len(p.Pets))
	for _, pet := range p.Pets {
		out = append(out, gmPetRowOf(pet))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NpcNum < out[j].NpcNum })
	return out, nil
}

// gmPets 列出 uid 的护法（调用方不需持锁，内部加 p.mu）。
func (srv *Server) gmPets(uid int32) ([]gmPetRow, error) {
	p, err := srv.players.Get(uid)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]gmPetRow, 0, len(p.Pets))
	for _, pet := range p.Pets {
		out = append(out, gmPetRowOf(pet))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NpcNum < out[j].NpcNum })
	return out, nil
}

const gmPetPointMax = 9999

// gmSetPetPoint 改护法的培养次数（attadd / hpadd，nil 表示不改）。与 gmSetRole 一样先踢下线写完存档再改。
// 返回改后的那一行护法。
func (srv *Server) gmSetPetPoint(uid, npcnum int32, attadd, hpadd *int) (*gmPetRow, error) {
	if srv.roles.Get(uid) == nil {
		return nil, errors.New("no such role")
	}
	for _, v := range []*int{attadd, hpadd} {
		if v != nil && (*v < 0 || *v > gmPetPointMax) {
			return nil, errors.New("bad value (0..9999)")
		}
	}
	if err := srv.kickWait(uid); err != nil {
		return nil, err
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pet := p.petByNpc(npcnum)
	if pet == nil {
		return nil, errors.New("no such pet")
	}
	if attadd != nil {
		pet.AttAdd = *attadd
	}
	if hpadd != nil {
		pet.HPAdd = *hpadd
	}
	if err := p.save(); err != nil {
		return nil, err
	}
	row := gmPetRowOf(pet)
	return &row, nil
}

// gmAccountRow 是 GET /accounts 返回的一行账号。
type gmAccountRow struct {
	UID     int32     `json:"uid"`
	URS     string    `json:"urs"`
	Created time.Time `json:"created"`
	Device  string    `json:"device"`
	HasRole bool      `json:"has_role"`
	Online  bool      `json:"online"`
}

// gmAccounts 列出全部账号（按 uid 排序），带上是否有角色 / 是否在线。
func (srv *Server) gmAccounts() []gmAccountRow {
	list := srv.accounts.List()
	out := make([]gmAccountRow, 0, len(list))
	for _, acc := range list {
		out = append(out, gmAccountRow{UID: acc.UID, URS: acc.URS, Created: acc.Created, Device: acc.Device,
			HasRole: srv.roles.Get(acc.UID) != nil, Online: srv.sessionOf(acc.UID) != nil})
	}
	return out
}

// gmDeleteAccount 删账号：在线的先踢下线（等存档写完），再依次删账号 → 角色 → 玩家存档。
// confirm 必须与 urs 完全一致（防误删）。返回被删的 uid。
func (srv *Server) gmDeleteAccount(urs, confirm string) (int32, error) {
	return srv.gmDeleteAccountData(urs, confirm)
}

func (srv *Server) gmHandlers(mux *http.ServeMux) {
	// 宝箱掉落实时调控（方案 A：内存生效，重启恢复代码默认值）
	mux.HandleFunc("GET /box", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, srv.boxSnapshot())
	})
	mux.HandleFunc("POST /box_set", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Box   int `json:"box"`
			Drops []struct {
				W    int    `json:"w"`
				Code string `json:"code"`
				Cnt  int32  `json:"cnt"`
			} `json:"drops"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		drops := make([]boxDrop, 0, len(req.Drops))
		for _, d := range req.Drops {
			drops = append(drops, boxDrop{w: d.W, code: d.Code, cnt: d.Cnt})
		}
		if err := srv.boxSet(req.Box, drops); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "box": req.Box, "count": len(drops)})
	})
	mux.HandleFunc("POST /box_reset", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Box int `json:"box"` // 0 = 全部宝箱恢复预设值
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := srv.boxReset(req.Box); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "box": req.Box})
	})
	mux.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, gmCatalog())
	})
	mux.HandleFunc("GET /player", func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		out, err := srv.gmPlayer(int32(uid))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /role", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32            `json:"uid"`
			Set map[string]int64 `json:"set"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		ro, err := srv.gmSetRole(req.UID, req.Set)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, ro)
	})
	mux.HandleFunc("POST /kick", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32 `json:"uid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := srv.kickWait(req.UID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "kicked"})
	})
	mux.HandleFunc("GET /pets", func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		pets, err := srv.gmPets(int32(uid))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, pets)
	})
	mux.HandleFunc("POST /pet_point", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID    int32 `json:"uid"`
			NpcNum int32 `json:"npcnum"`
			AttAdd *int  `json:"attadd"`
			HPAdd  *int  `json:"hpadd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		row, err := srv.gmSetPetPoint(req.UID, req.NpcNum, req.AttAdd, req.HPAdd)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, row)
	})
	mux.HandleFunc("POST /pet_relate", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID     int32   `json:"uid"`
			PetNums []int32 `json:"petnums"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmPetRelateAll(req.UID, req.PetNums)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /account", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URS  string `json:"urs"`
			Pwd  string `json:"pwd"`
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		acc, err := srv.accounts.Register(req.URS, req.Pwd, req.Code)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"uid": acc.UID, "urs": acc.URS})
	})
	mux.HandleFunc("GET /accounts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, srv.gmAccounts())
	})
	mux.HandleFunc("POST /account_pwd", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URS string `json:"urs"`
			Pwd string `json:"pwd"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		acc, err := srv.accounts.SetPassword(req.URS, req.Pwd)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"uid": acc.UID})
	})
	mux.HandleFunc("POST /account_delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URS     string `json:"urs"`
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		uid, err := srv.gmDeleteAccount(req.URS, req.Confirm)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": "deleted", "uid": uid})
	})
	mux.HandleFunc("GET /dongfus", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, srv.gmDongfus())
	})
	mux.HandleFunc("POST /dongfu_lv", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DongfuID int32 `json:"dongfu_id"`
			Lv       int32 `json:"lv"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmSetDongfuLv(req.DongfuID, req.Lv)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /suit_all", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID      int32   `json:"uid"`
			ItemNums []int32 `json:"itemnums"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmGiveAllSuitSkills(req.UID, req.ItemNums)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /bag", func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		out, err := srv.gmGetBag(int32(uid))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /bag_add", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID  int32  `json:"uid"`
			Kind string `json:"kind"`
			ID   int    `json:"id"`
			N    int32  `json:"n"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmAddBag(req.UID, req.Kind, req.ID, req.N)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /bag_del", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID  int32  `json:"uid"`
			Kind string `json:"kind"`
			Key  int    `json:"key"`
			Type int    `json:"type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmDelBag(req.UID, req.Kind, req.Key, req.Type)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /progress", func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		out, err := srv.gmGetProgress(int32(uid))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /progress_set", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID  int32 `json:"uid"`
			Typ  int   `json:"typ"`
			Task int   `json:"task"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmSetProgress(req.UID, req.Typ, req.Task)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /xiuwei", func(w http.ResponseWriter, r *http.Request) {
		uid, _ := strconv.Atoi(r.URL.Query().Get("uid"))
		out, err := srv.gmGetXiuwei(int32(uid))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /god_soul", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32 `json:"uid"`
			N   int   `json:"n"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmAddGodSoul(req.UID, req.N)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /territory_score", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32 `json:"uid"`
			N   int   `json:"n"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmAddTerritoryScore(req.UID, req.N)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /xiuwei_set", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID int32          `json:"uid"`
			Set map[string]int `json:"set"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmSetXiuwei(req.UID, req.Set)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /dongfu_build", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DongfuID int32          `json:"dongfu_id"`
			Set      map[string]int `json:"set"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		out, err := srv.gmSetDongfuBuild(req.DongfuID, req.Set)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
	})
}

// gmAddGodSoul 给角色直接加洞府神魂（原先只能发道具，洞里看不到）。
func (srv *Server) gmAddGodSoul(uid int32, n int) (map[string]any, error) {
	if uid == 0 || n == 0 {
		return nil, errors.New("bad uid/n")
	}
	p, err := srv.players.Get(uid)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.df().GodSoul += int32(n)
	if p.df().GodSoul < 0 {
		p.df().GodSoul = 0
	}
	if err := p.save(); err != nil {
		return nil, err
	}
	return map[string]any{"uid": uid, "god_soul": p.df().GodSoul}, nil
}

// gmAddTerritoryScore 给正在领土战场里的角色加胜利点（战场是内存态，只在战斗中有效）。
func (srv *Server) gmAddTerritoryScore(uid int32, n int) (map[string]any, error) {
	terrMgr.mu.Lock()
	b := terrMgr.byUID[uid]
	terrMgr.mu.Unlock()
	if b == nil {
		return nil, errors.New("角色不在领土战场")
	}
	b.mu.Lock()
	pl := b.players[uid]
	if pl == nil {
		b.mu.Unlock()
		return nil, errors.New("角色不在领土战场")
	}
	pl.Score += n
	score := pl.Score
	b.mu.Unlock()
	return map[string]any{"uid": uid, "score": score}, nil
}
