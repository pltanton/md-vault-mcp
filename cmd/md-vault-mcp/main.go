package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/pltanton/md-vault-mcp/internal/index"
	"github.com/pltanton/md-vault-mcp/internal/notes"
	"github.com/pltanton/md-vault-mcp/internal/review"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	svc, err := notes.NewService(cfg)
	if err != nil {
		return err
	}
	ix, err := index.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer ix.Close()
	if err := svc.UseIndex(ctx, ix); err != nil {
		return fmt.Errorf("index: %w", err)
	}
	if err := svc.Watch(ctx); err != nil {
		return fmt.Errorf("watch: %w", err)
	}
	if cfg.Review != nil {
		svc.EnableReview(cfg.Review.PublicURL)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "md-vault", Version: version}, nil)
	svc.Register(server)
	if cfg.Review != nil {
		stopReview, err := startReview(ctx, svc, cfg)
		if err != nil {
			return fmt.Errorf("review: %w", err)
		}
		defer stopReview()
	}

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

func startReview(ctx context.Context, svc *notes.Service, cfg notes.Config) (func(), error) {
	r := cfg.Review
	secret, err := os.ReadFile(r.OIDCClientSecretFile)
	if err != nil {
		return nil, err
	}
	key, err := review.LoadKey(filepath.Join(cfg.StateDir, "review.key"))
	if err != nil {
		return nil, err
	}
	rs := review.New(review.Config{
		PublicURL: r.PublicURL, AllowedGroups: r.AllowedGroups, Issuer: r.OIDCIssuer,
		ClientID: r.OIDCClientID, ClientSecret: strings.TrimSpace(string(secret)), Key: key,
	}, svc)
	ln, err := net.Listen("tcp", r.Listen)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: rs.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("review: %v", err)
		}
	}()
	go func() {
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			if err := svc.ExpireProposals(ctx); err != nil && ctx.Err() == nil {
				log.Printf("expire proposals: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	log.Printf("review page on %s", r.Listen)
	return func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}, nil
}
