package notes

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/pltanton/md-vault-mcp/internal/access"
)

const watchDebounce = 300 * time.Millisecond

// Watch subscribes to the vault and keeps the index current until ctx ends.
func (s *Service) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	if err := s.watchTree(w, s.vault.Root()); err != nil {
		w.Close()
		return err
	}
	go s.watchLoop(ctx, w)
	return nil
}

func (s *Service) watchTree(w *fsnotify.Watcher, top string) error {
	return filepath.WalkDir(top, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == top {
				return err
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		rel, ok := s.vaultRel(p)
		if !ok || rel != "" && !s.vault.Policy.MayHoldReadable(rel) {
			return filepath.SkipDir
		}
		return w.Add(p)
	})
}

func (s *Service) vaultRel(real string) (string, bool) {
	r, err := filepath.Rel(s.vault.Root(), real)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	if r == "." {
		return "", true
	}
	return filepath.ToSlash(r), true
}

// An empty path in the queue means a full Sync.
func (s *Service) watchLoop(ctx context.Context, w *fsnotify.Watcher) {
	defer w.Close()
	due := make(chan string)
	timers := map[string]*time.Timer{}
	schedule := func(rel string) {
		if t, ok := timers[rel]; ok {
			t.Reset(watchDebounce)
			return
		}
		timers[rel] = time.AfterFunc(watchDebounce, func() {
			select {
			case due <- rel:
			case <-ctx.Done():
			}
		})
	}
	for {
		select {
		case <-ctx.Done():
			for _, t := range timers {
				t.Stop()
			}
			return
		case rel := <-due:
			delete(timers, rel)
			var err error
			if rel == "" {
				err = s.Sync(ctx)
			} else {
				err = s.Reindex(ctx, rel)
			}
			if err != nil && ctx.Err() == nil {
				log.Printf("index %q: %v", rel, err)
			}
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			rel, ok := s.vaultRel(ev.Name)
			if !ok || rel == "" || access.Hidden(rel) {
				continue
			}
			if ev.Has(fsnotify.Create) {
				if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
					if s.vault.Policy.MayHoldReadable(rel) {
						if err := s.watchTree(w, ev.Name); err != nil {
							log.Printf("watch %s: %v", rel, err)
						}
						schedule("")
					}
					continue
				}
			}
			switch {
			case s.vault.Policy.IsNote(rel):
				schedule(rel)
			case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
				schedule("")
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				schedule("")
				continue
			}
			log.Printf("watch: %v", err)
		}
	}
}
