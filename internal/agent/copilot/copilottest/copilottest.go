// Package copilottest provides a fake Copilot executable for tests. A test binary becomes the fake
// when it is copied to a file named fake-copilot*, so no live Copilot is ever needed.
package copilottest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// InstructionsName is the file name of the session Git instructions the fake looks for.
const InstructionsName = "mint-git.instructions.md"

// Snapshot is what the fake prints for a launch: the process state the child observed.
type Snapshot struct {
	Settings     map[string]any    `json:"settings"`
	Env          map[string]string `json:"env"`
	CWD          string            `json:"cwd"`
	Instructions string            `json:"instructions"`
	Args         []string          `json:"args"`
}

// IsFake reports whether the running test binary was started as the fake Copilot.
// Call it first in TestMain: if it returns true, os.Exit(copilottest.Run()).
func IsFake() bool { return strings.HasPrefix(filepath.Base(os.Args[0]), "fake-copilot") }

// Binary copies the running test binary to a fake-copilot executable and returns its path.
func Binary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	data, err := os.ReadFile(self)
	require.NoError(t, err)
	name := "fake-copilot"
	if os.PathSeparator == '\\' {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, data, 0o700))
	return path
}

// Run is the fake's main function and returns its exit code.
func Run() int {
	args := os.Args[1:]
	if slices.Contains(os.Args[1:], "--version") {
		fmt.Println("GitHub Copilot CLI 1.0.112.")
		return 0
	}
	if slices.Equal(args, []string{"config", "sandbox"}) {
		home := os.Getenv("COPILOT_HOME")
		data, err := os.ReadFile(filepath.Join(home, "settings.json"))
		if err != nil {
			return 91
		}
		var settings map[string]json.RawMessage
		if json.Unmarshal(data, &settings) != nil || len(settings["sandbox"]) == 0 {
			return 91
		}
		fmt.Println(string(settings["sandbox"]))
		return 0
	}
	if len(args) >= 3 && args[0] == "config" && args[1] == "--global" {
		return globalConfig(args[2:])
	}
	snapshot := Snapshot{Args: os.Args[1:], Env: make(map[string]string)}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		snapshot.Env[key] = value
	}
	var err error
	snapshot.CWD, err = os.Getwd()
	if err != nil {
		return 90
	}
	if home := os.Getenv("COPILOT_HOME"); home != "" {
		data, err := os.ReadFile(filepath.Join(home, "settings.json"))
		if err != nil {
			return 91
		}
		if json.Unmarshal(data, &snapshot.Settings) != nil {
			return 91
		}
	}
	if dirs := os.Getenv("COPILOT_CUSTOM_INSTRUCTIONS_DIRS"); dirs != "" {
		parts := strings.Split(dirs, ",")
		data, err := os.ReadFile(filepath.Join(parts[len(parts)-1], InstructionsName))
		if err != nil {
			return 93
		}
		snapshot.Instructions = string(data)
	}
	if json.NewEncoder(os.Stdout).Encode(snapshot) != nil {
		return 92
	}
	if slices.Contains(os.Args[1:], "--prompt=exit-seven") {
		return 7
	}
	return 0
}

func globalConfig(args []string) int {
	home := os.Getenv("COPILOT_HOME")
	if home == "" {
		return 91
	}
	path := filepath.Join(home, "settings.json")
	settings := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(data, &settings) != nil {
			return 91
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 91
	}
	if len(args) == 2 && args[0] == "--rm" {
		deleteSetting(settings, strings.Split(args[1], "."))
	} else if len(args) == 2 {
		var value any
		if json.Unmarshal([]byte(args[1]), &value) != nil {
			value = args[1]
		}
		setSetting(settings, strings.Split(args[0], "."), value)
	} else {
		return 91
	}
	data, err := json.Marshal(settings)
	if err != nil || os.MkdirAll(home, 0o700) != nil || os.WriteFile(path, data, 0o600) != nil {
		return 91
	}
	return 0
}

func setSetting(settings map[string]any, path []string, value any) {
	if len(path) == 1 {
		if path[0] == "injectHosts" {
			hosts, _ := settings[path[0]].([]any)
			settings[path[0]] = append(hosts, value)
		} else {
			settings[path[0]] = value
		}
		return
	}
	child, _ := settings[path[0]].(map[string]any)
	if child == nil {
		child = make(map[string]any)
		settings[path[0]] = child
	}
	setSetting(child, path[1:], value)
}

func deleteSetting(settings map[string]any, path []string) {
	if len(path) == 1 {
		delete(settings, path[0])
		return
	}
	child, _ := settings[path[0]].(map[string]any)
	if child != nil {
		deleteSetting(child, path[1:])
	}
}
