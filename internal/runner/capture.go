package runner

import (
	"bytes"
	"io"
	"os"
)

type streamResult struct {
	data      []byte
	truncated bool
	failed    bool
}

type capture struct {
	done     chan streamResult
	overflow chan struct{}
}

// A single reader owns each buffer. Only its final immutable value is shared.
func captureStream(reader *os.File, limit int) capture {
	c := capture{make(chan streamResult, 1), make(chan struct{}, 1)}
	go func() {
		result := streamResult{data: make([]byte, 0, limit)}
		var chunk [4096]byte
		for {
			n, err := reader.Read(chunk[:])
			keep := min(n, limit-len(result.data))
			result.data = append(result.data, chunk[:keep]...)
			if keep < n && !result.truncated {
				result.truncated = true
				c.overflow <- struct{}{}
			}
			if err != nil {
				result.failed = err != io.EOF
				c.done <- result
				return
			}
		}
	}()
	return c
}

type controlResult struct {
	completion completion
	valid      bool
}

type controlCapture struct {
	result chan controlResult
	done   chan bool
}

func captureControl(reader *os.File) controlCapture {
	c := controlCapture{make(chan controlResult, 1), make(chan bool, 1)}
	go func() {
		data := make([]byte, 0, 256)
		var b [1]byte
		for len(data) <= controlLimit {
			if _, err := io.ReadFull(reader, b[:]); err != nil {
				c.result <- controlResult{}
				c.done <- err == io.EOF
				return
			}
			data = append(data, b[0])
			if b[0] == '\n' {
				var result completion
				valid := len(data) <= controlLimit && decodeStrict(bytes.TrimSpace(data), &result) == nil && validCompletion(result)
				c.result <- controlResult{result, valid}
				// Additional control data is invalid, even if its first line was valid.
				_, err := reader.Read(b[:])
				c.done <- err == io.EOF
				return
			}
		}
		c.result <- controlResult{}
		c.done <- false
	}()
	return c
}
