package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"syncscope/internal/api"
	"syncscope/internal/crawler"
	"syncscope/internal/oidc"
	"syncscope/internal/store"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8088", "HTTP listen address")
	data := flag.String("data", "data/state.json", "state file")
	admin := flag.String("admin-token", env("SYNCSCOPE_ADMIN_TOKEN", "dev-admin-token"), "admin token")
	oidcIssuer := flag.String("oidc-issuer", env("OIDC_ISSUER", ""), "OIDC issuer URL")
	oidcClient := flag.String("oidc-client-id", env("OIDC_CLIENT_ID", ""), "OIDC client id")
	oidcSecret := flag.String("oidc-client-secret", env("OIDC_CLIENT_SECRET", ""), "OIDC client secret")
	oidcScopes := flag.String("oidc-scopes", env("OIDC_SCOPES", "openid profile email"), "OIDC scopes")
	shareSecret := flag.String("share-secret", env("SHARE_SECRET", ""), "HMAC secret for public share links")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	st, err := store.Open(*data)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	apiServer := api.New(st, *admin, log)
	apiServer.ShareSecret = *shareSecret
	if *oidcIssuer != "" && *oidcClient != "" {
		oc, err := oidc.New(ctx, oidc.Config{
			Issuer:       *oidcIssuer,
			ClientID:     *oidcClient,
			ClientSecret: *oidcSecret,
			Scopes:       *oidcScopes,
		})
		if err != nil {
			log.Error("oidc discovery", "err", err)
			os.Exit(1)
		}
		apiServer.OIDC = oc
		apiServer.Issuer = *oidcIssuer
		log.Info("OIDC enabled", "issuer", *oidcIssuer)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go crawler.New(st, log).Run(ctx)

	go func() {
		log.Info("control plane listening", "addr", *addr, "data", *data)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
