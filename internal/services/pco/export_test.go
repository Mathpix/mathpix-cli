package pco

import "time"

// SetWatchPollForTests shortens the cloud-job watch interval so tests do not sleep.
func SetWatchPollForTests(d time.Duration) { watchPollInitial = d }
