// Package crawler keeps the catalog in sync with library directories.
package crawler

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"syncscope/internal/store"
)

type Crawler struct {
	Store   *store.Store
	Log     *slog.Logger
	started map[string]bool
}

func New(st *store.Store, log *slog.Logger) *Crawler {
	return &Crawler{Store: st, Log: log, started: map[string]bool{}}
}

func (c *Crawler) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	c.ensure(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.ensure(ctx)
		}
	}
}

func (c *Crawler) ensure(ctx context.Context) {
	for _, lib := range c.Store.ListLibraries() {
		if c.started[lib.ID] {
			continue
		}
		c.started[lib.ID] = true
		go c.watch(ctx, lib)
	}
}

func (c *Crawler) watch(ctx context.Context, lib *store.Library) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		c.Log.Error("crawler watcher", "library", lib.ID, "err", err)
		return
	}
	defer w.Close()
	addDirs(w, lib.RootPath, c.Log)

	debounce := time.NewTimer(time.Hour)
	if !debounce.Stop() {
		<-debounce.C
	}
	armed := false

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Create != 0 {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					addDirs(w, ev.Name, c.Log)
				}
			}
			if !armed {
				debounce.Reset(500 * time.Millisecond)
				armed = true
			}
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			c.Log.Warn("crawler event", "library", lib.ID, "err", err)
		case <-debounce.C:
			armed = false
			nodes, err := store.ScanLibrary(lib.RootPath)
			if err != nil {
				c.Log.Warn("crawler rescan", "library", lib.ID, "err", err)
				continue
			}
			if err := c.Store.ReplaceNodes(lib.ID, nodes); err != nil {
				c.Log.Warn("crawler replace", "library", lib.ID, "err", err)
				continue
			}
			addDirs(w, lib.RootPath, c.Log)
			c.Log.Debug("catalog refreshed", "library", lib.ID, "nodes", len(nodes))
		}
	}
}

func addDirs(w *fsnotify.Watcher, root string, log *slog.Logger) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".stversions" || d.Name() == ".stfolder" {
			return filepath.SkipDir
		}
		if err := w.Add(p); err != nil {
			log.Debug("watch dir", "path", p, "err", err)
		}
		return nil
	})
}
