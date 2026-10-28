package review

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/notes"
)

type fakeQueue struct {
	others            map[string]index.Proposal
	p                 index.Proposal
	applied, rejected string
	by, comment       string
}

func (q *fakeQueue) ListProposals(context.Context) ([]index.Proposal, []index.Proposal, error) {
	return []index.Proposal{q.p}, nil, nil
}

func (q *fakeQueue) GetProposal(_ context.Context, id string) (index.Proposal, bool, error) {
	if p, ok := q.others[id]; ok {
		return p, true, nil
	}
	return q.p, id == q.p.ID, nil
}

func (q *fakeQueue) ProposalNote(index.Proposal) (string, bool) {
	return "# Старое\nтекст", true
}

func (q *fakeQueue) PreviewProposal(index.Proposal) []notes.EditPreview {
	return []notes.EditPreview{{Line: 14, Before: []string{"## Мама"}, Old: []string{"- ДР: 12 марта"}, New: []string{"- ДР: 21 марта"}}}
}

func (q *fakeQueue) ApplyProposal(_ context.Context, id, by, comment string) (index.Proposal, error) {
	q.applied, q.by, q.comment, q.p.Status = id, by, comment, "applied"
	return q.p, nil
}

func (q *fakeQueue) RejectProposal(_ context.Context, id, by, comment string) (index.Proposal, error) {
	q.rejected, q.by, q.comment, q.p.Status = id, by, comment, "rejected"
	return q.p, nil
}

type browser struct {
	t   *testing.T
	h   http.Handler
	jar map[string]string
}

func (b *browser) do(method, target string, form url.Values) (*http.Response, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range b.jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	rec := httptest.NewRecorder()
	b.h.ServeHTTP(rec, req)
	res := rec.Result()
	for _, c := range res.Cookies() {
		if c.MaxAge < 0 {
			delete(b.jar, c.Name)
		} else {
			b.jar[c.Name] = c.Value
		}
	}
	out, _ := io.ReadAll(res.Body)
	return res, string(out)
}

func (b *browser) login(idp *fakeIDP, next string) *http.Response {
	b.t.Helper()
	res, _ := b.do("GET", "/login?next="+url.QueryEscape(next), nil)
	loc, err := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || err != nil || !strings.HasPrefix(loc.String(), idp.url+"/authorize") {
		b.t.Fatalf("login redirect %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	q := loc.Query()
	if q.Get("code_challenge_method") != "S256" {
		b.t.Fatalf("no PKCE: %s", loc)
	}
	idp.nonce, idp.challenge = q.Get("nonce"), q.Get("code_challenge")
	res, _ = b.do("GET", "/callback?code=good&state="+url.QueryEscape(q.Get("state")), nil)
	return res
}

func setup(t *testing.T, groups ...string) (*browser, *fakeIDP, *fakeQueue) {
	t.Helper()
	idp := newFakeIDP(t, "family-notes")
	q := &fakeQueue{p: index.Proposal{ID: "p1", Kind: "edit", Path: "Дни рождения.md", Status: "pending", Reason: "в чате сказали 21-е"}}
	if len(groups) == 0 {
		groups = []string{"admins", "family"}
	}
	s := New(Config{PublicURL: "http://review.test", AllowedGroups: groups, Issuer: idp.url, ClientID: "family-notes",
		ClientSecret: "secret", Key: []byte("0123456789abcdef0123456789abcdef")}, q)
	return &browser{t: t, h: s.Handler(), jar: map[string]string{}}, idp, q
}

func TestUnauthenticatedGoesToLogin(t *testing.T) {
	b, _, _ := setup(t)
	res, _ := b.do("GET", "/p/p1", nil)
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login?next=%2Fp%2Fp1" {
		t.Errorf("got %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, _ := b.do("POST", "/p/p1/apply", url.Values{}); res.StatusCode != http.StatusForbidden {
		t.Errorf("post without session: %d", res.StatusCode)
	}
}

func TestLoginLandsOnNext(t *testing.T) {
	b, idp, _ := setup(t)
	res := b.login(idp, "/p/p1")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/p/p1" || b.jar[sessionCookie] == "" || b.jar[flowCookie] != "" {
		t.Fatalf("callback %d %s jar %v", res.StatusCode, res.Header.Get("Location"), b.jar)
	}
}

func TestLoginAcceptsShortAndQualifiedGroups(t *testing.T) {
	for _, g := range []string{"family", "family@home.id.test"} {
		b, idp, _ := setup(t)
		idp.user, idp.groups = "julsa", []string{g}
		if res := b.login(idp, "/"); res.StatusCode != http.StatusSeeOther {
			t.Errorf("%s: %d", g, res.StatusCode)
		}
	}
}

func TestLoginRefusesOtherGroups(t *testing.T) {
	b, idp, _ := setup(t, "admins")
	idp.user, idp.groups = "julsa", []string{"family@home.id.test"}
	res := b.login(idp, "/")
	if res.StatusCode != http.StatusForbidden || b.jar[sessionCookie] != "" {
		t.Errorf("got %d jar %v", res.StatusCode, b.jar)
	}
}

func TestLoginRedirectStaysLocal(t *testing.T) {
	for _, next := range []string{"//evil.example/x", "https://evil.example", "/\\evil.example"} {
		b, idp, _ := setup(t)
		if res := b.login(idp, next); res.Header.Get("Location") != "/" {
			t.Errorf("%s: redirected to %s", next, res.Header.Get("Location"))
		}
	}
}

func TestCallbackNeedsLoginCookie(t *testing.T) {
	b, _, _ := setup(t)
	if res, _ := b.do("GET", "/callback?code=good&state=x", nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d", res.StatusCode)
	}
}

func TestLoginWithoutProvider(t *testing.T) {
	s := New(Config{PublicURL: "http://review.test", AllowedGroups: []string{"admins"}, Issuer: "http://127.0.0.1:1",
		ClientID: "c", Key: []byte("0123456789abcdef0123456789abcdef")}, &fakeQueue{})
	b := &browser{t: t, h: s.Handler(), jar: map[string]string{}}
	if res, body := b.do("GET", "/login", nil); res.StatusCode != http.StatusBadGateway || !strings.Contains(body, "Kanidm") {
		t.Errorf("got %d %s", res.StatusCode, body)
	}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func TestReviewAndApply(t *testing.T) {
	b, idp, q := setup(t)
	b.login(idp, "/")
	res, body := b.do("GET", "/", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, `href="/p/p1"`) || !strings.Contains(body, "Дни рождения.md") {
		t.Fatalf("list %d\n%s", res.StatusCode, body)
	}
	res, body = b.do("GET", "/p/p1", nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<del>12</del><ins>21</ins>") || !strings.Contains(body, "<li>ДР: 21 марта</li>") || !strings.Contains(body, "в чате сказали 21-е") {
		t.Fatalf("proposal %d\n%s", res.StatusCode, body)
	}
	m := csrfField.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no csrf field")
	}
	if res, _ := b.do("POST", "/p/p1/apply", url.Values{"comment": {"ок"}}); res.StatusCode != http.StatusForbidden || q.applied != "" {
		t.Errorf("apply without csrf: %d", res.StatusCode)
	}
	res, _ = b.do("POST", "/p/p1/apply", url.Values{"csrf": {m[1]}, "comment": {" ок "}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/p/p1" || q.applied != "p1" || q.by != "anton" || q.comment != "ок" {
		t.Errorf("apply %d %s %+v", res.StatusCode, res.Header.Get("Location"), q)
	}
	if res, body := b.do("GET", "/p/p1", nil); !strings.Contains(body, "applied") || strings.Contains(body, `name="comment"`) {
		t.Errorf("decided page %d\n%s", res.StatusCode, body)
	}
}

func TestReject(t *testing.T) {
	b, idp, q := setup(t)
	idp.user, idp.groups = "julsa", []string{"family"}
	b.login(idp, "/")
	_, body := b.do("GET", "/p/p1", nil)
	m := csrfField.FindStringSubmatch(body)
	b.do("POST", "/p/p1/reject", url.Values{"csrf": {m[1]}, "comment": {"не надо"}})
	if q.rejected != "p1" || q.by != "julsa" || q.comment != "не надо" {
		t.Errorf("got %+v", q)
	}
}

func TestUnknownProposal(t *testing.T) {
	b, idp, _ := setup(t)
	b.login(idp, "/")
	if res, _ := b.do("GET", "/p/nope", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("got %d", res.StatusCode)
	}
}

func TestLogout(t *testing.T) {
	b, idp, _ := setup(t)
	b.login(idp, "/")
	_, body := b.do("GET", "/", nil)
	m := csrfField.FindStringSubmatch(body)
	b.do("POST", "/logout", url.Values{"csrf": {m[1]}})
	if res, _ := b.do("GET", "/", nil); res.StatusCode != http.StatusSeeOther {
		t.Errorf("still signed in: %d", res.StatusCode)
	}
}

func TestDeleteAndMovePages(t *testing.T) {
	b, idp, q := setup(t)
	q.others = map[string]index.Proposal{
		"del": {ID: "del", Kind: "delete", Path: "Старое.md", Status: "pending", Reason: "дубль",
			LinkEdits: []byte(`{"linked":["Tasks.md"]}`)},
		"mv": {ID: "mv", Kind: "move", Path: "Старое.md", NewPath: "Архив/Старое.md", Status: "pending", Reason: "в архив",
			LinkEdits: []byte(`{"notes":[{"path":"Tasks.md","edits":[{"old":"[[Старое]]","new":"[[Архив/Старое]]"}]}],"unwritable":["Work.md"]}`)},
	}
	b.login(idp, "/")
	_, body := b.do("GET", "/p/del", nil)
	for _, want := range []string{"Delete this note", "<li>Tasks.md</li>", "<h1>Старое</h1>"} {
		if !strings.Contains(body, want) {
			t.Errorf("delete page lacks %q\n%s", want, body)
		}
	}
	_, body = b.do("GET", "/p/mv", nil)
	for _, want := range []string{"Архив/Старое.md", "<del>[[Старое]]</del>", "<ins>[[Архив/Старое]]</ins>", "<li>Work.md</li>"} {
		if !strings.Contains(body, want) {
			t.Errorf("move page lacks %q\n%s", want, body)
		}
	}
}
