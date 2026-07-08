package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	appdata "harukizmoe/pimoe/internal/application/data"
	approuter "harukizmoe/pimoe/internal/application/router"
	appservice "harukizmoe/pimoe/internal/application/service"
	"harukizmoe/pimoe/internal/logger"
)

const (
	defaultServerAddr               = ":8080"
	defaultServerProviderConfigPath = "configs/providers.yaml"
	defaultServerSessionRoot        = ".moe/sessions"
	serverLogPath                   = ".moe/logs/agent.log"
)

type serverOptions struct {
	addr         string
	configPath   string
	sessionRoot  string
	providerName string
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
	sessionService, err := appservice.NewSessionService(appservice.SessionConfig{
		Store:              appdata.NewManagerSessionStore(opts.sessionRoot),
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
		addr:        defaultServerAddr,
		configPath:  defaultServerProviderConfigPath,
		sessionRoot: defaultServerSessionRoot,
	}
	flags := flag.NewFlagSet("pimoe-server", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&opts.addr, "addr", opts.addr, "HTTP listen address")
	flags.StringVar(&opts.configPath, "config", opts.configPath, "providers YAML config path")
	flags.StringVar(&opts.sessionRoot, "session-root", opts.sessionRoot, "managed session root")
	flags.StringVar(&opts.providerName, "provider", "", "provider instance name")
	if err := flags.Parse(args); err != nil {
		return serverOptions{}, fmt.Errorf("parse flags: %w", err)
	}
	return opts, nil
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
