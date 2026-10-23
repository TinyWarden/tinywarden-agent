package controlplane

import (
	"time"
)

func WorkerRetry(err error, failures int) time.Duration {
	if failures > 4 {
		return DegradedWait(err)
	}
	return RetryWait(err, time.Duration(2<<min(failures-1, 3))*time.Second)
}

// Every mutation is owned by this tick. Network/runner workers return one bounded
// result on private buffered channels; none can mutate cache, queue or sequence.
