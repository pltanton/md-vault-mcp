package access

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrNotReadable = errors.New("no such note, or it is outside what you may read")
	ErrNotWritable = errors.New("this note is outside what you may change")
)

type Vault struct {
	root   string
	Policy *Policy
}

func OpenVault(root string, policy *Policy) (*Vault, error) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("vault %s is not a directory", root)
	}
	return &Vault{root: real, Policy: policy}, nil
}

func (v *Vault) Root() string { return v.root }

func (v *Vault) ResolveRead(rel string) (string, string, error) {
	return v.resolve(rel, v.Policy.CanRead, ErrNotReadable, false)
}

func (v *Vault) ResolveReadAny(rel string) (string, string, error) {
	return v.resolve(rel, v.Policy.CanReadFile, ErrNotReadable, false)
}

func (v *Vault) ResolveWrite(rel string) (string, string, error) {
	return v.resolve(rel, v.Policy.CanWrite, ErrNotWritable, false)
}

func (v *Vault) ResolveCreate(rel string) (string, string, error) {
	return v.resolve(rel, v.Policy.CanWrite, ErrNotWritable, true)
}

func (v *Vault) ResolveDir(rel string) (string, string, error) {
	clean, err := Clean(rel)
	if err != nil {
		return "", "", err
	}
	if !v.Policy.MayHoldReadable(clean) {
		return "", "", ErrNotReadable
	}
	real, err := filepath.EvalSymlinks(filepath.Join(v.root, filepath.FromSlash(clean)))
	if err != nil {
		return "", "", ErrNotReadable
	}
	inner, ok := v.inside(real)
	if !ok || inner != clean {
		return "", "", ErrNotReadable
	}
	return real, clean, nil
}

func (v *Vault) resolve(rel string, allowed func(string) bool, deny error, create bool) (string, string, error) {
	clean, err := Clean(rel)
	if err != nil {
		return "", "", err
	}
	if !allowed(clean) {
		return "", "", deny
	}
	abs := filepath.Join(v.root, filepath.FromSlash(clean))

	if create {
		if _, err := os.Lstat(abs); err == nil {
			return "", "", fmt.Errorf("%s already exists", clean)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		ancestor, realAncestor, err := v.existingAncestor(filepath.Dir(abs))
		if err != nil {
			return "", "", err
		}
		rest, err := filepath.Rel(ancestor, abs)
		if err != nil {
			return "", "", err
		}
		target := filepath.Join(realAncestor, rest)
		inner, ok := v.inside(target)
		if !ok || !allowed(inner) {
			return "", "", deny
		}
		return target, clean, nil
	}

	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", "", ErrNotReadable
		}
		return "", "", err
	}
	inner, ok := v.inside(real)
	if !ok || !allowed(inner) {
		return "", "", deny
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s is not a regular file", clean)
	}
	return real, clean, nil
}

func (v *Vault) existingAncestor(dir string) (string, string, error) {
	for {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return dir, real, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", "", err
		}
		dir = next
	}
}

func (v *Vault) inside(real string) (string, bool) {
	rel, err := filepath.Rel(v.root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return filepath.ToSlash(rel), true
}
