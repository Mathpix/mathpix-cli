// Package config reads and writes mpx credentials and settings as two INI-style files under a
// config directory, each split into named profiles. Credentials
// (app_id, app_key) live in `credentials`; everything else (endpoint, output format, ...) lives in
// `config`. Nothing here decides precedence against flags or the environment; that is cli.Resolve's
// job. This package only owns the files.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultProfile is the profile used when none is named by flag or environment.
const DefaultProfile = "default"

// Fields stored in the credentials file, kept apart from settings so a leaked config file carries
// no secrets.
const (
	KeyAppID  = "app_id"
	KeyAppKey = "app_key"
)

// Fields stored in the config file.
const (
	KeyEndpoint  = "endpoint"
	KeyOutput    = "output"
	KeyVerbosity = "verbosity"
)

// Dir returns the config directory: $MPX_CONFIG_DIR if set, else ~/.mpx.
func Dir() (string, error) {
	if d := os.Getenv("MPX_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory (set MPX_CONFIG_DIR): %w", err)
	}
	return filepath.Join(home, ".mpx"), nil
}

func credentialsPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "credentials"), nil
}

func configPath() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config"), nil
}

// Profile is the merged view of one profile across both files: credentials and settings together.
type Profile struct {
	Name        string
	Credentials map[string]string
	Settings    map[string]string
}

// Cred returns a credentials value (app_id, app_key) or "".
func (p Profile) Cred(key string) string { return p.Credentials[key] }

// Setting returns a settings value (endpoint, output, ...) or "".
func (p Profile) Setting(key string) string { return p.Settings[key] }

// Load reads one profile from both files. Missing files are not an error; the profile just comes
// back empty, which is what a first run looks like.
func Load(profile string) (Profile, error) {
	if profile == "" {
		profile = DefaultProfile
	}
	out := Profile{Name: profile, Credentials: map[string]string{}, Settings: map[string]string{}}
	cp, err := credentialsPath()
	if err != nil {
		return out, err
	}
	cfgp, err := configPath()
	if err != nil {
		return out, err
	}
	creds, err := readINI(cp)
	if err != nil {
		return out, err
	}
	settings, err := readINI(cfgp)
	if err != nil {
		return out, err
	}
	if section, ok := creds[profile]; ok {
		out.Credentials = section
	}
	if section, ok := settings[profile]; ok {
		out.Settings = section
	}
	return out, nil
}

// Save writes the given credentials and settings into one profile, merging into whatever the files
// already hold for other profiles. Directory is created 0700 and the credentials file 0600 so
// secrets are not world-readable.
func Save(profile string, credentials, settings map[string]string) error {
	if profile == "" {
		profile = DefaultProfile
	}
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := mergeSection(filepath.Join(dir, "credentials"), profile, credentials, 0o600); err != nil {
		return err
	}
	return mergeSection(filepath.Join(dir, "config"), profile, settings, 0o644)
}

func mergeSection(path, profile string, values map[string]string, mode os.FileMode) error {
	existing, err := readINI(path)
	if err != nil {
		return err
	}
	if existing == nil {
		existing = map[string]map[string]string{}
	}
	if existing[profile] == nil {
		existing[profile] = map[string]string{}
	}
	for k, v := range values {
		if v == "" {
			delete(existing[profile], k)
			continue
		}
		existing[profile][k] = v
	}
	return writeINI(path, existing, mode)
}

// readINI parses a minimal INI file: `[section]` headers and `key = value` lines, with `#` and `;`
// comments. A missing file returns an empty map, not an error.
func readINI(path string) (map[string]map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()
	out := map[string]map[string]string{}
	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			if out[section] == nil {
				out[section] = map[string]string{}
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || section == "" {
			continue
		}
		out[section][strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out, scanner.Err()
}

func writeINI(path string, data map[string]map[string]string, mode os.FileMode) error {
	sections := make([]string, 0, len(data))
	for s := range data {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	var b strings.Builder
	for i, s := range sections {
		if len(data[s]) == 0 {
			continue
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "[%s]\n", s)
		keys := make([]string, 0, len(data[s]))
		for k := range data[s] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s = %s\n", k, data[s][k])
		}
	}
	return os.WriteFile(path, []byte(b.String()), mode)
}
