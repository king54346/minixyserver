package xymsrv

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"turnrpg/internal/store"
	"turnrpg/internal/xym"
)

func startServer(t *testing.T, cfg Config) (addr, dir string) {
	_, addr, dir = startServerSrv(t, cfg)
	return addr, dir
}

func startServerSrv(t *testing.T, cfg Config) (srv *Server, addr, dir string) {
	t.Helper()
	dir = t.TempDir()
	st := openTestStore(t, dir)
	accounts, err := OpenAccounts(st)
	if err != nil {
		t.Fatal(err)
	}
	roles, err := OpenRoles(st)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv = NewServer(cfg, accounts, roles, OpenPlayers(st))
	go srv.Serve(ln)
	t.Cleanup(srv.Shutdown)
	return srv, ln.Addr().String(), dir
}

// openTestStore 在 dir 下开一个 SQLite 存档（测试结束时关闭）。
func openTestStore(t *testing.T, dir string) store.Store {
	t.Helper()
	st, err := store.Open("sqlite", dir, filepath.Join(dir, "xym.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

type client struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &client{t: t, conn: conn, r: bufio.NewReader(conn)}
	if main, body := c.recv(); main != xym.SSeed || !bytes.Equal(body, []byte{0, 0, 0, 0}) {
		t.Fatalf("first frame main=%d body=%x, want S_SEED(0)", main, body)
	}
	return c
}

func (c *client) sendRaw(main byte, body []byte) {
	c.t.Helper()
	if _, err := c.conn.Write(xym.AppendC2S(nil, main, body)); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) sendExt(name string, m xym.Msg) {
	c.t.Helper()
	c.sendRaw(xym.MainExt, xym.ByName(name).MustEncode(m))
}

func (c *client) recv() (byte, []byte) {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	main, body, err := xym.ReadS2C(c.r)
	if err != nil {
		c.t.Fatalf("recv: %v", err)
	}
	return main, body
}

// expectExt 读下一帧，要求是 name 对应的扩展协议，返回字段。
func (c *client) expectExt(name string) xym.Msg {
	c.t.Helper()
	main, body := c.recv()
	if main != xym.MainExt {
		c.t.Fatalf("want %s, got main=%d body=%q", name, main, body)
	}
	_, spec, m, err := xym.DecodeExt(xym.S2C, body)
	if err != nil || spec != xym.ByName(name) {
		c.t.Fatalf("want %s, got %v err=%v", name, spec, err)
	}
	return m
}

// expectExtSkip 同 expectExt，但先跳过 skip 列出的消息（例如战斗里偶发的阵亡列表）。
func (c *client) expectExtSkip(name string, skip ...string) xym.Msg {
	c.t.Helper()
	for {
		main, body := c.recv()
		if main != xym.MainExt {
			c.t.Fatalf("want %s, got main=%d body=%q", name, main, body)
		}
		_, spec, m, err := xym.DecodeExt(xym.S2C, body)
		if err != nil {
			c.t.Fatal(err)
		}
		if spec == xym.ByName(name) {
			return m
		}
		ok := false
		for _, n := range skip {
			ok = ok || spec == xym.ByName(n)
		}
		if !ok {
			c.t.Fatalf("want %s, got %v", name, spec)
		}
	}
}

// skipExt 读到第一帧非扩展协议为止（跳过登录后推送、未读完的回包）。
func (c *client) skipExt() (byte, []byte) {
	c.t.Helper()
	for {
		if main, body := c.recv(); main != xym.MainExt {
			return main, body
		}
	}
}

func (c *client) expectFail(code uint32) {
	c.t.Helper()
	main, body := c.recv()
	for main == xym.SMsg {
		main, body = c.recv()
	}
	if main != xym.SLoginFail || binary.LittleEndian.Uint32(body) != code {
		c.t.Fatalf("want S_LOGIN_FAIL %d, got main=%d body=%x", code, main, body)
	}
}

func (c *client) expectClosed() {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := xym.ReadS2C(c.r); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				c.t.Fatal("connection still open")
			}
			return
		}
	}
}

func str(s string) []byte { return xym.VarStr(s) }

var ver = []byte{1, 0, 5, 0, 0x61, 0x01} // 1.5.353，与 offline_probe.versions 相同

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func loginBody(urs, pwd string) []byte {
	return cat(ver, str(urs), str(pwd), str("gen:test-device"))
}

func registerBody(urs, pwd, code string) []byte {
	return cat(loginBody(urs, pwd), str(code))
}

func sidBody(uid int64, sid string) []byte {
	return cat(ver, binary.LittleEndian.AppendUint32(nil, uint32(uid)), str(sid))
}

func TestSeedFrame(t *testing.T) {
	addr, _ := startServer(t, DefaultConfig())
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	buf := make([]byte, 7)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Read(buf); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(buf) != "03040000000000" { // 与官方服首帧逐字节相同
		t.Fatalf("first frame %x", buf)
	}
}

func TestLoginFlow(t *testing.T) {
	addr, dir := startServer(t, DefaultConfig())

	// 没有账号 → 102；注册 → S_SEND_SID + S_CREATE
	c := dial(t, addr)
	c.sendRaw(xym.CLogin, loginBody("alice", "pw1"))
	c.expectFail(xym.FailNoAccount)
	c.sendRaw(xym.CRegister, registerBody("alice", "pw1", "abcd"))
	sid := c.expectExt("LOGIN_PTO.S_SEND_SID")
	if main, _ := c.recv(); main != xym.SCreate {
		t.Fatalf("want S_CREATE, got main=%d", main)
	}
	// 建角前的扩展协议直接丢掉，不应有回包
	c.sendRaw(xym.CCreate, cat([]byte{1, 0}, str("有 空格")))
	if main, _ := c.recv(); main != xym.SMsg {
		t.Fatalf("bad name: want S_MSG, got %d", main)
	}
	c.sendRaw(xym.CCreate, cat([]byte{2, 0}, str("悟空")))
	c.expectExt("LOGIN_PTO.S_NEW_USER")
	suc := c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	if suc["name"] != "悟空" || suc["uid"] != sid["uid"] || suc["icon"] != int64(2) || suc["urs"] != "alice" {
		t.Fatalf("S_LOGIN_SUC %v", suc)
	}
	// 登录后上报的设备信息、未实现的协议都不回包，连接保持
	c.sendExt("LOGIN_PTO.C_MACHINE_INFO", xym.Msg{"isfirst": 1, "device": "x"})
	c.sendExt("LOGIN_PTO.C_AFTER_LOGIN", nil)
	c.sendExt("PET_PTO.C_APPLY_PET_LIST", nil)

	// 断线重连（帧 6）：新连接顶掉旧连接，不再走建角
	c2 := dial(t, addr)
	c2.sendRaw(xym.CSidLogin, sidBody(sid["uid"].(int64), sid["sid"].(string)))
	if got := c2.expectExt("LOGIN_PTO.S_SEND_SID"); got["sid"] != sid["sid"] {
		t.Fatalf("sid changed on reconnect: %v", got)
	}
	c2.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	if main, _ := c.skipExt(); main != xym.SMsg {
		t.Fatalf("kicked session: want S_MSG, got %d", main)
	}
	c.expectClosed()

	// 错误 sid → 1
	c3 := dial(t, addr)
	c3.sendRaw(xym.CSidLogin, sidBody(sid["uid"].(int64), "0123456789abcdef0123456789abcdef"))
	c3.expectFail(xym.FailBadPassword)

	// 密码错 → 1；不合法 → 101；注册重名 → 100
	c4 := dial(t, addr)
	c4.sendRaw(xym.CLogin, loginBody("alice", "wrong"))
	c4.expectFail(xym.FailBadPassword)
	c4.sendRaw(xym.CLogin, loginBody("alice", ""))
	c4.expectFail(xym.FailInvalid)
	c4.sendRaw(xym.CRegister, registerBody("ALICE", "pw2", "abcd"))
	c4.expectFail(xym.FailNameTaken)
	c4.sendRaw(xym.CRegister, loginBody("bob", "pw")) // 不带安全码
	c4.expectFail(xym.FailRefused)
	c4.sendRaw(xym.CRegister, cat(ver, str("bob"), str("pw"))) // 老包：连设备号都没有
	c4.expectFail(xym.FailRefused)

	// 找回：安全码错 106 → 核对 104 → 重设 105 → 新密码能登，旧 sid 失效
	c5 := dial(t, addr)
	c5.sendRaw(xym.CRecover, cat(ver, str("alice"), str("zzzz"), str(""), str("")))
	c5.expectFail(FailRecoverBadCode)
	c5.sendRaw(xym.CRecover, cat(ver, str("nobody"), str("abcd"), str(""), str("")))
	c5.expectFail(xym.FailNoAccount)
	c5.sendRaw(xym.CRecover, cat(ver, str("alice"), str("ABCD"), str(""), str("")))
	c5.expectFail(xym.RecoverCodeOK)
	c5.sendRaw(xym.CRecover, cat(ver, str("alice"), str("abcd"), str("newpw"), str("")))
	c5.expectFail(xym.RecoverDone)

	c6 := dial(t, addr)
	c6.sendRaw(xym.CLogin, loginBody("alice", "newpw"))
	sid2 := c6.expectExt("LOGIN_PTO.S_SEND_SID")
	if sid2["sid"] == sid["sid"] {
		t.Fatal("password login should issue a new sid")
	}
	c6.expectExt("LOGIN_PTO.S_LOGIN_SUC")
	c2.expectClosed() // 被 c6 顶掉

	// 另一个号起同名 → S_MSG，换名成功
	c7 := dial(t, addr)
	c7.sendRaw(xym.CRegister, registerBody("bob", "pw", "bob1"))
	c7.expectExt("LOGIN_PTO.S_SEND_SID")
	c7.recv() // S_CREATE
	c7.sendRaw(xym.CCreate, cat([]byte{1, 0}, str("悟空")))
	if main, _ := c7.recv(); main != xym.SMsg {
		t.Fatalf("dup name: want S_MSG, got %d", main)
	}
	c7.sendRaw(xym.CCreate, cat([]byte{1, 0}, str("八戒")))
	c7.expectExt("LOGIN_PTO.S_NEW_USER")
	c7.expectExt("LOGIN_PTO.S_LOGIN_SUC")

	// 存档落盘，另开一个连接能读回
	st := openTestStore(t, dir)
	roles, err := OpenRoles(st)
	if err != nil || len(roles.ByUID) != 2 {
		t.Fatalf("roles reopen: %v %d", err, len(roles.ByUID))
	}
	accs, err := OpenAccounts(st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accs.Login("alice", "newpw", ""); err != nil {
		t.Fatalf("reopen accounts: %v", err)
	}
}

func TestHandshakeTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.HandshakeTimeout = 200 * time.Millisecond
	addr, _ := startServer(t, cfg)
	c := dial(t, addr)
	c.expectClosed()
}

// TestHotfix：analysis/data/hotfix 由 session-01 抓包里的 S_SEND_NEW_HOTFIX_CONTENT 拆出；
// 重新打包必须与抓包 raw 逐字节相同，且登录时排在 S_SEND_SID 之后、S_LOGIN_SUC 之前。
func TestHotfix(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "analysis", "data", "hotfix")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no hotfix dir")
	}
	body, n, err := LoadHotfix(dir, "1.5.403")
	if err != nil || n == 0 {
		t.Fatalf("load: n=%d err=%v", n, err)
	}
	if want := captureRaw(t, 2007); want != nil && !bytes.Equal(body, want) {
		t.Fatalf("hotfix body differs from capture (%d vs %d bytes)", len(body), len(want))
	}

	cfg := DefaultConfig()
	cfg.Hotfix = body
	addr, _ := startServer(t, cfg)
	c := dial(t, addr)
	c.sendRaw(xym.CRegister, registerBody("hf", "pw", "abcd"))
	c.expectExt("LOGIN_PTO.S_SEND_SID")
	c.recv() // S_CREATE
	c.sendRaw(xym.CCreate, cat([]byte{1, 0}, str("沙僧")))
	if m := c.expectExt("LOGIN_PTO.S_SEND_NEW_HOTFIX_CONTENT"); len(m["contents"].([]xym.Msg)) != n {
		t.Fatalf("hotfix entries %d", len(m["contents"].([]xym.Msg)))
	}
	c.expectExt("LOGIN_PTO.S_NEW_USER")
	c.expectExt("LOGIN_PTO.S_LOGIN_SUC")
}

// captureRaw 从 XYM_CAPTURES/session-01.jsonl 取第一条 S2C pid 的 raw；没设环境变量返回 nil。
func captureRaw(t *testing.T, pid int) []byte {
	dir := os.Getenv("XYM_CAPTURES")
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "session-01.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(data, []byte("\n")) {
		var r struct {
			Dir string `json:"dir"`
			PID int    `json:"pid"`
			Raw string `json:"raw"`
		}
		if json.Unmarshal(line, &r) == nil && r.Dir == "S2C" && r.PID == pid && r.Raw != "" {
			b, _ := hex.DecodeString(r.Raw)
			return b
		}
	}
	return nil
}

// TestPlayRecord：「重播」C_PLAY_RECORD{type 1} 原样重发上一场战报；还没打过时不回。
func TestPlayRecord(t *testing.T) {
	_, addr, _ := startServerSrv(t, DefaultConfig())
	c := enterWorld(t, addr, "replayer")
	c.sendExt("FIGHT_PTO.C_PLAY_RECORD", xym.Msg{"type": 1, "gid": 0, "ttype": 0, "rdtype": 0})
	if got := c.collect(); len(got) != 0 {
		t.Fatalf("replay before any fight: %v", got)
	}
	c.sendExt("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": 10101, "type": 0, "pos1": 1, "pos2": 2})
	first := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	c.collect()
	c.sendExt("FIGHT_PTO.C_PLAY_RECORD", xym.Msg{"type": 1, "gid": 0, "ttype": 0, "rdtype": 0})
	again := c.expectExt("FIGHT_PTO.S_FIGHT_RECORD")
	if again["rid"] != first["rid"] || fmt.Sprint(again["record"]) != fmt.Sprint(first["record"]) {
		t.Fatalf("replay differs: rid %v vs %v", again["rid"], first["rid"])
	}
	if got := c.collect(); len(got) != 0 {
		t.Fatalf("replay sent extra %v", got)
	}
}

// TestDefaultVIP：新角色默认 VIP15，旧存档里 VIP 低于 15 的启动时补到 15。
func TestDefaultVIP(t *testing.T) {
	srv, addr, dir := startServerSrv(t, DefaultConfig())
	enterWorld(t, addr, "viper").conn.Close()
	acc, _ := srv.accounts.Login("viper", "pw", "")
	if v := srv.roles.Get(acc.UID).VIP; v != defaultVIP {
		t.Fatalf("new role vip %d", v)
	}
	srv.roles.Update(acc.UID, func(ro *Role) bool { ro.VIP = 3; return true })
	st := openTestStore(t, dir)
	roles, err := OpenRoles(st)
	if err != nil || roles.Get(acc.UID).VIP != defaultVIP {
		t.Fatalf("migrated vip %v %v", roles.Get(acc.UID), err)
	}
}
