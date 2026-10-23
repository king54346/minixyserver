package xymsrv

import (
	"slices"
	"sync"
	"time"

	"turnrpg/internal/store"
)

// 社交数据（好友、好友申请、礼物、私信、邮件）跨玩家读写，放在一个全服的 Social 里、用自己的锁，
// 不去碰别的玩家的 Player（处理请求时已持有自己的 Player 锁，再锁别人的可能互相等死）。
// 存档用 store 的玩家存档接口、保留 uid 0（真实玩家 uid 从 20001 起），每次改动整块写回。

// Social 是全服社交数据。
type Social struct {
	mu sync.Mutex
	st store.Store

	Friends map[int32][]int32            `json:"friends"`  // uid -> 好友
	Applies map[int32][]int32            `json:"applies"`  // uid -> 向他申请的人
	Msgs    map[int32]map[int32][]string `json:"msgs"`     // 收信人 -> 发信人 -> 未读私信
	GiftDay string                       `json:"gift_day"` // 礼物计数所属日期
	Sent    map[int32][]int32            `json:"sent"`     // 今天已送：送礼人 -> 收礼人
	Pending map[int32][]int32            `json:"pending"`  // 待领：收礼人 -> 送礼人
	Got     map[int32]int                `json:"got"`      // 今天已领的礼物数
	Mails   map[int32][]*Mail            `json:"mails"`
	MailSeq int32                        `json:"mail_seq"`
}

// Mail 是一封邮件（S_SEND_OPEN_NEW_MAIL.list 的一项）。附件：S 是奖励编码（WP20011 元宝、WP20010 银两、
// WP<道具号>…），R 是数量；isStar 为重要邮件。
type Mail struct {
	Idx     int32        `json:"idx"`
	Title   string       `json:"title"`
	Text    string       `json:"text"`
	Rewards []MailReward `json:"rewards,omitempty"`
	Got     bool         `json:"got,omitempty"`
	Read    bool         `json:"read,omitempty"`
	Time    int64        `json:"time"`
	Star    bool         `json:"star,omitempty"`
}

type MailReward struct {
	S string `json:"s"`
	R int32  `json:"r"`
}

func openSocial(st store.Store) *Social {
	so := &Social{st: st}
	loadGlobal(st, socialUID, so, "social")
	so.init()
	return so
}

func (so *Social) init() {
	if so.Friends == nil {
		so.Friends = map[int32][]int32{}
	}
	if so.Applies == nil {
		so.Applies = map[int32][]int32{}
	}
	if so.Msgs == nil {
		so.Msgs = map[int32]map[int32][]string{}
	}
	if so.Mails == nil {
		so.Mails = map[int32][]*Mail{}
	}
	if d := today(); so.GiftDay != d {
		so.GiftDay, so.Sent, so.Pending, so.Got = d, map[int32][]int32{}, map[int32][]int32{}, map[int32]int{}
	}
}

// save 写回存档；调用方持有 so.mu。
func (so *Social) save() {
	saveGlobal(so.st, socialUID, so, "social")
}

// do 在锁内执行 fn（先做跨天重置），fn 返回 true 时写回存档。
func (so *Social) do(fn func() bool) {
	so.mu.Lock()
	defer so.mu.Unlock()
	so.init()
	if fn() {
		so.save()
	}
}

func addUniq(l []int32, x int32) []int32 {
	if slices.Contains(l, x) {
		return l
	}
	return append(l, x)
}

func remove(l []int32, x int32) []int32 {
	return slices.DeleteFunc(l, func(y int32) bool { return y == x })
}

func (so *Social) isFriend(a, b int32) bool { return slices.Contains(so.Friends[a], b) }

// ---- 在线会话 ----

// sessionOf 是 uid 的在线会话（不在线返回 nil）。
func (srv *Server) sessionOf(uid int32) *Session {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.online[uid]
}

// onlineSessions 是全部在线会话的快照。
func (srv *Server) onlineSessions() []*Session {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	out := make([]*Session, 0, len(srv.online))
	for _, s := range srv.online {
		out = append(out, s)
	}
	return out
}

func nowUnix() int64 { return time.Now().Unix() }
