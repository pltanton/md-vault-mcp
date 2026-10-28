package pgtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
)

var (
	once     sync.Once
	dir      string
	port     int
	startErr error
	dbs      atomic.Int64
)

func Main(m *testing.M) {
	code := m.Run()
	if dir != "" {
		exec.Command("pg_ctl", "-D", filepath.Join(dir, "data"), "-m", "immediate", "-w", "stop").Run()
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

// URL returns a fresh database owned by a non-superuser role, like production.
func URL(t testing.TB) string {
	t.Helper()
	if _, err := exec.LookPath("initdb"); err != nil {
		t.Skip("initdb is not on PATH; run the tests in nix develop")
	}
	once.Do(start)
	if startErr != nil {
		t.Fatal(startErr)
	}
	name := fmt.Sprintf("t%d", dbs.Add(1))
	if err := exec1(connString("postgres", "postgres"), "CREATE DATABASE "+name+" OWNER app"); err != nil {
		t.Fatal(err)
	}
	return connString("app", name)
}

func connString(user, db string) string {
	return fmt.Sprintf("host=127.0.0.1 port=%d user=%s dbname=%s sslmode=disable", port, user, db)
}

func exec1(url, sql string) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, sql)
	return err
}

func start() {
	var err error
	if dir, err = os.MkdirTemp("", "pgtest"); err != nil {
		startErr = err
		return
	}
	data := filepath.Join(dir, "data")
	out, err := exec.Command("initdb", "-D", data, "-U", "postgres", "--auth=trust", "-E", "UTF8",
		"--locale-provider=builtin", "--locale=C.UTF-8").CombinedOutput()
	if err != nil {
		startErr = fmt.Errorf("initdb: %v\n%s", err, out)
		return
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		startErr = err
		return
	}
	port = l.Addr().(*net.TCPAddr).Port
	l.Close()
	opts := fmt.Sprintf("-k '' -c listen_addresses=127.0.0.1 -p %d -c fsync=off", port)
	out, err = exec.Command("pg_ctl", "-D", data, "-o", opts, "-l", filepath.Join(dir, "log"), "-w", "start").CombinedOutput()
	if err != nil {
		startErr = fmt.Errorf("pg_ctl start: %v\n%s", err, out)
		return
	}
	startErr = exec1(connString("postgres", "postgres"), "CREATE ROLE app LOGIN")
}
