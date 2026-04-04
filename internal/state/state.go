package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

type State struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Token     string    `json:"token"`
	StartedAt time.Time `json:"started_at"`
	ChromeURL string    `json:"chrome_url"`
}

// StateDir returns the .browse directory path relative to cwd.
func StateDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".browse"), nil
}

// StatePath returns the full path to state.json.
func StatePath() (string, error) {
	dir, err := StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// Read reads the state file. Returns nil if not found.
func Read() (*State, error) {
	path, err := StatePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Write writes the state file atomically.
func Write(s *State) error {
	dir, err := StateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path, err := StatePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Remove deletes the state file.
func Remove() error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// NewToken generates a new bearer token.
func NewToken() string {
	return uuid.New().String()
}

// ServerURL returns the HTTP URL for the server.
func (s *State) ServerURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Port)
}
