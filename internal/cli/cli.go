package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/dotaikit/browse/internal/state"
)

type commandRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Run sends a command to the server and prints the result.
func Run(command string, args []string) error {
	st, err := ensureServer()
	if err != nil {
		return err
	}

	// Send command
	body, err := json.Marshal(commandRequest{Command: command, Args: args})
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", st.ServerURL()+"/command", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+st.Token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("server request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp map[string]string
		if json.Unmarshal(data, &errResp) == nil {
			if msg, ok := errResp["error"]; ok {
				return fmt.Errorf("%s", msg)
			}
		}
		return fmt.Errorf("server error (%d): %s", resp.StatusCode, string(data))
	}

	if command == "restart" {
		fmt.Print(string(data))

		if err := waitForServerStop(st, 8*time.Second); err != nil {
			return err
		}
		if err := startServer(); err != nil {
			return err
		}
		restarted, err := waitForServer(12 * time.Second)
		if err != nil {
			return err
		}
		fmt.Printf("\nRestarted browse server (pid=%d, port=%d)", restarted.PID, restarted.Port)
		return nil
	}

	// Print result to stdout (plain text)
	fmt.Print(string(data))
	return nil
}

func isHealthy(st *state.State) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(st.ServerURL() + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func ensureServer() (*state.State, error) {
	st, err := state.Read()
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if st != nil && isHealthy(st) {
		return st, nil
	}
	if st != nil {
		_ = state.Remove()
	}

	if err := startServer(); err != nil {
		return nil, err
	}

	st, err = waitForServer(12 * time.Second)
	if err != nil {
		return nil, err
	}
	return st, nil
}

func startServer() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate browse executable: %w", err)
	}

	cmd := exec.Command(exe, "serve")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("auto-start browse server: %w", err)
	}
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	return nil
}

func waitForServer(timeout time.Duration) (*state.State, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := state.Read()
		if err == nil && st != nil && isHealthy(st) {
			return st, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil, fmt.Errorf("browse server did not auto-start. Ensure Chrome is running with --remote-debugging-port=9222, then run 'browse serve'")
}

func waitForServerStop(previous *state.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := state.Read()
		if err != nil {
			return fmt.Errorf("read state while waiting for shutdown: %w", err)
		}
		if st == nil {
			return nil
		}
		if st.PID != previous.PID || st.Port != previous.Port || st.Token != previous.Token {
			return nil
		}
		if !isHealthy(st) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("browse server did not stop in time")
}
