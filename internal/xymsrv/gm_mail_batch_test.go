package xymsrv

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGMSelectedMailRejectsInvalidRecipientsAndDeduplicates(t *testing.T) {
	srv := gmHarness(t)
	handler := srv.AdminHandler()
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mail", strings.NewReader(body)))
		return w
	}
	for _, body := range []string{
		`{"to":0,"uids":[20001],"title":"test","gold":1}`,
		`{"to":-1,"title":"test","gold":1}`,
		`{"to":-1,"uids":[20001,999999],"title":"test","gold":1}`,
	} {
		if w := post(body); w.Code != 400 {
			t.Fatalf("invalid recipient list returned %d: %s", w.Code, w.Body.String())
		}
	}
	if len(srv.social.Mails[20001]) != 0 {
		t.Fatal("invalid batch delivered a partial result")
	}
	w := post(`{"to":-1,"uids":[20001,20002,20001],"title":"test","gold":1}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"sent":2`) {
		t.Fatal(w.Body.String())
	}
	reloaded := openSocial(srv.players.st)
	if len(reloaded.Mails[20001]) != 1 || len(reloaded.Mails[20002]) != 1 {
		t.Fatal("selected mail was not persisted exactly once for each recipient")
	}
}
