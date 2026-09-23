package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mathpix/mathpix-cli/internal/config"
)

const (
	repo       = "Mathpix/mathpix-cli"
	checkEvery = 12 * time.Hour
	cacheName  = "update-check.json"
	lockName   = "update.lock"
	lockMaxAge = 30 * time.Minute
	installURL = "https://mathpix.com/mpx-cli/install.sh"
	WorkerName = "_selfupdate"
)

type state struct {
	LastCheck int64  `json:"last_check"`
	Latest    string `json:"latest"`
	Failed    bool   `json:"failed"`
}

type Updater struct {
	version string
	out     io.Writer
	client  *http.Client
}

func New(version string, out io.Writer) *Updater {
	return &Updater{
		version: version,
		out:     out,
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (u *Updater) Trigger() {
	s := u.readState()
	if s.Failed && u.interactive() {
		fmt.Fprintln(u.out, "mpx: an automatic update failed; run `mpx update` to update manually")
		s.Failed = false
		u.writeState(s)
	}
	if !u.autoEnabled() {
		return
	}
	if time.Since(time.Unix(s.LastCheck, 0)) < checkEvery {
		return
	}
	exe, err := exePath()
	if err != nil {
		return
	}
	_ = spawnDetached(exe, WorkerName)
}

func (u *Updater) RunWorker() {
	if !u.acquireLock() {
		return
	}
	defer u.releaseLock()
	s := u.readState()
	s.LastCheck = time.Now().Unix()
	u.writeState(s)
	if !u.versionOK() || !platformSupported() {
		return
	}
	latest, err := u.latest(context.Background())
	if err != nil {
		return
	}
	s.Latest = latest
	if !newer(u.version, latest) {
		s.Failed = false
		u.writeState(s)
		return
	}
	if err := u.apply(context.Background(), latest); err != nil {
		s.Failed = true
		u.writeState(s)
		return
	}
	s.Failed = false
	u.writeState(s)
}

func (u *Updater) Update() error {
	if !u.versionOK() {
		return fmt.Errorf("cannot self-update a dev build; reinstall from %s", installURL)
	}
	if !platformSupported() {
		return fmt.Errorf("self-update is not supported on %s; reinstall from %s", runtime.GOOS, installURL)
	}
	latest, err := u.latest(context.Background())
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	if !newer(u.version, latest) {
		fmt.Fprintf(u.out, "mpx is up to date (%s)\n", u.version)
		return nil
	}
	exe, err := exePath()
	if err != nil {
		return err
	}
	if !dirWritable(filepath.Dir(exe)) {
		return fmt.Errorf("cannot write to %s; reinstall from %s", filepath.Dir(exe), installURL)
	}
	fmt.Fprintf(u.out, "updating mpx %s -> %s\n", u.version, latest)
	if err := u.apply(context.Background(), latest); err != nil {
		return err
	}
	fmt.Fprintf(u.out, "updated to %s\n", latest)
	return nil
}

func (u *Updater) autoEnabled() bool {
	return u.versionOK() && platformSupported() && u.interactive() && !optedOut() && u.exeDirWritable()
}

func (u *Updater) versionOK() bool {
	_, ok := parseVer(u.version)
	return ok
}

func (u *Updater) interactive() bool {
	return isTerminal(u.out) && os.Getenv("CI") == ""
}

func (u *Updater) exeDirWritable() bool {
	exe, err := exePath()
	if err != nil {
		return false
	}
	return dirWritable(filepath.Dir(exe))
}

func optedOut() bool {
	if os.Getenv("MPX_NO_UPDATE") != "" {
		return true
	}
	if strings.EqualFold(os.Getenv("MPX_AUTO_UPDATE"), "off") {
		return true
	}
	p, err := config.Load(config.DefaultProfile)
	if err == nil && strings.EqualFold(p.Setting(config.KeyAutoUpdate), "off") {
		return true
	}
	return false
}

func platformSupported() bool {
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

func (u *Updater) latest(ctx context.Context) (string, error) {
	url := fmt.Sprintf("https://github.com/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := *u.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("no redirect from releases/latest")
	}
	tag := path.Base(loc)
	version := strings.TrimPrefix(tag, "v")
	if _, ok := parseVer(version); !ok {
		return "", fmt.Errorf("unexpected latest tag %q", tag)
	}
	return version, nil
}

func (u *Updater) apply(ctx context.Context, version string) error {
	base := fmt.Sprintf("https://github.com/%s/releases/download/v%s", repo, version)
	archive := fmt.Sprintf("mpx_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	archiveBytes, err := u.get(ctx, base+"/"+archive)
	if err != nil {
		return fmt.Errorf("download %s: %w", archive, err)
	}
	sumsBytes, err := u.get(ctx, base+"/checksums.txt")
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expected := checksumFor(string(sumsBytes), archive)
	if expected == "" {
		return fmt.Errorf("no checksum for %s", archive)
	}
	sum := sha256.Sum256(archiveBytes)
	if hex.EncodeToString(sum[:]) != expected {
		return fmt.Errorf("checksum mismatch for %s", archive)
	}
	bin, err := extractBinary(archiveBytes)
	if err != nil {
		return err
	}
	return replaceExecutable(bin)
}

func (u *Updater) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == "mpx" {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("mpx binary not found in archive")
}

func replaceExecutable(bin []byte) error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".mpx-update-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, exe)
}

func (u *Updater) statePath() string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, cacheName)
}

func (u *Updater) readState() state {
	var s state
	p := u.statePath()
	if p == "" {
		return s
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return s
	}
	json.Unmarshal(data, &s)
	return s
}

func (u *Updater) writeState(s state) {
	dir, err := config.Dir()
	if err != nil {
		return
	}
	os.MkdirAll(dir, 0o700)
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	os.WriteFile(filepath.Join(dir, cacheName), data, 0o644)
}

func (u *Updater) lockPath() string {
	dir, err := config.Dir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, lockName)
}

func (u *Updater) acquireLock() bool {
	p := u.lockPath()
	if p == "" {
		return false
	}
	os.MkdirAll(filepath.Dir(p), 0o700)
	if info, err := os.Stat(p); err == nil && time.Since(info.ModTime()) > lockMaxAge {
		os.Remove(p)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func (u *Updater) releaseLock() {
	if p := u.lockPath(); p != "" {
		os.Remove(p)
	}
}

func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".mpx-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func checksumFor(sums, file string) string {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == file {
			return fields[0]
		}
	}
	return ""
}

func parseVer(v string) ([3]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

func newer(current, candidate string) bool {
	c, ok1 := parseVer(current)
	n, ok2 := parseVer(candidate)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if n[i] != c[i] {
			return n[i] > c[i]
		}
	}
	return false
}
