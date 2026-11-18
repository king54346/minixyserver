package xymsrv

import (
	"testing"

	"turnrpg/internal/gamedata"
	"turnrpg/internal/xym"
)

func ansForQuestion(t *testing.T, qText string) int {
	t.Helper()
	for _, q := range gamedata.ExamQuestions {
		if q.Question == qText {
			return q.Ans
		}
	}
	t.Fatalf("question not found in gamedata.ExamQuestions: %q", qText)
	return 0
}

func TestDailyExam(t *testing.T) {
	now := int64(1700000000)
	oldNow := examNow
	examNow = func() int64 { return now }
	t.Cleanup(func() { examNow = oldNow })

	srv, addr, _ := startServerSrv(t, DefaultConfig())
	c1 := enterWorld(t, addr, "scholar1")
	acc1, _ := srv.accounts.Login("scholar1", "pw", "")
	p1, _ := srv.players.Get(acc1.UID)

	// 1. 每天 00:00 开启：事件列表 answer_question_open == 1
	c1.sendExt("TREE_PTO.C_EVENT_LIST", nil)
	if ev := c1.expectExt("TREE_PTO.S_EVENT_LIST"); num(ev, "answer_question_open") != 1 {
		t.Fatalf("event list before exam: %v", ev)
	}

	gold0 := srv.roles.Get(acc1.UID).Gold
	cash0 := srv.roles.Get(acc1.UID).Cash

	// 2. 第一名玩家全对且秒答（0 秒耗时），每题 100 分，20 题满分 2000 分，拿最高档 10w 元宝 + 10w 银两 + 各道具 ×10
	for i := 1; i <= examQuestionsPerGame; i++ {
		c1.sendExt("ANSWER_QUESTION_PTO.C_GET_QUESTION", nil)
		q := c1.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION")
		if num(q, "state") != examIng || num(q, "qid") != i || num(q, "done") != 0 || num(q, "lefttime") != examQuestionSec {
			t.Fatalf("q%d get: %v", i, q)
		}
		ans := ansForQuestion(t, q["question"].(string))
		c1.sendExt("ANSWER_QUESTION_PTO.C_ANSWER_QUESTION", xym.Msg{"round": 1, "qid": i, "ans": ans})
		if i < examQuestionsPerGame {
			res := c1.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION")
			if num(res, "done") != 1 || num(res, "isright") != 1 || num(res, "score") != i*100 {
				t.Fatalf("q%d ans: %v", i, res)
			}
		} else {
			got := c1.until(t, "ANSWER_QUESTION_PTO.S_SEND_QUESTION")
			res := got["ANSWER_QUESTION_PTO.S_SEND_QUESTION"]
			if num(res, "done") != 1 || num(res, "isright") != 1 || num(res, "score") != examMaxScore {
				t.Fatalf("q20 ans: %v", res)
			}
			if got["ETC_PTO.S_REWARD_MSG"] == nil {
				t.Fatalf("expected S_REWARD_MSG on 20th question: %v", got)
			}
			if ev := got["TREE_PTO.S_EVENT_LIST"]; ev == nil || num(ev, "answer_question_open") != 0 {
				t.Fatalf("expected answer_question_open=0 after exam: %v", ev)
			}
		}
	}

	ro1 := srv.roles.Get(acc1.UID)
	if ro1.Gold-gold0 != examMaxGold || ro1.Cash-cash0 != examMaxCash {
		t.Fatalf("max score rewards: gold +%d (want %d), cash +%d (want %d)",
			ro1.Gold-gold0, examMaxGold, ro1.Cash-cash0, examMaxCash)
	}
	if p1.itemCount(10002) < 10 || p1.itemCount(10114) < 10 {
		t.Fatalf("max score items: box=%d talisman=%d", p1.itemCount(10002), p1.itemCount(10114))
	}

	// 3. 每天每人限 1 局：再次请求题目返回 state = EXAM_OVER (0)
	c1.sendExt("ANSWER_QUESTION_PTO.C_GET_QUESTION", nil)
	if over := c1.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION"); num(over, "state") != examOver || num(over, "score") != examMaxScore {
		t.Fatalf("after finish get question: %v", over)
	}

	// 4. 第二名玩家：含慢答（耗时 4 秒得 80 分）、答错（0 分）、超时（10 秒自动推进得 0 分）
	c2 := enterWorld(t, addr, "scholar2")
	acc2, _ := srv.accounts.Login("scholar2", "pw", "")
	gold2 := srv.roles.Get(acc2.UID).Gold
	cash2 := srv.roles.Get(acc2.UID).Cash

	// 第 1 题：耗时 4 秒答对 -> 50 + (10-4)*5 = 80 分
	c2.sendExt("ANSWER_QUESTION_PTO.C_GET_QUESTION", nil)
	q1 := c2.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION")
	now += 4
	c2.sendExt("ANSWER_QUESTION_PTO.C_ANSWER_QUESTION", xym.Msg{
		"round": 1, "qid": 1, "ans": ansForQuestion(t, q1["question"].(string)),
	})
	if r1 := c2.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION"); num(r1, "score") != 80 || num(r1, "isright") != 1 {
		t.Fatalf("scholar2 q1: %v", r1)
	}

	// 第 2 题：超时 10 秒未答，下次 C_GET_QUESTION 直接推进到第 3 题
	c2.sendExt("ANSWER_QUESTION_PTO.C_GET_QUESTION", nil)
	if q2 := c2.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION"); num(q2, "qid") != 2 {
		t.Fatalf("scholar2 q2: %v", q2)
	}
	now += 10
	c2.sendExt("ANSWER_QUESTION_PTO.C_GET_QUESTION", nil)
	q3 := c2.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION")
	if num(q3, "qid") != 3 || num(q3, "score") != 80 {
		t.Fatalf("scholar2 timeout advance to q3: %v", q3)
	}

	// 第 3..20 题：全部答错（0 分）
	curQ := q3
	for i := 3; i <= examQuestionsPerGame; i++ {
		if i > 3 {
			c2.sendExt("ANSWER_QUESTION_PTO.C_GET_QUESTION", nil)
			curQ = c2.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION")
		}
		wrongAns := 1 - ansForQuestion(t, curQ["question"].(string))
		c2.sendExt("ANSWER_QUESTION_PTO.C_ANSWER_QUESTION", xym.Msg{"round": 1, "qid": i, "ans": wrongAns})
		if i < examQuestionsPerGame {
			res := c2.expectExt("ANSWER_QUESTION_PTO.S_SEND_QUESTION")
			if num(res, "isright") != 0 || num(res, "score") != 80 {
				t.Fatalf("scholar2 q%d wrong: %v", i, res)
			}
		} else {
			got := c2.until(t, "ANSWER_QUESTION_PTO.S_SEND_QUESTION")
			if got["ETC_PTO.S_REWARD_MSG"] == nil {
				t.Fatalf("scholar2 expected reward msg: %v", got)
			}
		}
	}

	ro2 := srv.roles.Get(acc2.UID)
	gotGold2, gotCash2 := ro2.Gold-gold2, ro2.Cash-cash2
	if gotGold2 <= examMinGold || gotGold2 >= examMaxGold || gotCash2 <= examMinCash || gotCash2 >= examMaxCash {
		t.Fatalf("scholar2 rewards out of range: gold=+%d cash=+%d", gotGold2, gotCash2)
	}

	// 5. 排行榜：scholar1（2000 分）第 1 名，scholar2（80 分）第 2 名
	var rk xym.Msg
	func() {
		p1.mu.Lock()
		defer p1.mu.Unlock()
		c2.sendExt("ANSWER_QUESTION_PTO.C_GET_RANK_LIST", nil)
		rk = c2.expectExt("ANSWER_QUESTION_PTO.S_SEND_RANK_LIST")
	}()
	rlists := rk["rank_lists"].([]xym.Msg)
	if num(rk, "score") != 80 || len(rlists) != 3 || num(rlists[0], "rank_pos") != 2 {
		t.Fatalf("rank list: %v", rk)
	}
	board := rlists[0]["list"].([]xym.Msg)
	if len(board) != 2 || num(board[0], "score") != 2000 || num(board[1], "score") != 80 {
		t.Fatalf("board: %v", board)
	}
}
