package cli

import (
	"bytes"
	"testing"
)

func TestUnsupportedOperationsCannotReportSuccess(t *testing.T) {
	for _, args := range [][]string{nil, {"enroll"}, {"--version", "enroll"}, {"__observe-exec-v1", "synthetic-secret"}} {
		var out, err bytes.Buffer
		if code := Run(args, &out, &err); code != 2 || out.Len() != 0 || err.Len() == 0 {
			t.Fatalf("args=%v: code=%d, stdout=%q, stderr=%q", args, code, out.String(), err.String())
		}
		if bytes.Contains(err.Bytes(), []byte("synthetic-secret")) {
			t.Fatal("CLI diagnostic echoed raw arguments")
		}
	}
}
