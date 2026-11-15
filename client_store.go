package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const clientHistoryLimit = 60

type clientSettings struct {
	Language    string
	Address     string
	Port        string
	Tests       [3]bool
	Connections int
	Seconds     int
	Unit        int
}

type clientRecord struct {
	Time     time.Time
	Target   string
	Settings clientSettings
	Latency  *latencyResult
	Download *speedResult
	Upload   *speedResult
	Curves   [2][]rateSample
	Status   string // statusDone / statusStopped / failed
	Error    string
}

type clientState struct {
	Version  int
	Settings clientSettings
	History  []clientRecord
}

func defaultClientState() clientState {
	return clientState{Version: 1, Settings: clientSettings{
		Language: "zh", Port: "8080", Tests: [3]bool{true, true, true}, Connections: 4, Seconds: 10,
	}}
}

func clientStatePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "kuainet", "client.json"), nil
}

func loadClientState(path string) (clientState, error) {
	state := defaultClientState()
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	defer file.Close()
	var loaded clientState
	if err := json.NewDecoder(io.LimitReader(file, 16*1024*1024)).Decode(&loaded); err != nil {
		return state, err
	}
	if loaded.Version != 1 {
		return state, fmt.Errorf("unsupported client data version %d", loaded.Version)
	}
	if _, ok := clientLocales.Texts[loaded.Settings.Language]; !ok {
		loaded.Settings.Language = "zh"
	}
	if loaded.Settings.Port == "" {
		loaded.Settings.Port = "8080"
	}
	if !containsInt(connectionChoices, loaded.Settings.Connections) {
		loaded.Settings.Connections = 4
	}
	if !containsInt([]int{10, 15, 30}, loaded.Settings.Seconds) {
		loaded.Settings.Seconds = 10
	}
	if loaded.Settings.Unit < 0 || loaded.Settings.Unit > 2 {
		loaded.Settings.Unit = 0
	}
	if len(loaded.History) > clientHistoryLimit {
		loaded.History = loaded.History[:clientHistoryLimit]
	}
	return loaded, nil
}

func containsInt(values []int, value int) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func saveClientState(path string, state clientState) error {
	if path == "" {
		return nil // Explicitly disabled for in-memory/test sessions.
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".client-*.json")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
