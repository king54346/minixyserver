package xymsrv

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// enterWorld 注册一个新号、建角并读掉登录推送，返回已进游戏的连接。
func enterWorld(t *testing.T, addr, urs string) *client {
	t.Helper()
	c := dial(t, addr)
	c.sendRaw(xym.CRegister, registerBody(urs, "pw", "abcd"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.recv() // S_CREATE
	c.sendRaw(xym.CCreate, cat([]byte{1, 0}, str(urs)))
	c.expectExt("LOGIN_PTO.S_NEW_USER")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c.collect()
	return c
}

// collect 读到连接安静 200ms 为止，返回收到的扩展协议（按到达顺序）。解码失败直接判错。
func (c *client) collect() []*xym.Spec {
	c.t.Helper()
	var out []*xym.Spec
	for {
		c.conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		main, body, err := xym.ReadS2C(c.r)
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return out
		}
		if err != nil {
			c.t.Fatalf("collect: %v", err)
		}
		if main == xym.SMsg {
			continue // 提示框（例如回放官方号的布阵，引用的卡本服没有）
		}
		if main != xym.MainExt {
			c.t.Fatalf("collect: unexpected main %d %q", main, body)
		}
		_, spec, _, err := xym.DecodeExt(xym.S2C, body)
		if err != nil {
			c.t.Fatalf("collect: %v", err)
		}
		out = append(out, spec)
	}
}

func specName(s *xym.Spec) string { return s.Module + "." + s.Name }

type capRow struct {
	Dir  string `json:"dir"`
	Main int    `json:"main"`
	PID  int    `json:"pid"`
	Mod  string `json:"mod"`
	Name string `json:"name"`
	Raw  string `json:"raw"`
	Conn int    `json:"conn"`
}

// capturedReplies 把抓包切成「一串 C2S + 紧随的一串 S2C」：请求 PID -> 官方回过的消息名集合
// （优先取单条请求的批次），以及一份原始请求 body。回包只认同一条连接上的；请求发出后连接就断了、
// 没录到回包的批次不计（例如 C_ON_TJUP），这类请求的回包未知，不在 replies 里。
func capturedReplies(t *testing.T, path string) (map[int]map[string]bool, map[int][]byte) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []capRow
	for _, line := range bytes.Split(data, []byte("\n")) {
		var r capRow
		if json.Unmarshal(line, &r) == nil && r.PID != 0 && (r.Main == 1 || r.Main == 254) {
			rows = append(rows, r)
		}
	}
	single, multi, reqs := map[int]map[string]bool{}, map[int]map[string]bool{}, map[int][]byte{}
	add := func(m map[int]map[string]bool, pid int, rs []capRow) {
		if m[pid] == nil {
			m[pid] = map[string]bool{}
		}
		for _, r := range rs {
			m[pid][r.Mod+"."+r.Name] = true
		}
	}
	for i := 0; i < len(rows); {
		j := i
		for j < len(rows) && rows[j].Dir == "C2S" {
			j++
		}
		k := j
		for k < len(rows) && rows[k].Dir == "S2C" && (j == i || rows[k].Conn == rows[j-1].Conn) {
			k++
		}
		for _, r := range rows[i:j] {
			if reqs[r.PID] == nil && r.Raw != "" {
				reqs[r.PID], _ = hex.DecodeString(r.Raw)
			}
			if k == j && (k == len(rows) || rows[k].Conn != r.Conn) {
				continue // 连接断开，回包没录到
			}
			if j-i == 1 {
				add(single, r.PID, rows[j:k])
			} else {
				add(multi, r.PID, rows[j:k])
			}
		}
		if k == i {
			k++
		}
		i = k
	}
	// 单发的批次能确定配对；只在连发里出现过的，退而用整批回包的并集
	for pid, m := range multi {
		if single[pid] == nil {
			single[pid] = m
		}
	}
	return single, reqs
}

// TestReplayCaptured：把官方抓包里的请求原样发给本服，回包的每条消息都必须在官方对同一请求的
// 回包里出现过（官方可能多回推送，所以是子集关系）；官方从不回包的，本服也不回。
func TestReplayCaptured(t *testing.T) {
	dir := os.Getenv("XYM_CAPTURES")
	if dir == "" {
		t.Skip("XYM_CAPTURES not set")
	}
	replies, reqs := capturedReplies(t, filepath.Join(dir, "session-01.jsonl"))
	srv, addr, _ := startServerSrv(t, DefaultConfig())
	c := enterWorld(t, addr, "replay")
	unlockAll(t, srv, "replay") // 抓包是 50 级老号，关卡请求要求已解锁
	primeDangyao(t, srv, "replay")
	primeDailies(t, srv, "replay")
	// 抓包的号在洞府里（「福利多多」的成员）
	acc, _ := srv.accounts.Login("replay", "pw", "")
	srv.dongfu.do(func() bool {
		srv.dongfu.List[1] = &Dongfu{ID: 1, Name: "福利多多", Slogan: "工具", Icon: 1, Lv: 1,
			Members: []*DFMember{{UID: acc.UID, Position: dfMember}}}
		return true
	})

	var pids []int
	for pid := range Handled() {
		if reqs[pid] != nil {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	if len(pids) < 20 {
		t.Fatalf("only %d handled requests found in capture", len(pids))
	}
	for _, pid := range pids {
		req := xym.Lookup(xym.C2S, pid)
		if replyNotOfficial[specName(req)] {
			t.Logf("%-40s -> (captured reply is a local placeholder, skipped)", specName(req))
			continue
		}
		if replies[pid] == nil { // 官方回包没录到（连接中途断开），无从比对
			t.Logf("%-40s -> (official reply not captured, skipped)", specName(req))
			continue
		}
		main := byte(xym.MainExt)
		if len(reqs[pid]) > 0xff {
			main = xym.MainExtLarge // 客户端 net.send_prot：超过 255 字节改用 u16 长度的帧
		}
		c.sendRaw(main, reqs[pid])
		got := c.collect()
		var names []string
		for _, sp := range got {
			names = append(names, specName(sp))
			if !replies[pid][specName(sp)] && !replayTimeDependent[specName(sp)] {
				t.Errorf("%s: replied %s, official never did (official: %v)", specName(req), specName(sp), keys(replies[pid]))
			}
		}
		if len(got) == 0 && len(replies[pid]) > 0 && req.Lock != "" {
			t.Errorf("%s: no reply, client waits on LOCK %s", specName(req), req.Lock)
		}
		t.Logf("%-40s -> %v", specName(req), names)
	}
}

// replayTimeDependent：和本地存档 / 当前时间有关、官方抓包里不一定出现的回包，不算「官方之外」：
// S_ASK_BOX 是服务端发起的确认框（如仙灵商铺缺材料时问是否补元宝兑换）；星战界面按当前时段（报名中 / 比赛中 /
// 已结束）回 S_OPEN_STARWAR / S_OPEN_FIGHT / S_GAME_OFF / S_SUPER_GAME_OFF，回放用的是墙上时钟。
var replayTimeDependent = map[string]bool{
	"ETC_PTO.S_ASK_BOX":            true,
	"STARWAR_PTO.S_OPEN_FIGHT":     true,
	"STARWAR_PTO.S_GAME_OFF":       true,
	"STARWAR_PTO.S_SUPER_GAME_OFF": true,
}

// replyNotOfficial：抓包里这些请求的「回包」不是官方服的——当时客户端热更新在本地拦下，弹了
// 「九幽试炼暂未开放…本地服务没有保留」的 S_RET_MSG（session-01 t=516），不能拿来比对。
var replyNotOfficial = map[string]bool{"JIUYOU_PTO.C_OPEN_MAIN": true}

// unlockAll 把账号的普通、精英关卡全部记为通关。
func unlockAll(t *testing.T, srv *Server, urs string) {
	t.Helper()
	acc, err := srv.accounts.Login(urs, "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := srv.players.Get(acc.UID)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for typ := range p.Prog {
		pr := p.Prog[typ]
		for task, l := range gamedata.TaskGates {
			pr.Stars[task] = 1
			pr.Last = max(pr.Last, task)
			for _, gid := range l {
				pr.Gates[gid] = 1
			}
		}
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestLockCoverage：每条带 LOCK 的请求，要么有 handler 且回包里带解锁消息，要么有兜底回包。
func TestLockCoverage(t *testing.T) {
	n := 0
	for _, c := range xym.All(xym.C2S) {
		if c.Lock == "" {
			continue
		}
		n++
		if lockReply()[c.PID] == nil {
			t.Errorf("%s (LOCK %s): no unlock reply", specName(c), c.Lock)
		}
	}
	if n != 37 {
		t.Errorf("LOCK requests = %d, want 37", n)
	}
}

// TestLockStub：未实现的带 LOCK 请求收到全零的解锁回包，连接不断。
func TestLockStub(t *testing.T) {
	addr, _ := startServer(t, DefaultConfig())
	c := enterWorld(t, addr, "stub")
	var req *xym.Spec // 任取一条还没有 handler、带 LOCK 的请求（按协议号最小的）
	for _, c := range xym.All(xym.C2S) {
		if c.Lock != "" && handlers[c.PID] == nil && lockReply()[c.PID] != nil && (req == nil || c.PID < req.PID) {
			req = c
		}
	}
	if req == nil {
		t.Skip("every LOCK request has a handler")
	}
	c.sendExt(specName(req), nil)
	got := c.collect()
	if len(got) != 1 || got[0] != lockReply()[req.PID] || got[0].Lock != req.Lock {
		t.Fatalf("stub reply %v", got)
	}
	// 新号的卡牌与阵容
	c.sendExt("PET_PTO.C_APPLY_PET_LIST", nil)
	pets := c.expectExt("PET_PTO.S_UPDATE_PET")["pets"].([]xym.Msg)
	if len(pets) != len(starterPets) || pets[0]["fight_pos"] != int64(1) || pets[0]["hp"].(int64) <= 0 {
		t.Fatalf("starter pets %v", pets)
	}
	c.expectExt("PET_PTO.S_PET_LIST")
	c.sendExt("TASK_PTO.C_OPEN_MAP_2", xym.Msg{"mapid": 0, "type": 0})
	if m := c.expectExt("TASK_PTO.S_SEND_MAP_2"); m["curr_task"] != int64(0) || m["mapid"] != int64(1) || m["map_task_fin"] != int64(0) {
		t.Fatalf("map %v", m)
	}
}

// primeDangyao 照抓包里的老号准备荡妖：元宝 / 体力足够，功德 1180、累计消耗 4905（3 级），功德商店全部商品上架。
func primeDangyao(t *testing.T, srv *Server, urs string) {
	t.Helper()
	acc, err := srv.accounts.Login(urs, "pw", "")
	if err != nil {
		t.Fatal(err)
	}
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Gold, ro.TPoint = 1000, 60; return true })
	p, _ := srv.players.Get(acc.UID)
	p.mu.Lock()
	defer p.mu.Unlock()
	d := p.dy()
	d.Gongde, d.Spent = 1180, 4905
	all := func(d *DangyaoState, _ bool, _ *rand.Rand) {
		d.Shop = map[int][]*dyShopSlot{}
		for _, it := range gamedata.Dangyao.Shop {
			d.Shop[it.Type] = append(d.Shop[it.Type], &dyShopSlot{Pos: it.Pos, Left: it.Left})
		}
	}
	all(d, false, nil)
	old := dyRollShop
	dyRollShop = all // 重置妖窟后照样全部上架，回放里的购买请求才买得到
	t.Cleanup(func() { dyRollShop = old })
}

// primeDailies 照抓包里的老号准备日常玩法：今天已经种过摇钱树，宝卷照 session-01 兑换前的数量。
func primeDailies(t *testing.T, srv *Server, urs string) {
	t.Helper()
	acc, _ := srv.accounts.Login(urs, "pw", "")
	p, _ := srv.players.Get(acc.UID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tree().PlantOn = today()
	p.duobao().Tickets = [dbTicketMax + 1]int{3, 6, 3, 1}
	// 抓包里升境界 / 升神通的两张卡（npcnum 照官方号），魂值、银两、人物等级足够
	p.Pets = append(p.Pets,
		&Pet{NpcNum: 10427, Type: 25032, Grade: 50, BagPos: 370, SoulGrade: 7, TalGrade: [3]int{1, 7, 4}, TLPoint: 10},
		&Pet{NpcNum: 10323, Type: 45033, Grade: 50, BagPos: 315, SoulGrade: 5, TalGrade: [3]int{1, 7, 4}, TLPoint: 10})
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.Grade, ro.SoulExp, ro.Cash = 50, 5000, 10000000; return true })
}
