// Package config persists settings and schedules as JSON in the user's
// config folder (~/Library/Application Support/TeamsStatusScheduler on macOS,
// %AppData%\TeamsStatusScheduler on Windows).
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"teamsstatus/internal/schedule"
)

const AppName = "TeamsStatusScheduler"

// Target selects which Teams to drive.
const (
	TargetDesktop = "desktop" // the new Teams desktop app
	TargetBrowser = "browser" // Teams web in a browser window
)

// Keep-awake modes.
const (
	AwakeOff       = "off"
	AwakeScheduled = "scheduled" // only while a schedule block is active
	AwakeAlways    = "always"    // whenever the app is running
)

type Settings struct {
	Target               string `json:"target"`
	CheckIntervalSeconds int    `json:"checkIntervalSeconds"`
	ResetOnExit          bool   `json:"resetOnExit"`
	StartHidden          bool   `json:"startHidden"`
	Paused               bool   `json:"paused"`
	KeepAwake            string `json:"keepAwake"`
	KeepDisplayAwake     bool   `json:"keepDisplayAwake"`
}

type Config struct {
	Version   int                 `json:"version"`
	Settings  Settings            `json:"settings"`
	Schedules []schedule.Schedule `json:"schedules"`
}

// Store guards the config and saves it on every change.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// Dir is the per-user data folder.
func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, AppName)
}

func defaults() Config {
	weekdays := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday}
	return Config{
		Version: 1,
		Settings: Settings{
			Target:               TargetDesktop,
			CheckIntervalSeconds: 60,
			ResetOnExit:          true,
			KeepAwake:            AwakeOff,
		},
		Schedules: []schedule.Schedule{{
			ID: NewID(), Name: "Standard work week", Priority: 10, Enabled: false,
			Blocks: []schedule.Block{
				{Days: weekdays, Start: "09:00", End: "12:00", Presence: schedule.Available},
				{Days: weekdays, Start: "12:00", End: "13:00", Presence: schedule.Away},
				{Days: weekdays, Start: "13:00", End: "17:00", Presence: schedule.Available},
				{Days: weekdays, Start: "17:00", End: "09:00", Presence: schedule.Offline},
			},
		}},
	}
}

// NewID returns a random schedule ID.
func NewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Load reads the config at path, creating defaults if it doesn't exist.
func Load(path string) (*Store, error) {
	s := &Store{path: path, cfg: defaults()}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	cfg := defaults()
	cfg.Schedules = nil
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Settings.CheckIntervalSeconds < 15 {
		cfg.Settings.CheckIntervalSeconds = 60
	}
	s.cfg = cfg
	return s, nil
}

// Get returns a deep copy of the current config.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var c Config
	data, _ := json.Marshal(s.cfg)
	_ = json.Unmarshal(data, &c)
	return c
}

// Update applies fn to the config and saves it.
func (s *Store) Update(fn func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	backup := s.cfg
	if err := fn(&s.cfg); err != nil {
		s.cfg = backup
		return err
	}
	return s.save()
}

func (s *Store) save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
