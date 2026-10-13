package access

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mustPolicy(t *testing.T, read, write RuleSet) *Policy {
	t.Helper()
	p, err := NewPolicy(read, write, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClean(t *testing.T) {
	ok := map[string]string{
		"":                    "",
		"a.md":                "a.md",
		"./00. Inbox//x.md":   "00. Inbox/x.md",
		"02. Areas/Personal/": "02. Areas/Personal",
		" 05. Daily/2026.md ": "05. Daily/2026.md",
	}
	for in, want := range ok {
		got, err := Clean(in)
		if err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"../x.md", "a/../../x.md", "a/..", "/etc/passwd", `a\b.md`, "a\x00.md"} {
		if _, err := Clean(bad); !errors.Is(err, ErrOutside) {
			t.Errorf("Clean(%q) = %v, want ErrOutside", bad, err)
		}
	}
}

func TestAllowlist(t *testing.T) {
	p := mustPolicy(t, RuleSet{
		Mode:  Allowlist,
		Allow: []string{"00. Inbox/", "02. Areas/Personal/**", "03. Resources/Travel", "*.md", "Single/one.md"},
		Deny:  []string{"**/Work/**", "02. Areas/Personal/Secret.md"},
	}, RuleSet{})

	cases := map[string]bool{
		"00. Inbox/x.md":                     true,
		"00. Inbox/deep/er/x.md":             true,
		"02. Areas/Personal/People/Alice.md": true,
		"03. Resources/Travel/ВНЖ.md":        true,
		"Tasks.md":                           true,
		"Single/one.md":                      true,
		"Single/two.md":                      false,
		"03. Resources/Garmin MAPS.md":       false,
		"02. Areas/Work/x.md":                false,
		"02. Areas/Personal/Work/x.md":       false,
		"00. Inbox/Work/x.md":                false,
		"02. Areas/Personal/Secret.md":       false,
		"02. Areas/Personal/Secret.md.bak":   false,
		"00. Inbox/picture.png":              false,
		"00. Inbox/.hidden.md":               false,
		".obsidian/workspace.md":             false,
		"00. Inbox/.trash/x.md":              false,
		"":                                   false,
	}
	for path, want := range cases {
		if got := p.CanRead(path); got != want {
			t.Errorf("CanRead(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDenylist(t *testing.T) {
	p := mustPolicy(t, RuleSet{Mode: Denylist, Deny: []string{"**/Work", "04. Archive/**", "Private.md"}}, RuleSet{})
	cases := map[string]bool{
		"anything/at/all.md":        true,
		"Tasks.md":                  true,
		"02. Areas/Work/x.md":       false,
		"Work/x.md":                 false,
		"03. Resources/Work/a/b.md": false,
		"04. Archive/old.md":        false,
		"Private.md":                false,
		"sub/Private.md":            true,
		".stversions/x.md":          false,
		".git/x.md":                 false,
	}
	for path, want := range cases {
		if got := p.CanRead(path); got != want {
			t.Errorf("CanRead(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestWriteIsWithinRead(t *testing.T) {
	p := mustPolicy(t,
		RuleSet{Allow: []string{"00. Inbox/**"}},
		RuleSet{Mode: Denylist, Deny: []string{"00. Inbox/Locked.md"}},
	)
	cases := map[string]bool{
		"00. Inbox/x.md":      true,
		"00. Inbox/Locked.md": false,
		"elsewhere.md":        false,
	}
	for path, want := range cases {
		if got := p.CanWrite(path); got != want {
			t.Errorf("CanWrite(%q) = %v, want %v", path, got, want)
		}
	}

	ro := mustPolicy(t, RuleSet{Allow: []string{"**"}}, RuleSet{})
	if ro.CanWrite("x.md") {
		t.Error("an empty write allowlist allowed a write")
	}
}

func TestMayHoldReadable(t *testing.T) {
	p := mustPolicy(t, RuleSet{
		Allow: []string{"02. Areas/Personal/**", "03. Resources/*/notes/**", "*.md"},
		Deny:  []string{"**/Work/**"},
	}, RuleSet{})
	cases := map[string]bool{
		"":                          true,
		"02. Areas":                 true,
		"02. Areas/Personal":        true,
		"02. Areas/Personal/deep":   true,
		"02. Areas/Work":            false,
		"02. Areas/Personal/Work":   false,
		"03. Resources":             true,
		"03. Resources/Any":         true,
		"03. Resources/Any/notes":   true,
		"03. Resources/Any/other":   false,
		"01. Projects":              false,
		".obsidian":                 false,
		"02. Areas/Personal/.trash": false,
	}
	for dir, want := range cases {
		if got := p.MayHoldReadable(dir); got != want {
			t.Errorf("MayHoldReadable(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestBadRules(t *testing.T) {
	bad := []RuleSet{
		{Mode: "maybe"},
		{Mode: Denylist, Allow: []string{"x/**"}},
		{Allow: []string{"/abs/**"}},
		{Allow: []string{"../up"}},
		{Allow: []string{"a//b"}},
		{Allow: []string{"[unclosed"}},
	}
	for _, rs := range bad {
		if _, err := NewPolicy(rs, RuleSet{}, nil); err == nil {
			t.Errorf("accepted %+v", rs)
		}
	}
}

func TestExtensions(t *testing.T) {
	p, err := NewPolicy(RuleSet{Allow: []string{"**"}}, RuleSet{}, []string{"md", ".TXT"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.CanRead("a.md") || !p.CanRead("a.txt") || !p.CanRead("A.MD") || p.CanRead("a.base") {
		t.Error("extension filter is off")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVaultSymlinks(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "vault")
	outside := filepath.Join(base, "outside")
	writeFile(t, filepath.Join(root, "00. Inbox", "ok.md"), "ok")
	writeFile(t, filepath.Join(root, "Work", "secret.md"), "secret")
	writeFile(t, filepath.Join(outside, "x.md"), "outside")
	links := map[string]string{
		"00. Inbox/escape.md": filepath.Join(outside, "x.md"),
		"00. Inbox/towork.md": filepath.Join(root, "Work", "secret.md"),
		"00. Inbox/inside.md": filepath.Join(root, "00. Inbox", "ok.md"),
		"00. Inbox/workdir":   filepath.Join(root, "Work"),
		"00. Inbox/outdir":    outside,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(name))); err != nil {
			t.Fatal(err)
		}
	}

	p := mustPolicy(t, RuleSet{Allow: []string{"00. Inbox/**"}}, RuleSet{Allow: []string{"00. Inbox/**"}})
	v, err := OpenVault(root, p)
	if err != nil {
		t.Fatal(err)
	}

	if _, rel, err := v.ResolveRead("00. Inbox/ok.md"); err != nil || rel != "00. Inbox/ok.md" {
		t.Errorf("plain file: %q %v", rel, err)
	}
	if _, _, err := v.ResolveRead("00. Inbox/inside.md"); err != nil {
		t.Errorf("symlink within allowed area: %v", err)
	}
	for _, name := range []string{"00. Inbox/escape.md", "00. Inbox/towork.md", "00. Inbox/workdir/secret.md", "00. Inbox/outdir/x.md"} {
		if _, _, err := v.ResolveRead(name); err == nil {
			t.Errorf("read through %s succeeded", name)
		}
		if _, _, err := v.ResolveWrite(name); err == nil {
			t.Errorf("write through %s succeeded", name)
		}
	}
	for _, name := range []string{"00. Inbox/workdir/new.md", "00. Inbox/outdir/new.md", "00. Inbox/outdir/sub/new.md"} {
		if _, _, err := v.ResolveCreate(name); err == nil {
			t.Errorf("create through %s succeeded", name)
		}
	}
	if real, _, err := v.ResolveCreate("00. Inbox/sub/new.md"); err != nil || real != filepath.Join(v.Root(), "00. Inbox", "sub", "new.md") {
		t.Errorf("create in a new subdir: %q %v", real, err)
	}
	if _, _, err := v.ResolveCreate("00. Inbox/ok.md"); err == nil {
		t.Error("create over an existing note succeeded")
	}
	if _, _, err := v.ResolveDir("00. Inbox/workdir"); err == nil {
		t.Error("a symlinked directory resolved")
	}
	if _, _, err := v.ResolveRead("../outside/x.md"); !errors.Is(err, ErrOutside) {
		t.Errorf("traversal: %v", err)
	}
	if _, _, err := v.ResolveRead("Work/secret.md"); !errors.Is(err, ErrNotReadable) {
		t.Errorf("denied file: %v", err)
	}
	if _, _, err := v.ResolveRead("00. Inbox/missing.md"); !errors.Is(err, ErrNotReadable) {
		t.Errorf("missing and denied should look the same, got %v", err)
	}
}
