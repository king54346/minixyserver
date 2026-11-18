package store

import (
	"path/filepath"
	"testing"
)

func TestRoleAndGlobalAtomic(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	role := Role{UID: 1, Gold: 10000, Name: "draft"}
	if err = st.SaveRole(role); err != nil {
		t.Fatal(err)
	}
	if err = st.SaveGlobal("weekly_draft", []byte(`{"generation":0}`)); err != nil {
		t.Fatal(err)
	}
	// Fail the second write, after the role debit has been executed.
	if err = st.db.Exec(`CREATE TRIGGER reject_draft BEFORE UPDATE ON globals BEGIN SELECT RAISE(ABORT, 'injected failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	role.Gold = 5000
	if err = st.SaveRoleAndGlobal(role, "weekly_draft", []byte(`{"generation":1}`)); err == nil {
		t.Fatal("expected failure")
	}
	roles, err := st.LoadRoles()
	if err != nil {
		t.Fatal(err)
	}
	data, err := st.LoadGlobal("weekly_draft")
	if err != nil {
		t.Fatal(err)
	}
	if roles[0].Gold != 10000 || string(data) != `{"generation":0}` {
		t.Fatalf("partial purchase: %+v %s", roles, data)
	}
	if err = st.db.Exec(`DROP TRIGGER reject_draft`).Error; err != nil {
		t.Fatal(err)
	}
	if err = st.SaveRoleAndGlobal(role, "weekly_draft", []byte(`{"generation":1}`)); err != nil {
		t.Fatal(err)
	}
	roles, _ = st.LoadRoles()
	data, _ = st.LoadGlobal("weekly_draft")
	if roles[0].Gold != 5000 || string(data) != `{"generation":1}` {
		t.Fatal("purchase missing")
	}
}
