// xymload 是压测工具：用管理接口建 N 个完美账号，N 个机器人同时登录，每隔 think 时间打一次关卡（体力不够就用元宝买），
// 统计战斗响应时间（发出 C_START_FIGHT 到收到 S_FIGHT_RECORD）和下行流量。只对自己的测试服用。
//
//	go run ./cmd/xymload -addr 127.0.0.1:28417 -admin 127.0.0.1:28418 -n 300 -dur 5m -think 5s
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"turnrpg/internal/xym"
)

var (
	addr           = flag.String("addr", "127.0.0.1:28417", "游戏端口")
	admin          = flag.String("admin", "127.0.0.1:28418", "管理接口（建完美账号）")
	n              = flag.Int("n", 300, "机器人数")
	dur            = flag.Duration("dur", 5*time.Minute, "压测时长")
	think          = flag.Duration("think", 5*time.Second, "两次战斗之间的平均间隔")
	prefix         = flag.String("prefix", "load", "账号前缀")
	skipMk         = flag.Bool("skip-create", false, "账号已建好，不再调 /perfect")
	loginSpacing   = flag.Duration("login-spacing", 20*time.Millisecond, "各客户端开始登录的间隔")
	scenario       = flag.String("scenario", "battle", "请求场景：battle 或 mixed（仙灵、热点与战斗）")
	reconnectEvery = flag.Int("reconnect-every", 0, "每多少场战斗主动断线并用 SID 重连，0 = 不测试")
	sdkCallback    = flag.Bool("sdk-callback", false, "登录后模拟旧版 SDK 回调帧")
)

var ver = []byte{1, 0, 5, 0, 0x61, 0x01} // 1.5.353

var (
	rxBytes, fights, noPoint, errs atomic.Int64
	reconnections                  atomic.Int64
	gateResets                     atomic.Int64
	latMu                          sync.Mutex
	lats                           []time.Duration
	loginLat                       []time.Duration
)

func main() {
	flag.Parse()
	if *reconnectEvery < 0 {
		log.Fatal("reconnect-every must not be negative")
	}
	if *loginSpacing < 0 {
		log.Fatal("login-spacing must not be negative")
	}
	if *scenario != "battle" && *scenario != "mixed" {
		log.Fatal("invalid scenario")
	}
	if !*skipMk {
		t0 := time.Now()
		sem := make(chan struct{}, 8)
		var wg sync.WaitGroup
		for i := range *n {
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				body, _ := json.Marshal(map[string]string{"urs": urs(i), "pwd": "123456", "code": "abcd1234", "name": fmt.Sprintf("压测%03d", i)})
				r, err := http.Post("http://"+*admin+"/perfect", "application/json", bytes.NewReader(body))
				if err != nil {
					log.Fatal(err)
				}
				r.Body.Close()
			}()
		}
		wg.Wait()
		log.Printf("建 %d 个完美账号 %v", *n, time.Since(t0).Round(time.Millisecond))
	}
	end := time.Now().Add(*dur)
	var wg sync.WaitGroup
	t0 := time.Now()
	for i := range *n {
		wg.Add(1)
		go func() { defer wg.Done(); bot(i, end) }()
		time.Sleep(*loginSpacing)
	}
	go func() {
		for range time.Tick(30 * time.Second) {
			report(time.Since(t0))
		}
	}()
	wg.Wait()
	report(time.Since(t0))
	if errs.Load() > 0 {
		os.Exit(1)
	}
}

func urs(i int) string { return fmt.Sprintf("%s%03d", *prefix, i) }

func report(el time.Duration) {
	latMu.Lock()
	l := slices.Clone(lats)
	ll := slices.Clone(loginLat)
	latMu.Unlock()
	slices.Sort(l)
	slices.Sort(ll)
	pct := func(s []time.Duration, p float64) time.Duration {
		if len(s) == 0 {
			return 0
		}
		return s[min(int(float64(len(s))*p), len(s)-1)].Round(time.Millisecond)
	}
	sec := el.Seconds()
	log.Printf("%v 战斗 %d（%.1f/秒） 响应 p50 %v p95 %v p99 %v 最长 %v | 登录 %d p50 %v p95 %v | 下行 %.2f MB/s（%.1f Mbps） 买体力 %d 错误 %d 重连 %d 重置关卡 %d",
		el.Round(time.Second), fights.Load(), float64(fights.Load())/sec, pct(l, .5), pct(l, .95), pct(l, .99), pct(l, 1),
		len(ll), pct(ll, .5), pct(ll, .95), float64(rxBytes.Load())/sec/1e6, float64(rxBytes.Load())*8/sec/1e6, noPoint.Load(), errs.Load(), reconnections.Load(), gateResets.Load())
}

type frame struct {
	main byte
	name string
	uid  int64
	sid  string
	ask  int64
	text string
}

func readFrames(conn net.Conn) <-chan frame {
	ch := make(chan frame, 256)
	go func() {
		r := bufio.NewReader(conn)
		for {
			main, body, err := xym.ReadS2C(r)
			if err != nil {
				close(ch)
				return
			}
			rxBytes.Add(int64(len(body) + 4))
			f := frame{main: main}
			if main == xym.MainExt {
				if _, spec, data, err := xym.DecodeExt(xym.S2C, body); err == nil {
					f.name = spec.Module + "." + spec.Name
					if f.name == "LOGIN_PTO.S_SEND_SID" {
						f.uid, _ = data["uid"].(int64)
						f.sid, _ = data["sid"].(string)
					}
					if f.name == "ETC_PTO.S_ASK_BOX" {
						f.ask, _ = data["session"].(int64)
						f.text, _ = data["text"].(string)
					}
				}
			}
			select {
			case ch <- f:
			default: // 机器人不关心的推送，满了就丢
			}
		}
	}()
	return ch
}

func bot(i int, end time.Time) {
	conn, err := net.Dial("tcp", *addr)
	if err != nil {
		errs.Add(1)
		log.Printf("bot %d dial: %v", i, err)
		return
	}
	defer func() {
		if conn != nil {
			conn.Close()
		}
	}()
	ch := readFrames(conn)
	var uid int64
	var sid string
	send := func(name string, m xym.Msg) {
		conn.Write(xym.AppendC2S(nil, xym.MainExt, xym.ByName(name).MustEncode(m)))
	}
	wait := func(timeout time.Duration, names ...string) string {
		dl := time.After(timeout)
		for {
			select {
			case f, ok := <-ch:
				if !ok {
					return ""
				}
				if f.sid != "" {
					uid, sid = f.uid, f.sid
				}
				if f.ask > 0 && strings.Contains(f.text, "挑战次数已用完") && strings.Contains(f.text, "重置挑战") && slices.Contains(names, "FIGHT_PTO.S_FIGHT_RECORD") {
					send("ETC_PTO.C_ASK_BOX", xym.Msg{"session": f.ask, "opt": 0})
					gateResets.Add(1)
					continue
				}
				if slices.Contains(names, f.name) {
					return f.name
				}
			case <-dl:
				log.Printf("bot %d timeout waiting %v", i, names)
				return ""
			}
		}
	}
	t0 := time.Now()
	body := bytes.Join([][]byte{ver, xym.VarStr(urs(i)), xym.VarStr("123456"), xym.VarStr("gen:load-device")}, nil)
	conn.Write(xym.AppendC2S(nil, xym.CLogin, body))
	if wait(30*time.Second, "LOGIN_PTO.S_LOGIN_SUC") == "" {
		errs.Add(1)
		log.Printf("bot %d login failed", i)
		return
	}
	if *sdkCallback {
		conn.Write(xym.AppendC2S(nil, xym.CNewLogin, ver))
	}
	// 进主界面时客户端会拉的一些数据
	for _, q := range []string{"PET_PTO.C_APPLY_PET_LIST", "LINEUP_PTO.C_GET_LINEUPS", "ETC_PTO.C_GET_ENTRANCE_DATA", "LIEXI_PTO.C_OPEN_MAIN"} {
		send(q, nil)
	}
	latMu.Lock()
	loginLat = append(loginLat, time.Since(t0))
	latMu.Unlock()
	time.Sleep(time.Duration(rand.Int64N(int64(*think))))
	rounds, reconnectedRound := 0, 0
	for time.Now().Before(end) {
		if *scenario == "mixed" {
			for _, q := range []struct{ req, reply string }{
				{"ETC_PTO.C_OPEN_XIANLING_SHOP", "ETC_PTO.S_SEND_XIANLING_SHOP_INFO"},
				{"ETC_PTO.C_CHANGE_HOT_ITEM", "ETC_PTO.S_UPDATE_RANDOM_PET"},
			} {
				started := time.Now()
				send(q.req, nil)
				if wait(15*time.Second, q.reply) == "" {
					errs.Add(1)
				} else {
					latMu.Lock()
					lats = append(lats, time.Since(started))
					latMu.Unlock()
				}
			}
		}
		t := time.Now()
		send("TASK_PTO.C_START_FIGHT", xym.Msg{"gid": int64(10101), "type": int64(0), "pos1": int64(1), "pos2": int64(2), "pos3": int64(3), "pos4": int64(4), "pos5": int64(5)})
		switch wait(30*time.Second, "FIGHT_PTO.S_FIGHT_RECORD", "ETC_PTO.S_OPEN_POINT_BUY") {
		case "FIGHT_PTO.S_FIGHT_RECORD":
			fights.Add(1)
			rounds++
			latMu.Lock()
			lats = append(lats, time.Since(t))
			latMu.Unlock()
		case "ETC_PTO.S_OPEN_POINT_BUY":
			noPoint.Add(1)
			send("ETC_PTO.C_BUY_POINT_CONFIRM", xym.Msg{"type": int64(1), "buy_type": int64(1)})
		default:
			errs.Add(1)
		}
		if *reconnectEvery > 0 && rounds > 0 && rounds%*reconnectEvery == 0 && rounds != reconnectedRound {
			if uid <= 0 || sid == "" {
				errs.Add(1)
				log.Printf("bot %d missing SID", i)
				return
			}
			conn.Close()
			conn, err = net.Dial("tcp", *addr)
			if err != nil {
				errs.Add(1)
				return
			}
			ch = readFrames(conn)
			body := append(slices.Clone(ver), xym.U32LE(uint32(uid))...)
			body = append(body, xym.VarStr(sid)...)
			conn.Write(xym.AppendC2S(nil, xym.CSidLogin, body))
			if wait(30*time.Second, "LOGIN_PTO.S_LOGIN_SUC") == "" {
				errs.Add(1)
				log.Printf("bot %d SID reconnect failed", i)
				return
			}
			reconnections.Add(1)
			reconnectedRound = rounds
			if *sdkCallback {
				conn.Write(xym.AppendC2S(nil, xym.CNewLogin, ver))
			}
		}
		time.Sleep(*think/2 + time.Duration(rand.Int64N(int64(*think))))
	}
}
