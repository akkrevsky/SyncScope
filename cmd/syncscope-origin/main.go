package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"syncscope/internal/control"
	"syncscope/internal/syncthing"
)

func main() {
	controlURL := flag.String("control", "http://127.0.0.1:8088", "control plane URL")
	adminToken := flag.String("admin-token", env("SYNCSCOPE_ADMIN_TOKEN", "dev-admin-token"), "admin token")
	stURL := flag.String("syncthing", "http://127.0.0.1:8384", "Syncthing API URL")
	stKey := flag.String("syncthing-key", env("SYNCTHING_API_KEY", ""), "Syncthing API key")
	interval := flag.Duration("interval", 10*time.Second, "reconcile interval")
	once := flag.Bool("once", false, "run once and exit")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctl := control.New(*controlURL)
	ctl.AdminToken = *adminToken
	st := syncthing.New(*stURL, *stKey)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		cancel()
	}()

	for {
		if err := reconcile(ctx, ctl, st, log); err != nil {
			log.Error("reconcile", "err", err)
		}
		if *once {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(*interval):
		}
	}
}

func reconcile(ctx context.Context, ctl *control.Client, st *syncthing.Client, log *slog.Logger) error {
	status, err := st.Status(ctx)
	if err != nil {
		return err
	}
	libs, err := ctl.OriginLibraries(ctx)
	if err != nil {
		return err
	}
	for _, ol := range libs {
		ids := []string{status.MyID}
		for _, d := range ol.Devices {
			if d.SyncthingID == "" || d.SyncthingID == status.MyID {
				continue
			}
			if err := st.EnsureDevice(ctx, d.SyncthingID, d.ID, d.Address); err != nil {
				log.Error("ensure device", "device", d.ID, "err", err)
				continue
			}
			ids = append(ids, d.SyncthingID)
		}

		folder := map[string]any{
			"id":               ol.Library.ID,
			"label":            ol.Library.Name,
			"path":             ol.Library.RootPath,
			"type":             "sendonly",
			"fsWatcherEnabled": true,
			"rescanIntervalS":  3600,
			"devices":          devices(ids),
		}
		if _, err := st.EnsureFolder(ctx, ol.Library.ID, folder); err != nil {
			log.Error("ensure folder", "library", ol.Library.ID, "err", err)
			continue
		}
		if err := st.SetFolderDevices(ctx, ol.Library.ID, ids); err != nil {
			log.Error("set folder devices", "library", ol.Library.ID, "err", err)
			continue
		}
		log.Info("library reconciled", "library", ol.Library.ID, "devices", len(ids)-1)
	}
	return nil
}

func devices(ids []string) []any {
	out := make([]any, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, map[string]any{"deviceID": id})
	}
	return out
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
