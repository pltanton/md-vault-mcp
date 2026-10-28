package notes

import (
	"fmt"
	"strings"
	"testing"
)

func TestLinkRefs(t *testing.T) {
	lines := []string{
		"See [[Мама]] and ![[Люди/Мама#ДР|день]] and [мама](Люди/Мама.md#ДР).",
		"`[[Code]]` and [site](https://x.org) and ![img](a.png) and [[ ]]",
		"```",
		"[[Fenced]]",
		"```",
		"[esc](../Люди/Мама%20Ира.md)",
	}
	var got []string
	for _, r := range linkRefs(lines) {
		got = append(got, fmt.Sprintf("%d %v %v %s %s", r.line, r.embed, r.md, r.target, r.suffix))
	}
	want := []string{
		"0 false false Мама ",
		"0 true false Люди/Мама #ДР|день",
		"0 false true Люди/Мама.md #ДР",
		"5 false true ../Люди/Мама Ира.md ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s", strings.Join(got, "\n"))
	}
}

func TestResolveAndRewrite(t *testing.T) {
	ix := newNoteIndex([]string{"Люди/Мама.md", "Дом/Мама.md", "Люди/Папа.md", "Inbox/Заметка.md", "Корень.md"})
	from := "Inbox/Заметка.md"
	ref := func(s string) linkRef { return linkRefs([]string{s})[0] }
	for _, c := range []struct{ link, resolved string }{
		{"[[Папа]]", "Люди/Папа.md"},
		{"[[Мама]]", ""},
		{"[[Люди/Мама]]", "Люди/Мама.md"},
		{"[x](../Люди/Папа.md)", "Люди/Папа.md"},
		{"[x](Люди/Папа.md)", "Люди/Папа.md"},
		{"[[Нет такой]]", ""},
	} {
		if got := ix.resolve(from, ref(c.link)); got != c.resolved {
			t.Errorf("%s resolved to %q, want %q", c.link, got, c.resolved)
		}
	}
	for _, c := range []struct{ link, newPath, want string }{
		{"[[Папа]]", "Люди/Отец.md", "[[Отец]]"},
		{"[[Папа#ДР|папа]]", "Люди/Отец.md", "[[Отец#ДР|папа]]"},
		{"![[Папа]]", "Архив/Папа.md", "![[Папа]]"},
		{"[[Папа]]", "Дом/Мама.md", "[[Дом/Мама]]"},
		{"[[Люди/Папа]]", "Архив/Папа.md", "[[Архив/Папа]]"},
		{"[[Люди/Папа.md]]", "Архив/Папа.md", "[[Архив/Папа.md]]"},
		{"[x](../Люди/Папа.md#a)", "Архив/Старое/Папа.md", "[x](../Архив/Старое/Папа.md#a)"},
		{"[x](Люди/Папа.md)", "Архив/Папа.md", "[x](Архив/Папа.md)"},
		{"[x](../Люди/Папа.md)", "Inbox/Папа Ира.md", "[x](Папа Ира.md)"},
		{"[x](../Люди/%D0%9F%D0%B0%D0%BF%D0%B0.md)", "Архив/Папа Ира.md", "[x](../Архив/Папа%20Ира.md)"},
	} {
		if got := ix.rewrite(from, ref(c.link), "Люди/Папа.md", c.newPath); got != c.want {
			t.Errorf("%s → %s: got %s, want %s", c.link, c.newPath, got, c.want)
		}
	}
}
