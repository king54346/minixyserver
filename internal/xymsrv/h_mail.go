package xymsrv

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 邮件（MAIL_PTO 新版邮件）：系统 / 管理员发给玩家，可带元宝、银两、魂值、道具等附件，玩家在聊天界面的「邮件」页
// 查看、领取、删除。发信入口是 admin.go 的管理接口（Server.SendMail）。
//
// 依据：view/ChatMailView.lua（C_OPEN_NEW_MAIL{idx 0} 拉列表 → S_SEND_OPEN_NEW_MAIL；没附件的点开发
// C_CLICK_NEW_MAIL_CONTENT → S_CLICK_NEW_MAIL_CONTENT_SUCC；「全部领取」C_CLICK_NEW_MAIL_REWARD{idx 0} →
// S_CLICK_NEW_MAIL_REWARD_SUCC；清空 C_DEL_ALL_NEW_MAIL → S_DEL_NEW_MAIL_SUCC，之后客户端自己重拉列表和
// C_GET_NEW_MAIL_NUM）；附件 rewardlist{rid, R, S, golda}：S 非空时按「两字母类型 + 编号」显示（WP 道具…），
// 数量显示为 R（logic/reward_mgr.getRewardInfoWithS）。元宝 / 银两 / 魂值用道具 20011 / 20010 / 20012 的图标表示，
// 领取时折成人物属性。
//
// 估计：每人最多留 100 封（多了删最旧的已领 / 已读）；没有过期时间。

const (
	maxMails = 100
	mailCash = 20010
	mailGold = 20011
	mailSoul = 20012
)

// MailSpec 是要发出的一封邮件。Items 的键是奖励编码（WP10317、DZ25001…），值是数量。
type MailSpec struct {
	Items map[string]int32 `json:"items,omitempty"`
	Title string           `json:"title"`
	Text  string           `json:"text"`
	Gold  int32            `json:"gold"`
	Cash  int32            `json:"cash"`
	Soul  int32            `json:"soul"`
	Star  bool             `json:"star"`
}

func (spec MailSpec) rewards() []MailReward {
	var out []MailReward
	if spec.Gold > 0 {
		out = append(out, MailReward{S: "WP" + strconv.Itoa(mailGold), R: spec.Gold})
	}
	if spec.Cash > 0 {
		out = append(out, MailReward{S: "WP" + strconv.Itoa(mailCash), R: spec.Cash})
	}
	if spec.Soul > 0 {
		out = append(out, MailReward{S: "WP" + strconv.Itoa(mailSoul), R: spec.Soul})
	}
	codes := make([]string, 0, len(spec.Items))
	for code := range spec.Items {
		codes = append(codes, code)
	}
	slices.Sort(codes)
	for _, code := range codes {
		if n := spec.Items[code]; n > 0 {
			out = append(out, MailReward{S: code, R: n})
		}
	}
	return out
}

// SendMail 给 to（0 = 全服所有角色）发一封邮件，返回发出的封数。在线的玩家立刻刷新未读数。
func (srv *Server) SendMail(to int32, spec MailSpec) (int, error) {
	var uids []int32
	if to == 0 {
		srv.roles.mu.Lock()
		for uid := range srv.roles.ByUID {
			uids = append(uids, uid)
		}
		srv.roles.mu.Unlock()
	} else {
		uids = []int32{to}
	}
	return srv.sendMailToUIDs(uids, spec)
}

func (srv *Server) sendMailToUIDs(uids []int32, spec MailSpec) (int, error) {
	if strings.TrimSpace(spec.Title) == "" {
		return 0, fmt.Errorf("title is empty")
	}
	uids = slices.Clone(uids)
	slices.Sort(uids)
	uids = slices.Compact(uids)
	for _, uid := range uids {
		if uid <= 0 || srv.roles.Get(uid) == nil {
			return 0, fmt.Errorf("no role %d", uid)
		}
	}
	if len(uids) == 0 {
		return 0, nil
	}
	now := time.Now().Unix()
	so := srv.social
	so.mu.Lock()
	so.init()
	oldSeq := so.MailSeq
	oldMail := make(map[int32][]*Mail, len(uids))
	rewards := spec.rewards()
	for _, uid := range uids {
		oldMail[uid] = so.Mails[uid]
		so.MailSeq++
		l := append(slices.Clone(so.Mails[uid]), &Mail{Idx: so.MailSeq, Title: spec.Title, Text: spec.Text,
			Rewards: slices.Clone(rewards), Time: now, Star: spec.Star})
		so.Mails[uid] = trimMails(l)
	}
	var err error
	if so.st != nil {
		var data []byte
		data, err = json.Marshal(so)
		if err == nil {
			err = so.st.SaveGlobal(string(globalSocial), data)
		}
	}
	if err != nil {
		so.MailSeq = oldSeq
		for _, uid := range uids {
			if oldMail[uid] == nil {
				delete(so.Mails, uid)
			} else {
				so.Mails[uid] = oldMail[uid]
			}
		}
	}
	so.mu.Unlock()
	if err != nil {
		return 0, fmt.Errorf("save mail: %w", err)
	}
	for _, uid := range uids {
		if s := srv.sessionOf(uid); s != nil {
			s.sendMailNum()
		}
	}
	return len(uids), nil
}

// trimMails 删到 maxMails 封：先删最旧的已处理邮件，不够再删最旧的。
func trimMails(l []*Mail) []*Mail {
	for len(l) > maxMails {
		i := slices.IndexFunc(l, func(m *Mail) bool { return m.Read && (len(m.Rewards) == 0 || m.Got) })
		if i < 0 {
			i = 0
		}
		l = slices.Delete(l, i, i+1)
	}
	return l
}

func init() {
	Handle("MAIL_PTO.C_GET_NEW_MAIL_NUM", func(s *Session, p *Player, m xym.Msg) { s.sendMailNum() })
	Handle("MAIL_PTO.C_OPEN_NEW_MAIL", func(s *Session, p *Player, m xym.Msg) { s.sendMailList() })
	Handle("MAIL_PTO.C_CLICK_NEW_MAIL_CONTENT", onReadMail)
	Handle("MAIL_PTO.C_CLICK_NEW_MAIL_REWARD", onClaimMail)
	Handle("MAIL_PTO.C_DEL_ALL_NEW_MAIL", onDelMail)
}

func (s *Session) sendMailNum() {
	// Authentication binds the session before role loading completes. Scheduler
	// notifications must wait until login has published the role pointer.
	if !s.inGame.Load() {
		return
	}
	unreward, unread := 0, 0
	s.srv.social.do(func() bool {
		for _, m := range s.srv.social.Mails[s.role.UID] {
			if len(m.Rewards) > 0 && !m.Got {
				unreward++
			}
			if !m.Read {
				unread++
			}
		}
		return false
	})
	s.SendExt("MAIL_PTO.S_GET_NEW_MAIL_NUM", xym.Msg{"unreward": unreward, "unread": unread})
}

func (s *Session) sendMailList() {
	list := []xym.Msg{}
	s.srv.social.do(func() bool {
		for _, m := range s.srv.social.Mails[s.role.UID] {
			rl := []xym.Msg{}
			for _, r := range m.Rewards {
				rl = append(rl, xym.Msg{"rid": 0, "R": r.R, "S": r.S, "golda": 0})
			}
			list = append(list, xym.Msg{"idx": m.Idx, "title": m.Title, "text": m.Text, "rewardlist": rl,
				"isGetReward": b2i(m.Got), "isRead": b2i(m.Read), "time": m.Time, "isStar": b2i(m.Star)})
		}
		return false
	})
	s.SendExt("MAIL_PTO.S_SEND_OPEN_NEW_MAIL", xym.Msg{"list": list})
}

func onReadMail(s *Session, p *Player, m xym.Msg) {
	idx := int32(m["idx"].(int64))
	found := false
	s.srv.social.do(func() bool {
		for _, ml := range s.srv.social.Mails[s.role.UID] {
			if ml.Idx == idx {
				found, ml.Read = true, true
			}
		}
		return found
	})
	if found {
		s.SendExt("MAIL_PTO.S_CLICK_NEW_MAIL_CONTENT_SUCC", xym.Msg{"idx": idx})
	}
}

// onClaimMail：领取附件（idx 0 = 全部）。元宝 / 银两 / 魂值进人物属性，其余按奖励编码进背包。
// 领取成功会记审计流水（gm.log「mail claim」），便于查谁领了哪封 GM 福利。
func onClaimMail(s *Session, p *Player, m xym.Msg) {
	idx := int32(m["idx"].(int64))
	var got []MailReward
	var done []int32
	var claims []map[string]any
	s.srv.social.do(func() bool {
		for _, ml := range s.srv.social.Mails[s.role.UID] {
			if (idx == 0 || ml.Idx == idx) && len(ml.Rewards) > 0 && !ml.Got {
				ml.Got, ml.Read = true, true
				got = append(got, ml.Rewards...)
				done = append(done, ml.Idx)
				claims = append(claims, map[string]any{"idx": ml.Idx, "title": ml.Title, "rewards": ml.Rewards, "reward_details": opsRewardDetails(ml.Rewards), "grant_rule": ml.GrantRule, "grant_day": ml.GrantDay, "sent_at": ml.Time})
			}
		}
		return len(done) > 0
	})
	if len(done) == 0 {
		s.retMsg("没有可以领取的附件。")
		return
	}
	if b, err := json.Marshal(map[string]any{"uid": s.role.UID, "name": s.role.Name, "mails": claims}); err == nil {
		logGMEvent(s.srv.cfg.GMLog, "mail claim", "uid:"+strconv.Itoa(int(s.role.UID)), 200, string(b))
	}
	var gold, cash, soul int32
	delta := &bagDelta{}
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), uint64(s.role.UID)))
	for _, r := range got {
		switch r.S {
		case "WP" + strconv.Itoa(mailGold):
			gold += r.R
		case "WP" + strconv.Itoa(mailCash):
			cash += r.R
		case "WP" + strconv.Itoa(mailSoul):
			soul += r.R
		default:
			p.giveCode(r.S, r.R, rng, delta)
		}
	}
	p.saveLog(s)
	if gold > 0 || cash > 0 || soul > 0 {
		s.roleUpdate(func(ro *Role) bool { ro.Gold += gold; ro.Cash += cash; ro.SoulExp += soul; return true },
			"gold", "cash", "soulexp")
	}
	list := s.sendDelta(p, delta)
	// 元宝 / 银两 / 魂值放进列表（道具 20011 / 20010 / 20012 的图标），弹窗才会逐项显示（只填 gold 字段时银两不显示，真机所见）
	for _, c := range [][2]int32{{mailGold, gold}, {mailCash, cash}, {mailSoul, soul}} {
		if c[1] > 0 {
			list = append(list, xym.Msg{"rtype": rtItem, "type": c[0], "count": c[1]})
		}
	}
	if s.srv.cfg.EnableGMOps {
		if err := s.srv.ops.audit(OpsEvent{Kind: "mail_claim", UID: s.role.UID, Name: s.role.Name, Day: today(), Status: "processed", Data: claims}); err != nil {
			s.logErr("mail claim audit", err)
		}
	}
	s.SendExt("ETC_PTO.S_REWARD_MSG", rewardMsg(1, list))
	ids := make([]xym.Msg, 0, len(done))
	for _, i := range done {
		ids = append(ids, xym.Msg{"idx": i})
	}
	s.SendExt("MAIL_PTO.S_CLICK_NEW_MAIL_REWARD_SUCC", xym.Msg{"list": ids})
}

// onDelMail：删除邮件（list 为空 = 删掉全部已读且附件已领 / 无附件的）。
func onDelMail(s *Session, p *Player, m xym.Msg) {
	l, _ := m["list"].([]xym.Msg)
	want := map[int32]bool{}
	for _, x := range l {
		want[int32(x["idx"].(int64))] = true
	}
	var del []xym.Msg
	s.srv.social.do(func() bool {
		so := s.srv.social
		so.Mails[s.role.UID] = slices.DeleteFunc(so.Mails[s.role.UID], func(ml *Mail) bool {
			done := ml.Read && (len(ml.Rewards) == 0 || ml.Got)
			if (len(want) == 0 && done) || want[ml.Idx] && done {
				del = append(del, xym.Msg{"idx": ml.Idx})
				return true
			}
			return false
		})
		return len(del) > 0
	})
	if del == nil {
		del = []xym.Msg{}
	}
	s.SendExt("MAIL_PTO.S_DEL_NEW_MAIL_SUCC", xym.Msg{"list": del})
}

// mailItemOK 校验管理接口传来的奖励编码（WP / ZB / FS / DZ / HP / JT / SP + 编号）。
func mailItemOK(code string) bool {
	m := dropRe.FindStringSubmatch(code)
	if m == nil || m[2] == "" {
		return false
	}
	id, _ := strconv.Atoi(m[2])
	switch m[1] {
	case "WP":
		return gamedata.Items[id] != nil
	case "ZB", "JT":
		return gamedata.Suits[id] != nil
	case "FS", "SP":
		return gamedata.Magics[int32(id)] != nil
	case "DZ", "HP":
		return gamedata.Pets[int32(id)] != nil
	}
	return false
}
