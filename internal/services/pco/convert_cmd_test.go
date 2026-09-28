package pco_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/mathpix/mathpix-cli/internal/cli"
	"github.com/mathpix/mathpix-cli/internal/services/pco"
	"github.com/mathpix/mathpix-cli/internal/services/pco/fakepco"
)

// TestMain keeps every test's run index out of the developer's real ~/.mpx.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "pco-cmd-test")
	os.Setenv("MPX_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// run drives `mpx pco ...` through a minimal root, so the tests exercise real flag parsing without
// the self-update hook the production root installs.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	flags := &cli.Flags{}
	root := &cobra.Command{Use: "mpx", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringVar(&flags.Profile, "profile", "", "")
	root.PersistentFlags().StringVar(&flags.AppID, "app-id", "", "")
	root.PersistentFlags().StringVar(&flags.AppKey, "app-key", "", "")
	root.PersistentFlags().StringVar(&flags.Endpoint, "endpoint", "", "")
	root.PersistentFlags().StringVar(&flags.Output, "output", "", "")
	root.PersistentFlags().StringVar(&flags.Verbosity, "verbosity", "", "")
	root.PersistentFlags().BoolVarP(&flags.Quiet, "quiet", "q", false, "")
	root.AddCommand(pco.Command(flags))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

func TestConvertCloudFolderWatchesTheJobToTheEndAndExitsNonZeroOnFailures(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	pco.SetWatchPollForTests(time.Millisecond)
	out, err := run(t, "--endpoint", fake.URL(), "pco", "convert", "s3://acme-docs/batch1/", "--formats", "md")
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 {
		t.Fatalf("a job with a failed file must exit 1, got %v; output:\n%s", err, out)
	}
	if !strings.Contains(out, "job j_1 created") {
		t.Fatalf("expected the created note, got:\n%s", out)
	}
	if !strings.Contains(out, "2 completed, 1 failed") || !strings.Contains(out, "completed_with_failures") {
		t.Fatalf("expected the per-file stream and summary, got:\n%s", out)
	}
}

func TestJobsRetryThenFilesReflectTheRequeue(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	if _, err := run(t, "--endpoint", fake.URL(), "pco", "convert", "s3://acme-docs/batch2/", "--detach"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "--endpoint", fake.URL(), "pco", "jobs", "retry", "j_1", "--error-id", "corrupt_pdf_file")
	if err != nil || !strings.Contains(out, `"requeued": 1`) {
		t.Fatalf("retry: %v\n%s", err, out)
	}
	out, err = run(t, "--endpoint", fake.URL(), "pco", "jobs", "files", "j_1", "--status", "completed")
	if err != nil || strings.Count(out, `"status": "completed"`) != 3 {
		t.Fatalf("after retry all three files are completed:\n%s (%v)", out, err)
	}
}

func TestResumeIsRefusedForACloudFolder(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	_, err := run(t, "--endpoint", fake.URL(), "pco", "convert", "s3://acme-docs/batch1/", "--resume")
	if err == nil || !strings.Contains(err.Error(), "for local runs") {
		t.Fatalf("expected the refusal naming local runs, got %v", err)
	}
}

func TestDetachPrintsAResumeCommandThatFetchesTheOutputs(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "converted")
	out, err := run(t, "--endpoint", fake.URL(), "pco", "convert", dir, outDir, "--detach")
	if err != nil {
		t.Fatalf("detach: %v\n%s", err, out)
	}
	// The printed command is the id alone: the runs index knows where the record is.
	match := regexp.MustCompile(`Fetch them with: mpx pco convert --resume ([0-9a-f]{12})`).FindStringSubmatch(out)
	if match == nil {
		t.Fatalf("the detach summary must print `mpx pco convert --resume RUN_ID`, got:\n%s", out)
	}
	out, err = run(t, "--endpoint", fake.URL(), "pco", "convert", "--resume", match[1])
	if err != nil || !strings.Contains(out, "1 completed") {
		t.Fatalf("resume with the printed command: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(outDir, "a.mmd")); err != nil {
		t.Fatal("resume should have downloaded the output under the record's out dir")
	}
}

func TestOverwriteReprocessesLocallyAndMakesANewCloudJob(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.pdf"), []byte("%PDF"), 0o644)
	if out, err := run(t, "--endpoint", fake.URL(), "pco", "convert", dir); err != nil || !strings.Contains(out, "1 completed") {
		t.Fatalf("first run: %v\n%s", err, out)
	}
	if out, _ := run(t, "--endpoint", fake.URL(), "pco", "convert", dir); !strings.Contains(out, "1 skipped") {
		t.Fatalf("second run should skip:\n%s", out)
	}
	out, err := run(t, "--endpoint", fake.URL(), "pco", "convert", dir, "--overwrite")
	if err != nil || !strings.Contains(out, "1 completed") || fake.Submits != 2 {
		t.Fatalf("--overwrite must resubmit and replace: %v submits=%d\n%s", err, fake.Submits, out)
	}
	if records, _ := filepath.Glob(filepath.Join(dir, "_mathpix", "*")); len(records) != 2 {
		t.Fatalf("an overwrite run keeps its own record beside the first: %v", records)
	}
	run(t, "--endpoint", fake.URL(), "pco", "convert", "s3://acme-docs/again/", "--detach")
	out, _ = run(t, "--endpoint", fake.URL(), "pco", "convert", "s3://acme-docs/again/", "--detach")
	if !strings.Contains(out, "already exists") {
		t.Fatalf("same spec should attach:\n%s", out)
	}
	out, _ = run(t, "--endpoint", fake.URL(), "pco", "convert", "s3://acme-docs/again/", "--detach", "--overwrite")
	if !strings.Contains(out, "created; existing outputs will be replaced") {
		t.Fatalf("--overwrite on a cloud folder must make a new job:\n%s", out)
	}
	if _, err := run(t, "--endpoint", fake.URL(), "pco", "convert", dir, "--overwrite", "--resume"); err == nil {
		t.Fatal("--overwrite with --resume must be refused")
	}
}

func TestSingleImageGoesThroughV3TextAndSurfacesARefusal(t *testing.T) {
	fake := fakepco.New()
	defer fake.Close()
	dir := t.TempDir()
	image := filepath.Join(dir, "scan.jpg")
	if err := os.WriteFile(image, []byte("not really a jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "scan.mmd")
	if _, err := run(t, "--endpoint", fake.URL(), "pco", "convert", image, out); err != nil {
		t.Fatalf("convert image: %v", err)
	}
	if fake.TextRequests != 1 {
		t.Fatalf("a single image should OCR through /v3/text, got %d text requests", fake.TextRequests)
	}
	if body, _ := os.ReadFile(out); string(body) != "fake text for scan.jpg" {
		t.Fatalf("unexpected mmd content: %q", body)
	}
	_, err := run(t, "--endpoint", fake.URL(), "pco", "convert", image, "--options-json", `{"region": {"top_left_x": 0, "top_left_y": 0, "width": 5000, "height": 10}}`)
	if err == nil || !strings.Contains(err.Error(), "opts_value_out_of_range") {
		t.Fatalf("a refused option must surface the deployment's error id, got %v", err)
	}
}
