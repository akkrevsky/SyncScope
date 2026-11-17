// Package store is a small JSON-backed control-plane store for the MVP.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type User struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	Token     string    `json:"token"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Device struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id,omitempty"`
	Name        string    `json:"name"`
	SyncthingID string    `json:"syncthing_id"`
	Address     string    `json:"address,omitempty"`
	Platform    string    `json:"platform,omitempty"`
	Token       string    `json:"token"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

type Library struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RootPath  string    `json:"root_path"`
	OriginURL string    `json:"origin_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Grant struct {
	LibraryID string    `json:"library_id"`
	DeviceID  string    `json:"device_id"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

type Node struct {
	ID        string    `json:"id"`
	LibraryID string    `json:"library_id"`
	Path      string    `json:"path"`
	Parent    string    `json:"parent"`
	Name      string    `json:"name"`
	IsDir     bool      `json:"is_dir"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"mod_time"`
}

type state struct {
	Users     map[string]*User    `json:"users"`
	Sessions  map[string]*Session `json:"sessions"`
	Devices   map[string]*Device  `json:"devices"`
	Libraries map[string]*Library `json:"libraries"`
	Grants    []*Grant            `json:"grants"`
	Nodes     map[string][]*Node  `json:"nodes"`
	Versions  map[string]uint64   `json:"versions"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	data state
}

func Open(p string) (*Store, error) {
	s := &Store{path: p, data: state{
		Users:     map[string]*User{},
		Sessions:  map[string]*Session{},
		Devices:   map[string]*Device{},
		Libraries: map[string]*Library{},
		Nodes:     map[string][]*Node{},
		Versions:  map[string]uint64{},
	}}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, fmt.Errorf("load %s: %w", p, err)
		}
	}
	if s.data.Devices == nil {
		s.data.Devices = map[string]*Device{}
	}
	if s.data.Libraries == nil {
		s.data.Libraries = map[string]*Library{}
	}
	if s.data.Nodes == nil {
		s.data.Nodes = map[string][]*Node{}
	}
	if s.data.Versions == nil {
		s.data.Versions = map[string]uint64{}
	}
	if s.data.Users == nil {
		s.data.Users = map[string]*User{}
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]*Session{}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) CreateDevice(name, syncthingID, address, platform string) (*Device, error) {
	return s.CreateDeviceForUser("", name, syncthingID, address, platform)
}

func (s *Store) CreateDeviceForUser(userID, name, syncthingID, address, platform string) (*Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(syncthingID) == "" {
		return nil, fmt.Errorf("syncthing_id is required")
	}
	for _, d := range s.data.Devices {
		if d.SyncthingID == syncthingID && d.Status == "active" {
			return nil, fmt.Errorf("device with syncthing_id %s already exists", syncthingID)
		}
	}
	d := &Device{
		ID:          "dev-" + randHex(6),
		UserID:      userID,
		Name:        strings.TrimSpace(name),
		SyncthingID: syncthingID,
		Address:     strings.TrimSpace(address),
		Platform:    platform,
		Token:       randHex(24),
		Status:      "active",
		CreatedAt:   time.Now().UTC(),
	}
	if d.Name == "" {
		d.Name = d.ID
	}
	s.data.Devices[d.ID] = d
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return d, nil
}

func (s *Store) CreateOrGetUser(subject, email, name string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.data.Users {
		if u.Subject == subject {
			if email != "" {
				u.Email = email
			}
			if name != "" {
				u.Name = name
			}
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			return u, nil
		}
	}
	u := &User{
		ID:        "usr-" + randHex(6),
		Subject:   subject,
		Email:     email,
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	s.data.Users[u.ID] = u
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) CreateSession(userID string, ttl time.Duration) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Users[userID]; !ok {
		return nil, fmt.Errorf("user not found")
	}
	sess := &Session{
		Token:     randHex(32),
		UserID:    userID,
		ExpiresAt: time.Now().Add(ttl).UTC(),
	}
	s.data.Sessions[sess.Token] = sess
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *Store) UserBySession(token string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.data.Sessions[token]
	if !ok || time.Now().After(sess.ExpiresAt) {
		return nil, false
	}
	u, ok := s.data.Users[sess.UserID]
	return u, ok
}

func (s *Store) User(id string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.data.Users[id]
	return u, ok
}

func (s *Store) DeviceByToken(token string) (*Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.data.Devices {
		if d.Token == token && d.Status == "active" {
			return d, true
		}
	}
	return nil, false
}

func (s *Store) Device(id string) (*Device, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.data.Devices[id]
	return d, ok
}

func (s *Store) ListDevices() []*Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Device, 0, len(s.data.Devices))
	for _, d := range s.data.Devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) RevokeDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.data.Devices[id]
	if !ok {
		return fmt.Errorf("device not found")
	}
	d.Status = "revoked"
	for _, g := range s.data.Grants {
		if g.DeviceID == id {
			g.State = "revoked"
		}
	}
	return s.saveLocked()
}

func (s *Store) CreateLibrary(name, root string) (*Library, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("library root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("library root is not a directory")
	}
	nodes, err := scanLibrary("pending", abs)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, lib := range s.data.Libraries {
		if overlapping(lib.RootPath, abs) {
			return nil, fmt.Errorf("library root overlaps %s (%s)", lib.Name, lib.RootPath)
		}
	}
	lib := &Library{
		ID:        "lib-" + randHex(4),
		Name:      strings.TrimSpace(name),
		RootPath:  abs,
		CreatedAt: time.Now().UTC(),
	}
	if lib.Name == "" {
		lib.Name = filepath.Base(abs)
	}
	for _, n := range nodes {
		n.LibraryID = lib.ID
		n.ID = nodeID(lib.ID, n.Path)
	}
	s.data.Libraries[lib.ID] = lib
	s.data.Nodes[lib.ID] = nodes
	s.data.Versions[lib.ID] = 1
	if err := s.saveLocked(); err != nil {
		delete(s.data.Libraries, lib.ID)
		delete(s.data.Nodes, lib.ID)
		return nil, err
	}
	return lib, nil
}

func (s *Store) SetLibraryOrigin(id, originURL string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lib, ok := s.data.Libraries[id]
	if !ok {
		return fmt.Errorf("library not found")
	}
	lib.OriginURL = strings.TrimRight(strings.TrimSpace(originURL), "/")
	return s.saveLocked()
}

func (s *Store) DeviceCanAccess(deviceID, libraryID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasGrantLocked(deviceID, libraryID)
}

func (s *Store) Library(id string) (*Library, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	l, ok := s.data.Libraries[id]
	return l, ok
}

func (s *Store) ListLibraries() []*Library {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Library, 0, len(s.data.Libraries))
	for _, l := range s.data.Libraries {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) AddGrant(libraryID, deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Libraries[libraryID]; !ok {
		return fmt.Errorf("library not found")
	}
	d, ok := s.data.Devices[deviceID]
	if !ok {
		return fmt.Errorf("device not found")
	}
	if d.Status != "active" {
		return fmt.Errorf("device is %s", d.Status)
	}
	for _, g := range s.data.Grants {
		if g.LibraryID == libraryID && g.DeviceID == deviceID {
			g.State = "active"
			return s.saveLocked()
		}
	}
	s.data.Grants = append(s.data.Grants, &Grant{
		LibraryID: libraryID,
		DeviceID:  deviceID,
		State:     "active",
		CreatedAt: time.Now().UTC(),
	})
	return s.saveLocked()
}

func (s *Store) LibrariesForDevice(deviceID string) []*Library {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Library
	for _, g := range s.data.Grants {
		if g.DeviceID != deviceID || g.State != "active" {
			continue
		}
		if l, ok := s.data.Libraries[g.LibraryID]; ok {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s *Store) GrantsForLibrary(libraryID string) []*Grant {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Grant
	for _, g := range s.data.Grants {
		if g.LibraryID == libraryID && g.State == "active" {
			out = append(out, g)
		}
	}
	return out
}

// Catalog returns direct children of parent. An empty parent means the root.
func (s *Store) Catalog(deviceID, libraryID, parent string) ([]*Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.hasGrantLocked(deviceID, libraryID) {
		return nil, fmt.Errorf("access denied")
	}
	parent = strings.TrimPrefix(path.Clean("/"+parent), "/")
	if parent == "." {
		parent = ""
	}
	var out []*Node
	for _, n := range s.data.Nodes[libraryID] {
		if n.Parent == parent {
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (s *Store) hasGrantLocked(deviceID, libraryID string) bool {
	for _, g := range s.data.Grants {
		if g.DeviceID == deviceID && g.LibraryID == libraryID && g.State == "active" {
			return true
		}
	}
	return false
}

// ScanLibrary walks a library root and returns catalog nodes without IDs.
func ScanLibrary(root string) ([]*Node, error) {
	return scanLibrary("", root)
}

// ReplaceNodes atomically replaces the catalog for a library.
func (s *Store) ReplaceNodes(libraryID string, nodes []*Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Libraries[libraryID]; !ok {
		return fmt.Errorf("library not found")
	}
	for _, n := range nodes {
		n.LibraryID = libraryID
		n.ID = nodeID(libraryID, n.Path)
	}
	s.data.Nodes[libraryID] = nodes
	s.data.Versions[libraryID]++
	return s.saveLocked()
}

// CatalogVersion is incremented on every catalog replacement.
func (s *Store) CatalogVersion(libraryID string) uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Versions[libraryID]
}

func scanLibrary(libraryID, root string) ([]*Node, error) {
	var out []*Node
	skip := map[string]bool{".stfolder": true, ".stversions": true, ".stignore": true}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		if p == root {
			return nil
		}
		name := d.Name()
		if skip[name] || strings.HasPrefix(name, ".syncthing.") && strings.HasSuffix(name, ".tmp") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return nil
		}
		n := &Node{
			LibraryID: libraryID,
			Path:      rel,
			Parent:    parentOf(rel),
			Name:      name,
			IsDir:     d.IsDir(),
			Size:      info.Size(),
			ModTime:   info.ModTime().UTC(),
		}
		if !d.IsDir() {
			n.Size = info.Size()
		} else {
			n.Size = 0
		}
		out = append(out, n)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func parentOf(rel string) string {
	p := path.Dir(rel)
	if p == "." {
		return ""
	}
	return p
}

func nodeID(libraryID, rel string) string {
	h := sha256.Sum256([]byte(libraryID + "\x00" + rel))
	return hex.EncodeToString(h[:8])
}

func overlapping(a, b string) bool {
	a = filepath.Clean(a) + string(os.PathSeparator)
	b = filepath.Clean(b) + string(os.PathSeparator)
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
