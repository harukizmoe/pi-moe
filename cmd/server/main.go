package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	approuter "harukizmoe/pimoe/internal/application/router"
	appservice "harukizmoe/pimoe/internal/application/service"
	appconfig "harukizmoe/pimoe/internal/config"
	"harukizmoe/pimoe/internal/logger"
	"harukizmoe/pimoe/internal/session"
	pgstore "harukizmoe/pimoe/internal/storage/postgres"
)

const (
	defaultServerAddr               = ":8080"
	defaultServerProviderConfigPath = "configs/providers.yaml"
	defaultServerSessionRoot        = ".moe/sessions"
	defaultServerSessionStore       = "file"
	serverSessionStoreFile          = "file"
	serverSessionStorePostgres      = "postgres"
	serverLogPath                   = ".moe/logs/agent.log"
)

type serverOptions struct {
	appConfigPath string
	addr          string
	configPath    string
	sessionRoot   string
	providerName  string
	sessionStore  string
	postgresDSN   string
}

func main() {
	ctx := context.Background()
	opts, err := parseServerOptions(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	appLogger, closeLogger, err := logger.NewDevelopmentFile(serverLogPath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := closeLogger(); err != nil {
			log.Printf("close logger: %v", err)
		}
	}()
	sessionStore, closeSessionStore, err := openServerSessionStore(ctx, opts)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := closeSessionStore(); err != nil {
			log.Printf("close session store: %v", err)
		}
	}()
	sessionService, err := appservice.NewSessionService(appservice.SessionConfig{
		SessionRoot:        opts.sessionRoot,
		Store:              sessionStore,
		ProviderConfigPath: opts.configPath,
		ProviderName:       opts.providerName,
		Logger:             appLogger,
	})
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: opts.addr, Handler: approuter.New(approuter.Config{SessionService: sessionService})}
	if err := runServer(ctx, server); err != nil {
		log.Fatal(err)
	}
}

func parseServerOptions(args []string) (serverOptions, error) {
	opts := serverOptions{
		addr:         defaultServerAddr,
		configPath:   defaultServerProviderConfigPath,
		sessionRoot:  defaultServerSessionRoot,
		sessionStore: defaultServerSessionStore,
	}
	flags := flag.NewFlagSet("pimoe-server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.appConfigPath, "app-config", "", "application runtime YAML config path")
	flags.StringVar(&opts.addr, "addr", opts.addr, "HTTP listen address")
	flags.StringVar(&opts.configPath, "config", opts.configPath, "providers YAML config path")
	flags.StringVar(&opts.sessionRoot, "session-root", opts.sessionRoot, "managed session root")
	flags.StringVar(&opts.providerName, "provider", "", "provider instance name")
	flags.StringVar(&opts.sessionStore, "session-store", opts.sessionStore, "session metadata store: file or postgres")
	flags.StringVar(&opts.postgresDSN, "postgres-dsn", "", "PostgreSQL DSN for --session-store postgres")
	if err := flags.Parse(args); err != nil {
		return serverOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	if strings.TrimSpace(opts.appConfigPath) != "" {
		loaded, err := appconfig.LoadApp(opts.appConfigPath)
		if err != nil {
			return serverOptions{}, err
		}
		applyAppConfigDefaults(&opts, loaded, flags)
	}
	if err := validateServerOptions(&opts); err != nil {
		return serverOptions{}, err
	}
	return opts, nil
}

func validateServerOptions(opts *serverOptions) error {
	opts.sessionStore = strings.ToLower(strings.TrimSpace(opts.sessionStore))
	if opts.sessionStore == "" {
		opts.sessionStore = defaultServerSessionStore
	}
	switch opts.sessionStore {
	case serverSessionStoreFile:
		return nil
	case serverSessionStorePostgres:
		if strings.TrimSpace(opts.postgresDSN) == "" {
			return fmt.Errorf("--postgres-dsn is required when --session-store=postgres")
		}
		return nil
	default:
		return fmt.Errorf("unknown session store %q; want file or postgres", opts.sessionStore)
	}
}

func applyAppConfigDefaults(opts *serverOptions, cfg *appconfig.AppConfig, flags *flag.FlagSet) {
	if cfg == nil {
		return
	}
	if flagWasNotSet(flags, "addr") && strings.TrimSpace(cfg.Server.Addr) != "" {
		opts.addr = cfg.Server.Addr
	}
	if flagWasNotSet(flags, "session-root") && strings.TrimSpace(cfg.Session.Root) != "" {
		opts.sessionRoot = cfg.Session.Root
	}
	if flagWasNotSet(flags, "session-store") && strings.TrimSpace(cfg.Session.Store.Type) != "" {
		opts.sessionStore = cfg.Session.Store.Type
	}
	if flagWasNotSet(flags, "postgres-dsn") && strings.TrimSpace(cfg.Session.Store.Postgres.DSN) != "" {
		opts.postgresDSN = cfg.Session.Store.Postgres.DSN
	}
}

func flagWasNotSet(flags *flag.FlagSet, name string) bool {
	seen := false
	flags.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			seen = true
		}
	})
	return !seen
}

func openServerSessionStore(ctx context.Context, opts serverOptions) (session.SessionStore, func() error, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if opts.sessionStore == serverSessionStoreFile {
		return nil, func() error { return nil }, nil
	}

	db, err := gorm.Open(postgres.Open(opts.postgresDSN), &gorm.Config{})
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres session store: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("open postgres session store sql db: %w", err)
	}
	return pgstore.NewSessionStore(db, opts.sessionRoot), sqlDB.Close, nil
}

func runServer(ctx context.Context, server *http.Server) error {
	if ctx == nil {
		ctx = context.Background()
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	case <-ctx.Done():
		if err := server.Shutdown(context.Background()); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		return ctx.Err()
	}
}
