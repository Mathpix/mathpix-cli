package pco

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mathpix/mathpix-cli/internal/services/pco/batch"
)

// interruptSignals subscribes to Ctrl-C and SIGTERM for the life of a long command. Commands
// that finish in one round trip do not call it and die on Ctrl-C the ordinary way.
func interruptSignals() (<-chan os.Signal, func()) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	return signals, func() { signal.Stop(signals) }
}

// isTerminalFile says whether stdin can be prompted.
func isTerminalFile(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// askYesNo prints the question and reads one line. A second signal while waiting counts as yes.
// Without a terminal on stdin there is nobody to ask, so the answer is yes.
func askYesNo(out io.Writer, in *os.File, signals <-chan os.Signal, question string) bool {
	if !isTerminalFile(in) {
		return true
	}
	fmt.Fprintf(out, "%s [y/N] ", question)
	answer := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(in).ReadString('\n')
		answer <- strings.ToLower(strings.TrimSpace(line))
	}()
	select {
	case line := <-answer:
		return line == "y" || line == "yes"
	case <-signals:
		fmt.Fprintln(out)
		return true
	}
}

// confirmStopLocalRun is the runner's ConfirmStop: it tells the user what has already left the
// workstation and asks whether to end the run.
func confirmStopLocalRun(out io.Writer, signals <-chan os.Signal) func(batch.Snapshot) bool {
	return func(snap batch.Snapshot) bool {
		fmt.Fprintf(out, "\ninterrupted: %d of %d files submitted (%d completed, %d failed). No more files will be sent while you decide.\n",
			snap.Submitted, snap.Total, snap.Completed, snap.Failed)
		fmt.Fprintf(out, "Submitted documents keep processing on the deployment and cannot be retracted; the run record is %s.\n", snap.RunDir)
		return askYesNo(out, os.Stdin, signals, "Stop this run?")
	}
}
