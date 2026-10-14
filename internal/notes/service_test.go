package notes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pltanton/md-vault-mcp/internal/access"
)

const giftNote = `---
tags: [person]
---
# Alice

## Идеи для подарков
- Книга
- Шарф

## Даты
- ДР: 12 марта
`

type fixture struct {
	t     *testing.T
	vault string
	state string
	svc   *Service
	clock time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	base := t.TempDir()
	f := &fixture{t: t, vault: filepath.Join(base, "vault"), state: filepath.Join(base, "state"), clock: time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)}
	f.write("02. Areas/Personal/People/Alice.md", giftNote)
	f.write("00. Inbox/Покупки.md", "# Купить\n- [ ] молоко\n")
	f.write("02. Areas/Work/Job.md", "секретный подарок\n")
	f.write("03. Resources/Work/Syncs/s.md", "подарок\n")
	f.write("Tasks.md", "корневая заметка про подарок\n")
	f.write(".obsidian/workspace.md", "подарок\n")
	f.write("01. Projects/p.md", "подарок\n")

	rules := access.RuleSet{
		Allow: []string{"00. Inbox/**", "02. Areas/Personal/**", "*.md"},
		Deny:  []string{"**/Work/**"},
	}
	svc, err := NewService(Config{Vault: f.vault, StateDir: f.state, Read: rules, Write: rules})
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return f.clock }
	f.svc = svc
	return f
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.vault, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(rel string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.vault, filepath.FromSlash(rel)))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

const julia = "02. Areas/Personal/People/Alice.md"

func TestReadNumbersLines(t *testing.T) {
	f := newFixture(t)
	out, err := f.svc.Read("00. Inbox/Покупки.md", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "1\t# Купить\n2\t- [ ] молоко\n" || out.TotalLines != 2 {
		t.Errorf("got %+v", out)
	}
	out, _ = f.svc.Read(julia, 6, 2)
	if out.Content != "6\t## Идеи для подарков\n7\t- Книга\n" {
		t.Errorf("offset/limit: %q", out.Content)
	}
	for _, denied := range []string{"02. Areas/Work/Job.md", "../vault/Tasks.md", ".obsidian/workspace.md", "01. Projects/p.md"} {
		if _, err := f.svc.Read(denied, 0, 0); err == nil {
			t.Errorf("read %s", denied)
		}
	}
}

func TestSearchAndListShowOnlyReadable(t *testing.T) {
	f := newFixture(t)
	out, err := f.svc.Search("ПОДАР", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, m := range out.Matches {
		paths = append(paths, m.Path)
	}
	if got := strings.Join(paths, "|"); got != julia+"|Tasks.md" {
		t.Errorf("search hit %s", got)
	}

	out, _ = f.svc.Search("alice", "", 0)
	if len(out.Matches) == 0 || out.Matches[0].Path != julia || out.Matches[0].Line != 0 {
		t.Errorf("file name match: %+v", out.Matches)
	}

	if _, err := f.svc.Search("x", "02. Areas/Work", 0); err == nil {
		t.Error("search under a denied dir")
	}

	root, err := f.svc.List("")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range root.Entries {
		names = append(names, e.Kind+":"+e.Name)
	}
	if got := strings.Join(names, " "); got != "dir:00. Inbox dir:02. Areas note:Tasks.md" {
		t.Errorf("root listing: %s", got)
	}
	areas, _ := f.svc.List("02. Areas")
	if len(areas.Entries) != 1 || areas.Entries[0].Name != "Personal" {
		t.Errorf("areas listing leaks: %+v", areas.Entries)
	}
	for _, dir := range []string{"02. Areas/Work", "03. Resources", "01. Projects", ".obsidian", "nope"} {
		if _, err := f.svc.List(dir); err == nil {
			t.Errorf("listed %s", dir)
		}
	}
}

func TestAddUnderHeading(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Add(julia, "- Духи\n- Плед", "## Идеи для подарков")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(giftNote, "- Шарф\n", "- Шарф\n- Духи\n- Плед\n", 1)
	if got := f.read(julia); got != want {
		t.Errorf("middle section:\n%s", got)
	}
	if res.Line != 9 || res.Lines != 2 || res.ID == "" || !res.ExpiresAt.Equal(f.clock.Add(48*time.Hour)) {
		t.Errorf("result %+v", res)
	}

	if _, err := f.svc.Add(julia, "- Годовщина: 1 июня", "Даты"); err != nil {
		t.Fatal(err)
	}
	if got := f.read(julia); !strings.HasSuffix(got, "- ДР: 12 марта\n- Годовщина: 1 июня\n") {
		t.Errorf("last section, bare heading text:\n%s", got)
	}

	if _, err := f.svc.Add(julia, "Заметка в конце раздела Alice", "# Alice"); err != nil {
		t.Fatal(err)
	}
	if got := f.read(julia); !strings.HasSuffix(got, "- Годовщина: 1 июня\nЗаметка в конце раздела Alice\n") {
		t.Errorf("h1 section spans its subsections:\n%s", got)
	}

	if _, err := f.svc.Add(julia, "x", "## Нет такого"); err == nil {
		t.Error("unknown heading accepted")
	}
}

func TestAddEdges(t *testing.T) {
	f := newFixture(t)
	f.write("00. Inbox/sections.md", "# A\n\n# B\n- b\n\n\n# C\n```\n# not a heading\n```\n")
	if _, err := f.svc.Add("00. Inbox/sections.md", "- a", "# A"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Add("00. Inbox/sections.md", "- b2", "# B"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Add("00. Inbox/sections.md", "x", "# not a heading"); err == nil {
		t.Error("a heading inside a code fence matched")
	}
	want := "# A\n- a\n\n# B\n- b\n- b2\n\n\n# C\n```\n# not a heading\n```\n"
	if got := f.read("00. Inbox/sections.md"); got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}

	f.write("00. Inbox/crlf.md", "line one\r\nline two")
	if _, err := f.svc.Add("00. Inbox/crlf.md", "\n\nline three\n\n", ""); err != nil {
		t.Fatal(err)
	}
	if got := f.read("00. Inbox/crlf.md"); got != "line one\r\nline two\r\nline three" {
		t.Errorf("CRLF and missing final newline not kept: %q", got)
	}

	f.write("00. Inbox/dup.md", "# H\n- a\n# H\n- b\n")
	if _, err := f.svc.Add("00. Inbox/dup.md", "x", "# H"); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Errorf("duplicate heading: %v", err)
	}

	if _, err := f.svc.Add("00. Inbox/missing.md", "x", ""); err == nil {
		t.Error("added to a missing note")
	}
	if _, err := f.svc.Add("02. Areas/Work/Job.md", "x", ""); err == nil {
		t.Error("added to a denied note")
	}
	if _, err := f.svc.Add("00. Inbox/Покупки.md", " \n ", ""); err == nil {
		t.Error("added blank text")
	}
}

func TestWritesKeepMode(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.vault, "00. Inbox", "Покупки.md")
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Add("00. Inbox/Покупки.md", "- [ ] хлеб", "# Купить"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	leftovers, _ := filepath.Glob(filepath.Join(f.vault, "00. Inbox", ".*.tmp"))
	if len(leftovers) != 0 {
		t.Errorf("temp files left: %v", leftovers)
	}
}

func TestSizeLimit(t *testing.T) {
	f := newFixture(t)
	f.svc.maxSize = 64
	f.write("00. Inbox/big.md", strings.Repeat("x", 65))
	if _, err := f.svc.Read("00. Inbox/big.md", 0, 0); err == nil {
		t.Error("read an oversized note")
	}
	if _, err := f.svc.Add("00. Inbox/Покупки.md", strings.Repeat("y", 64), ""); err == nil {
		t.Error("grew a note past the limit")
	}
}

func TestCreate(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Create("00. Inbox/Новая/Идея.md", "# Идея\n\nтекст")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.read("00. Inbox/Новая/Идея.md"); got != "# Идея\n\nтекст\n" {
		t.Errorf("content %q", got)
	}
	if res.Lines != 3 {
		t.Errorf("result %+v", res)
	}
	if _, err := f.svc.Create("00. Inbox/Новая/Идея.md", "again"); err == nil {
		t.Error("overwrote an existing note")
	}
	for _, bad := range []string{"02. Areas/Work/x.md", "01. Projects/x.md", "00. Inbox/x.txt", "00. Inbox/../../x.md", ".obsidian/x.md"} {
		if _, err := f.svc.Create(bad, "x"); err == nil {
			t.Errorf("created %s", bad)
		}
	}
	if _, err := f.svc.Remove(res.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.vault, "00. Inbox", "Новая", "Идея.md")); !os.IsNotExist(err) {
		t.Errorf("removing the only block of a created note left the file: %v", err)
	}
}

func TestEditAndRemove(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Add(julia, "- Духи", "## Идеи для подарков")
	if err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(time.Hour)
	if _, err := f.svc.Edit(res.ID, "- Духи Chanel\n- Свеча"); err != nil {
		t.Fatal(err)
	}
	if got := f.read(julia); !strings.Contains(got, "- Шарф\n- Духи Chanel\n- Свеча\n\n## Даты") {
		t.Errorf("after edit:\n%s", got)
	}

	recent, err := f.svc.Recent(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Blocks) != 1 || recent.Blocks[0].Text != "- Духи Chanel\n- Свеча" || recent.Blocks[0].Heading != "## Идеи для подарков" {
		t.Errorf("recent %+v", recent.Blocks)
	}

	if _, err := f.svc.Remove(res.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.read(julia); got != giftNote {
		t.Errorf("remove did not restore the note:\n%s", got)
	}
	if _, err := f.svc.Remove(res.ID); err == nil {
		t.Error("removed the same block twice")
	}
}

func TestOwnerChangesWin(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Add(julia, "- Духи", "## Идеи для подарков")
	if err != nil {
		t.Fatal(err)
	}
	f.write(julia, strings.Replace(f.read(julia), "- Духи", "- Духи, но не сладкие", 1))
	_, err = f.svc.Edit(res.ID, "- другое")
	if err == nil || !errors.Is(err, errOwnerChanged) {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(f.read(julia), "- Духи, но не сладкие") {
		t.Error("the owner's text was touched")
	}
	if recent, _ := f.svc.Recent(0); len(recent.Blocks) != 0 {
		t.Errorf("owner-changed block still offered: %+v", recent.Blocks)
	}
}

func TestDuplicateBlockRefused(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Add("00. Inbox/Покупки.md", "- [ ] хлеб", "")
	if err != nil {
		t.Fatal(err)
	}
	f.write("00. Inbox/Покупки.md", f.read("00. Inbox/Покупки.md")+"- [ ] хлеб\n")
	if _, err := f.svc.Remove(res.ID); err == nil || !strings.Contains(err.Error(), "2 times") {
		t.Fatalf("got %v", err)
	}
}

func TestDuplicateResolvedByHeading(t *testing.T) {
	f := newFixture(t)
	f.write("00. Inbox/two.md", "# A\n- x\n\n# B\n")
	res, err := f.svc.Add("00. Inbox/two.md", "- x", "# B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Remove(res.ID); err != nil {
		t.Fatal(err)
	}
	if got := f.read("00. Inbox/two.md"); got != "# A\n- x\n\n# B\n" {
		t.Errorf("removed the wrong copy: %q", got)
	}
}

func TestEditWindowCountsFromCreation(t *testing.T) {
	f := newFixture(t)
	res, err := f.svc.Add("00. Inbox/Покупки.md", "- [ ] хлеб", "")
	if err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(47 * time.Hour)
	if _, err := f.svc.Edit(res.ID, "- [ ] батон"); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	if _, err := f.svc.Edit(res.ID, "- [ ] багет"); err == nil {
		t.Error("edited after the window, extended by the previous edit")
	}
	if recent, _ := f.svc.Recent(0); len(recent.Blocks) != 0 {
		t.Errorf("expired block offered: %+v", recent.Blocks)
	}
}

func TestJournalHasNoText(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Add(julia, "- Секретный подарок", "## Идеи для подарков"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Create("00. Inbox/Тайна.md", "совсем тайный текст"); err != nil {
		t.Fatal(err)
	}
	filepath.Walk(f.state, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		data, _ := os.ReadFile(p)
		for _, secret := range []string{"Секретный", "подарок", "тайный"} {
			if strings.Contains(string(data), secret) {
				t.Errorf("%s contains %q", filepath.Base(p), secret)
			}
		}
		return nil
	})
	j, _ := f.svc.journal.view()
	if len(j) != 2 || j[0].Hash == "" || j[0].Hash == f.svc.journal.hash([]string{"x"}) {
		t.Errorf("journal %+v", j)
	}
}

func TestHTTPNeedsToken(t *testing.T) {
	f := newFixture(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "md-vault", Version: "test"}, nil)
	f.svc.Register(server)
	const token = "0123456789abcdef0123"
	ts := httptest.NewServer(NewHTTPHandler(server, token))
	t.Cleanup(ts.Close)

	for _, auth := range []string{"", "Bearer wrong", "Bearer " + token + "x", token} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Authorization %q: %s", auth, resp.Status)
		}
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{Transport: bearer{token: token}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 12 {
		t.Errorf("%d tools", len(tools.Tools))
	}
	// The Python MCP SDK 2.x rejects a bare `true` as a property schema.
	for _, tool := range tools.Tools {
		for kind, schema := range map[string]any{"input": tool.InputSchema, "output": tool.OutputSchema} {
			data, _ := json.Marshal(schema)
			var top struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			json.Unmarshal(data, &top)
			for name, sub := range top.Properties {
				if sub[0] != '{' {
					t.Errorf("%s %s schema: property %s is %s, not an object", tool.Name, kind, name, sub)
				}
			}
		}
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "note_read", Arguments: map[string]any{"path": "Tasks.md"}})
	if err != nil || res.IsError {
		t.Fatalf("note_read over HTTP: %v %+v", err, res)
	}
	res, _ = session.CallTool(ctx, &mcp.CallToolParams{Name: "note_read", Arguments: map[string]any{"path": "02. Areas/Work/Job.md"}})
	if !res.IsError {
		t.Error("denied note read over HTTP")
	}
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}
