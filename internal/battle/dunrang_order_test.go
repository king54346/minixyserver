package battle

import (
	"testing"
)

func TestDunrangRandomSkipsUnavailable(t *testing.T) {
	six := newUnit(fu(6, 10, 1000, 1), 0)
	seven := newUnit(fu(7, 10, 1000, 1, levelID(famPanTaoYan, 4)), 0)
	eight := newUnit(fu(8, 10, 1000, 1), 0)
	dead := newUnit(fu(9, 10, 1000, 1), 0)
	dead.hp = 0
	gone := newUnit(fu(10, 10, 1000, 1), 0)
	gone.gone = true
	front := newUnit(fu(1, 10, 1000, 1), 0)
	enemy := newUnit(fu(106, 10, 1000, 1), 1)
	b := faqBattle(eight, seven, six, dead, gone, front, enemy)
	counts := map[*unit]int{}
	for i := 0; i < 3000; i++ {
		counts[b.dunrangTo(0)]++
	}
	if len(counts) != 3 {
		t.Fatalf("invalid candidate set: %v", counts)
	}
	for _, u := range []*unit{six, seven, eight} {
		if counts[u] < 800 || counts[u] > 1200 {
			t.Fatalf("biased selection at slot %d: %d/3000", u.Pos, counts[u])
		}
	}
	six.hp = 0
	seven.gone = true
	if b.dunrangTo(0) != eight {
		t.Fatal("must choose the only eligible bench unit")
	}
	eight.hp = 0
	if b.dunrangTo(0) != nil {
		t.Fatal("must not choose front, enemy, dead or gone units")
	}
	six.hp = 1000
	b.swap(front, six)
	if b.dunrangTo(0) != front {
		t.Fatal("candidate set must use current positions after swapping")
	}
}

// TestLeiyingStaysBackInPassiveSwaps：遁让 / 驱逐 / 飞仙遁入 都不会在还有别的后阵时把雷震子换上前；
// 只有他是唯一可选后阵时才会选他（2026-11-16 用户规则）。
func TestLeiyingStaysBackInPassiveSwaps(t *testing.T) {
	lei := newUnit(fu(6, 0, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	other := newUnit(fu(7, 0, 1000, 1), 0)
	front := newUnit(fu(1, 0, 1000, 1), 0)
	b := faqBattle(lei, other, front)
	for i := 0; i < 50; i++ {
		if got := b.dunrangTo(0); got != other {
			t.Fatalf("dunrang picked %v, want the ordinary bench unit", got)
		}
		if got := b.quzhuPick(front); got != other {
			t.Fatalf("quzhu picked %v, want the ordinary bench unit", got)
		}
	}
	if got := b.dunruTarget(0); got != other {
		t.Fatalf("dunru picked %v, want the ordinary bench unit", got)
	}
	other.hp, other.eaten = 0, true
	if b.dunrangTo(0) != lei || b.dunruTarget(0) != lei || b.quzhuPick(front) != lei {
		t.Fatal("雷震子 must be picked when he is the only option")
	}
}

// TestLeiyingSwapPriority：雷影触发的遁让换人不再随机——未遁入的入阵护法优先，同档按站位；雷震子不抢位。
func TestLeiyingSwapPriority(t *testing.T) {
	six := newUnit(fu(6, 10, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	seven := newUnit(fu(7, 10, 1000, 1, levelID(famPanTaoYan, 4)), 0)
	eight := newUnit(fu(8, 10, 1000, 1, levelID(famPanTaoYan, 4)), 0)
	front := newUnit(fu(1, 10, 1000, 1, levelID(famDunRang, 10)), 0)
	foe := newUnit(fu(101, 10, 1000, 1), 1)
	b := faqBattle(eight, six, seven, front, foe)
	if !b.leiying(foe, front, 5000, true) {
		t.Fatal("lightning protection did not activate")
	}
	if six.front() {
		t.Fatal("protector swapped in while enter-talented units were available")
	}
	if !seven.front() || eight.front() {
		t.Fatalf("want slot 7 in by slot order, got 7=%v 8=%v", seven.front(), eight.front())
	}
}

// TestLeiyingSwapPrefersDunrang：没有入阵护法时，优先换有[遁让]的后阵，而不是普通后阵。
func TestLeiyingSwapPrefersDunrang(t *testing.T) {
	lei := newUnit(fu(6, 10, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	plain := newUnit(fu(7, 10, 1000, 1), 0)
	dr := newUnit(fu(8, 10, 1000, 1, levelID(famDunRang, 10)), 0)
	front := newUnit(fu(1, 10, 1000, 1, levelID(famDunRang, 10)), 0)
	foe := newUnit(fu(101, 10, 1000, 1), 1)
	b := faqBattle(lei, plain, dr, front, foe)
	if !b.leiying(foe, front, 5000, true) {
		t.Fatal("lightning protection did not activate")
	}
	if !dr.front() || plain.front() {
		t.Fatalf("want 遁让 bench unit in, got dr=%v plain=%v", dr.front(), plain.front())
	}
}

// TestLeiyingSwapProtectorLastResort：没有其他存活后阵时，才把雷震子换上前阵。
func TestLeiyingSwapProtectorLastResort(t *testing.T) {
	lei := newUnit(fu(6, 10, 100000, 1, levelID(famLeiYingBiHu, 4)), 0)
	front := newUnit(fu(1, 10, 1000, 1, levelID(famDunRang, 10)), 0)
	foe := newUnit(fu(101, 10, 1000, 1), 1)
	b := faqBattle(lei, front, foe)
	if !b.leiying(foe, front, 5000, true) {
		t.Fatal("lightning protection did not activate")
	}
	if !lei.front() || front.front() {
		t.Fatalf("protector should come in as last resort, lei=%v front=%v", lei.front(), front.front())
	}
}
