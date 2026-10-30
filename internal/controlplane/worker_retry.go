package controlplane

import (
	"time"
)

func WorkerRetry(err error, failures int) time.Duration {
	// Monitoring must recover promptly after a short app/network interruption.
	// RetryWait still respects any explicit server-directed cooldown.
	return RetryWait(err, time.Duration(min(2<<min(max(failures-1, 0), 3), 10))*time.Second)
}
