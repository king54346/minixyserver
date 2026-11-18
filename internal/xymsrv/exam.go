package xymsrv

import (
	"sort"
	"time"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

// 科举答题（答题事件，view/DailyExamView.lua、view/EmergencyEventListView.lua）：
//   - 开启时间：每天 00:00 准时在「事件」界面开启（TREE_PTO.S_EVENT_LIST.answer_question_open = 1）。
//   - 题目数量与场次：每人每天 1 局，每局 20 道题（来自 gamedata.ExamQuestions / exam.csv），每题限时 10 秒。
//   - 得分机制：答对（ans == 0 是 / 1 否）得基础分 50 + 剩余秒数×5（50~100 分），20 题满分 2000 分；答错或超时得 0 分。
//   - 奖励结算：20 题打完按积分线性结算奖励：元宝 10,000~100,000（1w~10w），银两 1,000,000~10,000,000（100w~1000w），
//     以及各类游戏道具（黄金宝箱、黄金钥匙、请神符、战力丹、气血丹各 1~10 个）。

const (
	examOver      = 0 // EXAM_OVER：关闭答题界面
	examInit      = 1 // EXAM_INIT
	examIng       = 2 // EXAM_ING：答题中
	examRoundOver = 3 // EXAM_ROUND_OVER

	examQuestionsPerGame = 20
	examQuestionSec      = 10
	examBasePoint        = 50
	examSpeedPoint       = 5
	examMaxScore         = examQuestionsPerGame * (examBasePoint + examQuestionSec*examSpeedPoint) // 2000

	examMinGold = 10000
	examMaxGold = 100000
	examMinCash = 1000000
	examMaxCash = 10000000
)

var examRewardItems = []int{
	10002, // 黄金宝箱
	10003, // 黄金钥匙
	10114, // 请神符
	10126, // 战力丹
	10127, // 气血丹
}

// examNow 供测试拨快秒数。
var examNow = func() int64 { return time.Now().Unix() }

// ExamState 是科举答题的个人每日存档（Player.Exam）。
type ExamState struct {
	Day       string `json:"day"`
	Questions []int  `json:"questions,omitempty"` // 本局抽出的 20 道题在 gamedata.ExamQuestions 中的下标
	Idx       int    `json:"idx"`                 // 当前第几题（1..20）
	QStart    int64  `json:"qstart"`              // 当前题开始时间戳（秒）
	IsRight   int    `json:"isright"`             // 当前题作答结果：1 正确，0 错误
	Score     int    `json:"score"`               // 本局累计积分
	Done      bool   `json:"done"`                // 当前题是否已作答
	Finished  bool   `json:"finished"`            // 今日 1 局是否已打完并结算
}

type examRankScore struct {
	day   string
	score int
}

// Publish an immutable score while holding the player's lock, or before caching the player.
func (p *Player) publishExamRank() {
	ex := p.Exam
	if ex != nil && (ex.Score > 0 || ex.Finished) {
		p.examRank.Store(&examRankScore{day: ex.Day, score: ex.Score})
	}
}

func (p *Player) exam() *ExamState {
	d := today()
	if p.Exam == nil || p.Exam.Day != d {
		p.Exam = &ExamState{Day: d}
	}
	return p.Exam
}

func (p *Player) examOpen() bool {
	return !p.exam().Finished
}

func init() {
	Handle("ANSWER_QUESTION_PTO.C_GET_QUESTION", onGetQuestion)
	Handle("ANSWER_QUESTION_PTO.C_ANSWER_QUESTION", onAnswerQuestion)
	Handle("ANSWER_QUESTION_PTO.C_GET_RANK_LIST", onGetExamRank)
	regEvent(func(p *Player, m xym.Msg) { m["answer_question_open"] = b2i(p.examOpen()) }) // 突发事件里的答题入口
}

func sendExamQuestion(s *Session, ex *ExamState, left int) {
	q := gamedata.ExamQuestions[ex.Questions[ex.Idx-1]]
	s.SendExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION", xym.Msg{
		"state":          examIng,
		"round":          1,
		"qid":            ex.Idx,
		"left_questions": len(ex.Questions) - ex.Idx,
		"question":       q.Question,
		"lefttime":       left,
		"isright":        ex.IsRight,
		"done":           b2i(ex.Done),
		"score":          ex.Score,
	})
}

func onGetQuestion(s *Session, p *Player, _ xym.Msg) {
	ex := p.exam()
	now := examNow()
	if ex.Finished {
		s.SendExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION", xym.Msg{"state": examOver, "round": 1, "score": ex.Score})
		return
	}
	if len(ex.Questions) == 0 {
		n := min(examQuestionsPerGame, len(gamedata.ExamQuestions))
		ex.Questions = s.actRng().Perm(len(gamedata.ExamQuestions))[:n]
		ex.Idx = 1
		ex.QStart = now
		ex.Done = false
		ex.IsRight = 0
		ex.Score = 0
		p.saveLog(s)
		sendExamQuestion(s, ex, examQuestionSec)
		return
	}
	// 当前题已作答或 10 秒倒计时已到：推进到下一题；20 题全部完成则结算奖励并结束今日答题。
	if ex.Done || now-ex.QStart >= examQuestionSec {
		if ex.Idx >= len(ex.Questions) {
			s.settleExam(p, ex)
			s.SendExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION", xym.Msg{"state": examOver, "round": 1, "score": ex.Score})
			return
		}
		ex.Idx++
		ex.QStart = now
		ex.Done = false
		ex.IsRight = 0
		p.saveLog(s)
		sendExamQuestion(s, ex, examQuestionSec)
		return
	}
	left := max(1, examQuestionSec-int(now-ex.QStart))
	sendExamQuestion(s, ex, left)
}

func onAnswerQuestion(s *Session, p *Player, m xym.Msg) {
	ex := p.exam()
	now := examNow()
	if ex.Finished || len(ex.Questions) == 0 {
		s.SendExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION", xym.Msg{"state": examOver, "round": 1, "score": ex.Score})
		return
	}
	qid, _ := m["qid"].(int64)
	ans, _ := m["ans"].(int64)
	// 若上一题已作答或超时且请求直接答下一题，自动推进题号
	if (ex.Done || now-ex.QStart >= examQuestionSec) && ex.Idx < len(ex.Questions) && int(qid) == ex.Idx+1 {
		ex.Idx++
		ex.QStart = now
		ex.Done = false
		ex.IsRight = 0
	}
	if !ex.Done {
		ex.Done = true
		q := gamedata.ExamQuestions[ex.Questions[ex.Idx-1]]
		if int(ans) == q.Ans && now-ex.QStart <= examQuestionSec {
			ex.IsRight = 1
			elapsed := max(0, min(examQuestionSec, int(now-ex.QStart)))
			rem := examQuestionSec - elapsed
			ex.Score += examBasePoint + rem*examSpeedPoint
			p.publishExamRank()
		} else {
			ex.IsRight = 0
		}
		if ex.Idx >= len(ex.Questions) {
			s.settleExam(p, ex)
		} else {
			p.saveLog(s)
		}
	}
	sendExamQuestion(s, ex, 0)
}

func examRewardsForScore(score int) (gold, cash int32, items [][2]int) {
	sc := max(0, min(examMaxScore, score))
	gold = int32(examMinGold + sc*(examMaxGold-examMinGold)/examMaxScore)
	cash = int32(examMinCash + sc*(examMaxCash-examMinCash)/examMaxScore)
	cnt := 1 + sc*9/examMaxScore
	items = make([][2]int, 0, len(examRewardItems))
	for _, id := range examRewardItems {
		items = append(items, [2]int{id, cnt})
	}
	return gold, cash, items
}

func (s *Session) settleExam(p *Player, ex *ExamState) {
	if ex.Finished {
		return
	}
	ex.Finished = true
	p.publishExamRank()
	gold, cash, items := examRewardsForScore(ex.Score)
	s.grantTaskReward(p, items, nil, cash, gold, 0)
	s.sendEventList(p)
	s.SendExt("ETC_PTO.S_UPDATE_ENTRANCE", xym.Msg{"list": gamedata.Static.Entrance})
}

func onGetExamRank(s *Session, p *Player, _ xym.Msg) {
	ex := p.exam()
	d := today()
	type entry struct {
		uid   int32
		score int
	}
	var entries []entry
	// The request already holds p.mu. Reading published scores avoids locking another player.
	s.srv.players.mu.RLock()
	for uid, pl := range s.srv.players.byUID {
		if rank := pl.examRank.Load(); rank != nil && rank.day == d {
			entries = append(entries, entry{uid: uid, score: rank.score})
		}
	}
	s.srv.players.mu.RUnlock()
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].score != entries[j].score {
			return entries[i].score > entries[j].score
		}
		return entries[i].uid < entries[j].uid
	})
	list := make([]xym.Msg, 0, len(entries))
	myRank := 0
	for i, e := range entries {
		rank := i + 1
		if e.uid == p.UID {
			myRank = rank
		}
		name, icon, grade := s.srv.roleBrief(e.uid)
		dfName := ""
		var dfIcon int32
		s.srv.dongfu.do(func() bool {
			if df, _ := s.srv.dongfu.of(e.uid); df != nil {
				dfName, dfIcon = df.Name, df.Icon
			}
			return false
		})
		list = append(list, xym.Msg{
			"uid":         e.uid,
			"rank":        rank,
			"name":        name,
			"grade":       grade,
			"icon":        icon,
			"score":       e.score,
			"dongfu_name": dfName,
			"dongfu_icon": dfIcon,
		})
	}
	var rankLists []xym.Msg
	for r := 1; r <= 3; r++ {
		rankLists = append(rankLists, xym.Msg{
			"round":    r,
			"rank_pos": myRank,
			"list":     list,
		})
	}
	s.SendExt("ANSWER_QUESTION_PTO.S_SEND_RANK_LIST", xym.Msg{
		"score":      ex.Score,
		"rank_lists": rankLists,
	})
}
