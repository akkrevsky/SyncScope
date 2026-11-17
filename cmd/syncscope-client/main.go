package main

import (
	"context"
	"encoding/json"
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

type mountConfig struct {
	Library       string `json:"library"`
	Folder        string `json:"folder"`
	Local         string `json:"local"`
	OriginDevice  string `json:"origin_device"`
	OriginAddress string `json:"origin_address"`
}

type clientConfig struct {
	Control      string        `json:"control"`
	Token        string        `json:"token"`
	Syncthing    string        `json:"syncthing"`
	SyncthingKey string        `json:"syncthing_key"`
	State        string        `json:"state"`
	UI           string        `json:"ui"`
	Mounts       []mountConfig `json:"mounts"`
}

func main() {
	configPath := flag.String("config", "", "JSON config with multiple mounts")
	controlURL := flag.String("control", "http://127.0.0.1:8088", "control plane URL")
	token := flag.String("token", env("SYNCSCOPE_DEVICE_TOKEN", ""), "device token")
	stURL := flag.String("syncthing", "http://127.0.0.1:8385", "Syncthing API URL")
	stKey := flag.String("syncthing-key", env("SYNCTHING_API_KEY", ""), "Syncthing API key")
	libraryID := flag.String("library", "", "library id (single-mount mode)")
	folderID := flag.String("folder", "", "Syncthing folder id (default: library id)")
	local := flag.String("local", "", "local folder path (single-mount mode)")
	originDevice := flag.String("origin-device", "", "origin Syncthing device id")
	originAddress := flag.String("origin-address", "", "origin address, e.g. tcp://127.0.0.1:22000")
	statePath := flag.String("state", "syncscope-selection.json", "local selection state")
	uiAddr := flag.String("ui", "", "serve the navigator UI on this address")
	wait := flag.Bool("wait", false, "wait until folders are idle")
	timeout := flag.Duration("timeout", 60*time.Second, "wait timeout")
	var dirs, excludes stringsFlag
	flag.Var(&dirs, "select", "selected directory for the first mount (repeatable)")
	flag.Var(&excludes, "exclude", "excluded path for the first mount (repeatable)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := clientConfig{}
	if *configPath != "" {
		b, err := os.ReadFile(*configPath)
		if err != nil {
			log.Error("read config", "err", err)
			os.Exit(1)
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			log.Error("parse config", "err", err)
			os.Exit(1)
		}
	}
	if cfg.Control == "" {
		cfg.Control = *controlURL
	}
	if cfg.Token == "" {
		cfg.Token = *token
	}
	if cfg.Syncthing == "" {
		cfg.Syncthing = *stURL
	}
	if cfg.SyncthingKey == "" {
		cfg.SyncthingKey = *stKey
	}
	if cfg.State == "" {
		cfg.State = *statePath
	}
	if *uiAddr != "" {
		cfg.UI = *uiAddr
	}
	if len(cfg.Mounts) == 0 {
		if *libraryID == "" || *local == "" {
			log.Error("-library and -local are required without -config")
			os.Exit(2)
		}
		folder := *folderID
		if folder == "" {
			folder = *libraryID
		}
		cfg.Mounts = []mountConfig{{
			Library:       *libraryID,
			Folder:        folder,
			Local:         *local,
			OriginDevice:  *originDevice,
			OriginAddress: *originAddress,
		}}
	}
	if cfg.Token == "" {
		log.Error("device token is required")
		os.Exit(2)
	}

	ctl := control.New(cfg.Control)
	ctl.Token = cfg.Token
	st := syncthing.New(cfg.Syncthing, cfg.SyncthingKey)
	ctx := context.Background()

	libs, err := ctl.Libraries(ctx)
	if err != nil {
		log.Error("list libraries", "err", err)
		os.Exit(1)
	}
	byID := map[string]store.Library{}
	for _, l := range libs {
		byID[l.ID] = l
	}
	status, err := st.Status(ctx)
	if err != nil {
		log.Error("syncthing status", "err", err)
		os.Exit(1)
	}

	mounts := make([]webui.Mount, 0, len(cfg.Mounts))
	origins := map[string]bool{}
	for _, mc := range cfg.Mounts {
		lib, ok := byID[mc.Library]
		if !ok {
			log.Error("library is not available to this device", "library", mc.Library)
			os.Exit(1)
		}
		folder := mc.Folder
		if folder == "" {
			folder = mc.Library
		}
		if mc.OriginDevice != "" && !origins[mc.OriginDevice] {
			if err := st.EnsureDevice(ctx, mc.OriginDevice, "origin", mc.OriginAddress); err != nil {
				log.Error("ensure origin device", "device", mc.OriginDevice, "err", err)
				os.Exit(1)
			}
			origins[mc.OriginDevice] = true
		}
		devices := []string{status.MyID}
		if mc.OriginDevice != "" {
			devices = append(devices, mc.OriginDevice)
		}
		folderCfg := map[string]any{
			"id":               folder,
			"label":            lib.Name,
			"path":             mc.Local,
			"type":             "receiveonly",
			"fsWatcherEnabled": true,
			"rescanIntervalS":  3600,
		}
		if _, err := st.EnsureFolder(ctx, folder, folderCfg); err != nil {
			log.Error("ensure folder", "folder", folder, "err", err)
			os.Exit(1)
		}
		if err := st.SetFolderDevices(ctx, folder, devices); err != nil {
			log.Error("set folder devices", "folder", folder, "err", err)
			os.Exit(1)
		}
		mounts = append(mounts, webui.Mount{Library: lib, FolderID: folder, LocalPath: mc.Local})
	}

	selections, err := webui.LoadSelections(cfg.State)
	if err != nil {
		log.Error("load selection", "err", err)
		os.Exit(1)
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
		if err := selections.Set(mounts[0].Library.ID, inc, exc); err != nil {
			log.Error("save selection", "err", err)
			os.Exit(1)
		}
	}
	ui := &webui.Server{
		Control:    ctl,
		Syncthing:  st,
		Mounts:     mounts,
		Selections: selections,
		Log:        log,
	}
	if err := ui.Apply(ctx); err != nil {
		log.Error("apply selection", "err", err)
		os.Exit(1)
	}
	for _, m := range mounts {
		sel := selections.Get(m.Library.ID)
		fmt.Printf("mount=%s folder=%s selected=%d excluded=%d\n", m.Library.ID, m.FolderID, len(sel.Included), len(sel.Excluded))
	}

	if cfg.UI != "" {
		serveUI(ctx, ui, cfg.UI, log)
		return
	}
	if *wait {
		deadline := time.Now().Add(*timeout)
		for _, m := range mounts {
			sel := selections.Get(m.Library.ID)
			minFiles := 0
			if len(sel.Included) > 0 {
				minFiles = 1
			}
			for time.Now().Before(deadline) {
				fs, err := st.FolderStatus(ctx, m.FolderID)
				if err != nil {
					log.Error("folder status", "err", err)
					break
				}
				if fs.State == "idle" && fs.NeedFiles == 0 && fs.LocalFiles >= minFiles {
					fmt.Printf("mount=%s status=idle local_files=%d\n", m.Library.ID, fs.LocalFiles)
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
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
