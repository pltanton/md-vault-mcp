package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pltanton/md-vault-mcp/internal/notes"
)

var version = "dev"

func main() {
	log.SetFlags(0)
	log.SetPrefix("md-vault-mcp: ")

	configPath := flag.String("config", "", "path to the TOML config (required)")
	transport := flag.String("transport", "", "stdio or http; overrides the config")
	listen := flag.String("listen", "", "address for the http transport; overrides the config")
	tokenFile := flag.String("token-file", "", "file with the bearer token for the http transport; overrides the config")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *configPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := run(*configPath, *transport, *listen, *tokenFile); err != nil {
		log.Fatal(err)
	}
}

func run(configPath, transport, listen, tokenFile string) error {
	cfg, err := notes.LoadConfig(configPath)
	if err != nil {
		return err
	}
	if transport != "" {
		cfg.Transport = transport
	}
	if listen != "" {
		cfg.Listen = listen
	}
	if tokenFile != "" {
		cfg.TokenFile = tokenFile
	}

	svc, err := notes.NewService(cfg)
	if err != nil {
		return err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "md-vault", Version: version}, nil)
	svc.Register(server)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cfg.Transport {
	case "", "stdio":
		return server.Run(ctx, &mcp.StdioTransport{})
	case "http":
		return serveHTTP(ctx, server, cfg)
	}
	return fmt.Errorf("transport %q: use stdio or http", cfg.Transport)
}

func serveHTTP(ctx context.Context, server *mcp.Server, cfg notes.Config) error {
	if cfg.Listen == "" {
		return errors.New("http transport needs http.listen")
	}
	if cfg.TokenFile == "" {
		return errors.New("http transport needs http.token_file")
	}
	data, err := os.ReadFile(cfg.TokenFile)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 16 {
		return errors.New("token: at least 16 characters, please")
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           notes.NewHTTPHandler(server, token),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("listening on %s", cfg.Listen)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}
