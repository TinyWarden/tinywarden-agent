package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestCollectorHelperProcess(t *testing.T) {
	if os.Getenv("TW_TEST_DISK_HELPER") == "" {
		return
	}
	switch os.Getenv("TW_TEST_DISK_HELPER") {
	case "slow":
		time.Sleep(30 * time.Second)
	case "overflow":
		os.Stdout.Write(bytes.Repeat([]byte("x"), maxCollectorOutput+1))
	case "ready":
		json.NewEncoder(os.Stdout).Encode(CollectorResult{Coverage: "complete", Reason: "none", Mounts: []DiskMount{}})
	}
	os.Exit(0)
}

func helperCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestCollectorHelperProcess")
	cmd.Env = append(os.Environ(), "TW_TEST_DISK_HELPER="+mode)
	return cmd
}

func TestDiskHelperTimeoutCapAndOneOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan CollectorResult, 1)
	firstCommand := helperCommand(t, "slow")
	go func() { first <- runCollectorCommand(ctx, firstCommand, 300*time.Millisecond) }()
	time.Sleep(60 * time.Millisecond)
	if got := runCollectorCommand(ctx, helperCommand(t, "ready"), time.Second); got.Reason != "collector_timeout" {
		t.Fatalf("second helper started while first owns slot: %+v", got)
	}
	if got := <-first; got.Reason != "collector_timeout" {
		t.Fatalf("timeout %+v", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(collectorSlot) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := runCollectorCommand(ctx, helperCommand(t, "ready"), 3*time.Second); got.Coverage != "complete" {
		t.Fatalf("helper did not recover: %+v", got)
	}
	if got := runCollectorCommand(ctx, helperCommand(t, "overflow"), 3*time.Second); got.Reason != "output_overflow" {
		t.Fatalf("output cap %+v", got)
	}
	cancel()
	if got := runCollectorCommand(ctx, helperCommand(t, "slow"), time.Second); got.Reason != "collector_failed" {
		t.Fatalf("stop did not cancel helper: %+v", got)
	}
}
