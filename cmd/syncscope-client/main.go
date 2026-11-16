package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"syncscope/internal/control"
	"syncscope/internal/store"
	"syncscope/internal/syncthing"
	"syncscope/internal/webui"
)

type stringsFlag []string

func (s *stringsFlag) String() string { return strings.Join(*s, ",") }
func (s *stringsFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	controlURL := flag.String("control", "http://127.0.0.1:8088", "control plane URL")
	token := flag.String("token", env("SYNCSCOPE_DEVICE_TOKEN", ""), "device token")
	stURL := flag.String("syncthing", "http://127.0.0.1:8385", "Syncthing API URL")
	stKey := flag.String("syncthing-key", env("SYNCTHING_API_KEY", ""), "Syncthing API key")
	libraryID := flag.String("library", "", "library id")
	folderID := flag.String("folder", "", "Syncthing folder id (default: library id)")
	local := flag.String("local", "", "local folder path")
	originDevice := flag.String("origin-device", "", "origin Syncthing device id")
	originAddress := flag.String("origin-address", "", "origin address, e.g. tcp://127.0.0.1:22000")
	statePath := flag.String("state", "syncscope-selection.json", "local selection state")
	uiAddr := flag.String("ui", "", "serve the navigator UI on this address, e.g. 127.0.0.1:8099")
	wait := flag.Bool("wait", false, "wait until the folder is idle")
	timeout := flag.Duration("timeout", 60*time.Second, "wait timeout")
	var dirs, excludes stringsFlag
	flag.Var(&dirs, "select", "selected directory (repeatable)")
	flag.Var(&excludes, "exclude", "excluded path (repeatable)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if *libraryID == "" || *local == "" {
		log.Error("-library and -local are required")
		os.Exit(2)
	}
	if *folderID == "" {
		*folderID = *libraryID
	}

	ctl := control.New(*controlURL)
	ctl.Token = *token
	st := syncthing.New(*stURL, *stKey)
	ctx := context.Background()

	libs, err := ctl.Libraries(ctx)
	if err != nil {
		log.Error("list libraries", "err", err)
		os.Exit(1)
	}
	var lib store.Library
	for _, l := range libs {
		if l.ID == *libraryID {
			lib = l
		}
	}
	if lib.ID == "" {
		log.Error("library is not available to this device", "library", *libraryID)
		os.Exit(1)
	}

	status, err := st.Status(ctx)
	if err != nil {
		log.Error("syncthing status", "err", err)
		os.Exit(1)
	}
	if *originDevice != "" {
		if err := st.EnsureDevice(ctx, *originDevice, "origin", *originAddress); err != nil {
			log.Error("ensure origin device", "err", err)
			os.Exit(1)
		}
	}

	devices := []string{status.MyID}
	if *originDevice != "" {
		devices = append(devices, *originDevice)
	}
	folder := map[string]any{
		"id":               *folderID,
		"label":            lib.Name,
		"path":             *local,
		"type":             "receiveonly",
		"fsWatcherEnabled": true,
		"rescanIntervalS":  3600,
	}
	if _, err := st.EnsureFolder(ctx, *folderID, folder); err != nil {
		log.Error("ensure folder", "err", err)
		os.Exit(1)
	}
	if err := st.SetFolderDevices(ctx, *folderID, devices); err != nil {
		log.Error("set folder devices", "err", err)
		os.Exit(1)
	}

	selection, err := webui.LoadSelection(*statePath, *libraryID)
	if err != nil {
		log.Error("load selection", "err", err)
		os.Exit(1)
	}
	ui := &webui.Server{
		Control:   ctl,
		Syncthing: st,
		Library:   lib,
		FolderID:  *folderID,
		LocalPath: *local,
		Selection: selection,
		Log:       log,
	}
	if len(dirs) > 0 || len(excludes) > 0 {
		inc, err := cleanFlagPaths(dirs)
		if err != nil {
			log.Error("select", "err", err)
			os.Exit(2)
		}
		exc, err := cleanFlagPaths(excludes)
		if err != nil {
			log.Error("exclude", "err", err)
			os.Exit(2)
		}
		if err := selection.Set(inc, exc); err != nil {
			log.Error("save selection", "err", err)
			os.Exit(1)
		}
	}
	if err := ui.Apply(ctx); err != nil {
		log.Error("apply selection", "err", err)
		os.Exit(1)
	}
	sel := selection.Get()
	fmt.Printf("folder=%s selected=%d excluded=%d\n", *folderID, len(sel.Included), len(sel.Excluded))

	if *uiAddr != "" {
		serveUI(ctx, ui, *uiAddr, log)
		return
	}

	if *wait {
		minFiles := 0
		if len(sel.Included) > 0 {
			minFiles = 1
		}
		deadline := time.Now().Add(*timeout)
		for time.Now().Before(deadline) {
			fs, err := st.FolderStatus(ctx, *folderID)
			if err != nil {
				log.Error("folder status", "err", err)
				break
			}
			if fs.State == "idle" && fs.NeedFiles == 0 && fs.LocalFiles >= minFiles {
				fmt.Printf("status=idle local_files=%d\n", fs.LocalFiles)
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		log.Warn("timeout waiting for idle")
	}
}

func serveUI(ctx context.Context, ui *webui.Server, addr string, log *slog.Logger) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           ui.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		log.Info("navigator UI", "url", "http://"+addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("ui", "err", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

func cleanFlagPaths(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		p = strings.TrimPrefix(p, "/")
		if p == "" {
			continue
		}
		c := path.Clean(p)
		if c == "." || c == ".." || strings.HasPrefix(c, "../") {
			return nil, fmt.Errorf("invalid path %q", raw)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
