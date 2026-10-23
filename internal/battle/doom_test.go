package battle

import "testing"

// TestHuoshiToBench：带活尸的护法换回后阵直接死亡；没有活尸的照常回后阵。
func TestHuoshiToBench(t *testing.T) {
	u := newUnit(fu(1, 100, 1000, 1), 0)
	v := newUnit(fu(6, 100, 1000, 1), 0)
	o := newUnit(fu(101, 100, 1000, 1), 1)
	b := faqBattle(u, v, o)
	b.huoshi(u, 5401)
	u.doomIn = 5
	b.swap(u, v)
	if u.alive() || !v.alive() || v.slot() != 1 {
		t.Fatalf("huoshi alive %v, v alive %v slot %d", u.alive(), v.alive(), v.slot())
	}
	b.swap(v, u) // 没有活尸：回后阵活着
	if !v.alive() {
		t.Fatal("normal unit died on bench")
	}
	if _, err := DecodeRecord(b.w.b); err != nil {
		t.Fatal(err)
	}
}
