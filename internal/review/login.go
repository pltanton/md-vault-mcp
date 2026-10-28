package review

import (
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func localPath(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "/\\")
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	next := r.URL.Query().Get("next")
	if !localPath(next) {
		next = "/"
	}
	_, oc, err := s.oidcClient()
	if err != nil {
		s.message(w, http.StatusBadGateway, viewer{}, "Sign-in unavailable", "Kanidm does not answer: "+err.Error())
		return
	}
	f := flow{Kind: "flow", State: randomHex(16), Nonce: randomHex(16), Verifier: oauth2.GenerateVerifier(),
		Next: next, Exp: s.now().Add(flowTTL).Unix()}
	s.setCookie(w, flowCookie, s.seal(f), flowTTL)
	http.Redirect(w, r, oc.AuthCodeURL(f.State, oauth2.S256ChallengeOption(f.Verifier), oidc.Nonce(f.Nonce)), http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var f flow
	c, err := r.Cookie(flowCookie)
	if err != nil || !s.open(c.Value, &f) || f.Kind != "flow" || f.Exp < s.now().Unix() || q.Get("state") != f.State {
		s.message(w, http.StatusBadRequest, viewer{}, "Sign-in failed", "The sign-in link expired. Open the page again.")
		return
	}
	s.clearCookie(w, flowCookie)
	if e := q.Get("error"); e != "" {
		s.message(w, http.StatusForbidden, viewer{}, "Sign-in failed", "Kanidm refused: "+e)
		return
	}
	provider, oc, err := s.oidcClient()
	if err != nil {
		s.message(w, http.StatusBadGateway, viewer{}, "Sign-in unavailable", "Kanidm does not answer: "+err.Error())
		return
	}
	tok, err := oc.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(f.Verifier))
	if err != nil {
		s.message(w, http.StatusBadGateway, viewer{}, "Sign-in failed", "Kanidm gave no token: "+err.Error())
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := provider.Verifier(&oidc.Config{ClientID: s.cfg.ClientID}).Verify(r.Context(), raw)
	if err != nil || idt.Nonce != f.Nonce {
		s.message(w, http.StatusForbidden, viewer{}, "Sign-in failed", "The Kanidm token did not verify.")
		return
	}
	var claims struct {
		User   string   `json:"preferred_username"`
		Groups []string `json:"groups"`
	}
	if err := idt.Claims(&claims); err != nil {
		s.message(w, http.StatusForbidden, viewer{}, "Sign-in failed", "The Kanidm token cannot be read.")
		return
	}
	if len(claims.Groups) == 0 {
		if ui, err := provider.UserInfo(r.Context(), oauth2.StaticTokenSource(tok)); err == nil {
			var extra struct {
				Groups []string `json:"groups"`
			}
			if ui.Claims(&extra) == nil {
				claims.Groups = extra.Groups
			}
		}
	}
	if claims.User == "" {
		claims.User = idt.Subject
	}
	if !s.allowed(claims.Groups) {
		s.message(w, http.StatusForbidden, viewer{}, "No access", "The account "+claims.User+" has no access to this vault.")
		return
	}
	s.setCookie(w, sessionCookie, s.seal(session{Kind: "session", User: claims.User, Groups: claims.Groups,
		Exp: s.now().Add(sessionTTL).Unix()}), sessionTTL)
	http.Redirect(w, r, f.Next, http.StatusSeeOther)
}

func (s *Server) allowed(groups []string) bool {
	for _, g := range groups {
		short, _, _ := strings.Cut(g, "@")
		for _, a := range s.cfg.AllowedGroups {
			if short == a {
				return true
			}
		}
	}
	return false
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, v viewer) {
	s.clearCookie(w, sessionCookie)
	s.message(w, http.StatusOK, viewer{}, "Signed out", "To come back, open a proposal link again.")
}
