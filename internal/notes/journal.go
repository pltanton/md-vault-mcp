package notes

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type entry struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Heading string    `json:"heading,omitempty"`
	Lines   int       `json:"lines"`
	Whole   bool      `json:"whole,omitempty"`
	Hash    string    `json:"hash"`
	Created time.Time `json:"created_at"`
	Updated time.Time `json:"updated_at"`
}

type journalFile struct {
	Entries []entry `json:"entries"`
}

// HMAC, not a bare hash: a short line like "- [ ] milk" is guessable from its SHA-256.
type journal struct {
	dir string
	key []byte
}

func openJournal(dir string) (*journal, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("state dir: %w", err)
	}
	key, err := loadKey(filepath.Join(dir, "key"))
	if err != nil {
		return nil, err
	}
	return &journal{dir: dir, key: key}, nil
}

func loadKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		key, err := hex.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) < 32 {
			return nil, fmt.Errorf("journal key %s is corrupt", path)
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	rand.Read(key)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return loadKey(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(hex.EncodeToString(key) + "\n"); err != nil {
		f.Close()
		return nil, err
	}
	return key, f.Close()
}

func (j *journal) hash(lines []string) string {
	m := hmac.New(sha256.New, j.key)
	m.Write([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(m.Sum(nil))
}

func (j *journal) lock(how int) (func(), error) {
	f, err := os.OpenFile(filepath.Join(j.dir, "journal.lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

func (j *journal) load() ([]entry, error) {
	data, err := os.ReadFile(filepath.Join(j.dir, "journal.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var jf journalFile
	if err := json.Unmarshal(data, &jf); err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	return jf.Entries, nil
}

func (j *journal) save(entries []entry) error {
	data, err := json.MarshalIndent(journalFile{Entries: entries}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(j.dir, "journal-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(j.dir, "journal.json"))
}

func (j *journal) view() ([]entry, error) {
	unlock, err := j.lock(syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return j.load()
}

func (j *journal) update(fn func([]entry) ([]entry, error)) error {
	unlock, err := j.lock(syscall.LOCK_EX)
	if err != nil {
		return err
	}
	defer unlock()
	entries, err := j.load()
	if err != nil {
		return err
	}
	next, err := fn(entries)
	if err != nil {
		return err
	}
	return j.save(next)
}

func newID() string {
	var b [6]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
