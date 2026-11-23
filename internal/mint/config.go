package mint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type config struct {
	Copilot       string `json:"copilot"`
	Remote        string `json:"remote"`
	LegacyKeyFile string `json:"key_file,omitempty"`
}

func configDirectory() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", errors.New("cannot locate the user configuration directory")
	}
	return filepath.Join(base, "gh-mint"), nil
}

func readConfig(dir string) (config, error) {
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return config{}, errors.New("cannot read configuration; run 'mint setup'")
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, errors.New("invalid mint configuration")
	}
	if err := cfg.validate(); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func (c config) validate() error {
	if !filepath.IsAbs(c.Copilot) {
		return errors.New("copilot path must be absolute")
	}
	if c.Remote == "" || strings.HasPrefix(c.Remote, "-") || strings.ContainsAny(c.Remote, "\x00\r\n") {
		return errors.New("invalid Git remote name")
	}
	return nil
}

func writeConfig(dir string, cfg config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the configuration directory")
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "config.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create configuration; existing configuration is never overwritten by setup")
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("cannot write configuration")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("cannot persist configuration")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return errors.New("cannot finish writing configuration")
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New("cannot resolve path")
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path %q", absolute)
	}
	return resolved, nil
}

func within(parent, path string) bool {
	relative, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
