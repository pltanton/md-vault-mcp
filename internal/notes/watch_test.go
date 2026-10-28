package notes

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func (f *fixture) waitFor(query, want string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := f.svc.Search(context.Background(), query, "", 0)
		if err == nil && matchKeys(out) == want {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("search %q: got %s (%v), want %s", query, matchKeys(out), err, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWatcherKeepsIndexCurrent(t *testing.T) {
	f := newIndexedFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := f.svc.Watch(ctx); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(f.vault, "00. Inbox", "Новое.md")

	f.write("00. Inbox/Новое.md", "варежки\n")
	f.waitFor("варежки", "00. Inbox/Новое.md:1")

	tmp := filepath.Join(f.vault, "00. Inbox", ".syncthing.Новое.md.tmp")
	if err := os.WriteFile(tmp, []byte("перчатки\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, note); err != nil {
		t.Fatal(err)
	}
	f.waitFor("перчатки", "00. Inbox/Новое.md:1")
	f.waitFor("варежки", "")

	f.write("00. Inbox/Глубже/Ещё/Заметка.md", "носки\n")
	f.waitFor("носки", "00. Inbox/Глубже/Ещё/Заметка.md:1")

	if err := os.Remove(note); err != nil {
		t.Fatal(err)
	}
	f.waitFor("перчатки", "")

	if err := os.RemoveAll(filepath.Join(f.vault, "00. Inbox", "Глубже")); err != nil {
		t.Fatal(err)
	}
	f.waitFor("носки", "")
}
