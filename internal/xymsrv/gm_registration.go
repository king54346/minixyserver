package xymsrv

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type RegistrationSettings struct {
	Version  int64 `json:"version"`
	Disabled bool  `json:"disabled"`
}

func (a *Accounts) registrationSettings() RegistrationSettings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.registration
}

var errRegistrationVersion = errors.New("注册配置已被其他窗口修改，请重新读取")

func (a *Accounts) setRegistration(c RegistrationSettings) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c.Version != a.registration.Version {
		return errRegistrationVersion
	}
	c.Version++
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := a.st.SaveGlobal("registration_settings", raw); err != nil {
		return err
	}
	a.registration = c
	return nil
}

func (srv *Server) gmRegistrationHandlers(mux *http.ServeMux) {
	mux.HandleFunc("GET /gm/registration", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, srv.accounts.registrationSettings())
	})
	mux.HandleFunc("POST /gm/registration", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Version  int64 `json:"version"`
			Disabled *bool `json:"disabled"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil || req.Disabled == nil {
			writeJSON(w, 400, map[string]string{"error": "需要配置版本和禁止注册状态"})
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			writeJSON(w, 400, map[string]string{"error": "请求内容无效"})
			return
		}
		if err := srv.accounts.setRegistration(RegistrationSettings{Version: req.Version, Disabled: *req.Disabled}); err != nil {
			status := 500
			if errors.Is(err, errRegistrationVersion) {
				status = 409
			}
			writeJSON(w, status, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, srv.accounts.registrationSettings())
	})
}
