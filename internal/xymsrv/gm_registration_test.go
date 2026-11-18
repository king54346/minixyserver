package xymsrv

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"turnrpg/internal/store"
)

func TestRegistrationPersistentGateAndExistingLogin(t *testing.T) {
	st := store.OpenFile(t.TempDir())
	a, err := OpenAccounts(st)
	if err != nil {
		t.Fatal(err)
	}
	if a.registrationSettings().Disabled {
		t.Fatal("must default open")
	}
	old, err := a.Register("existing", "password", "")
	if err != nil {
		t.Fatal(err)
	}
	before := a.NextUID
	if err := a.setRegistration(RegistrationSettings{Version: 1, Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Register("new", "password", ""); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatal(err)
	}
	if a.NextUID != before {
		t.Fatal("closed registration consumed UID")
	}
	if got, err := a.Login("existing", "password", ""); err != nil || got.UID != old.UID {
		t.Fatal("existing login blocked", err)
	}
	b, err := OpenAccounts(st)
	if err != nil || !b.registrationSettings().Disabled {
		t.Fatal("gate did not survive restart", err)
	}
	if err := b.setRegistration(RegistrationSettings{Version: 1}); !errors.Is(err, errRegistrationVersion) {
		t.Fatal("stale version accepted", err)
	}
	if err := b.setRegistration(RegistrationSettings{Version: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Register("new", "password", ""); err != nil {
		t.Fatal("reopen failed", err)
	}
}

func TestRegistrationHTTP(t *testing.T) {
	srv := gmHarness(t)
	h := srv.AdminHandler()
	call := func(method string, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/gm/registration", bytes.NewBufferString(body)))
		return w
	}
	w := call("GET", "")
	var c RegistrationSettings
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &c) != nil || c.Disabled {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{"version":1}`, `{"version":1,"disabled":null}`, `{"version":1,"disabled":true,"bad":0}`, `{"version":1,"disabled":true} {}`} {
		if w = call("POST", body); w.Code != 400 {
			t.Fatal(body, w.Code)
		}
	}
	if w = call("POST", `{"version":1,"disabled":true}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call("POST", `{"version":1,"disabled":false}`); w.Code != 409 {
		t.Fatal("stale request", w.Code)
	}
	if w = call("GET", ""); json.Unmarshal(w.Body.Bytes(), &c) != nil || !c.Disabled || c.Version != 2 {
		t.Fatal(w.Body.String())
	}
}
