package managed

import (
	"github.com/TinyWarden/tinywarden-agent/internal/platform"
	"testing"
)

func TestReconnectChecksTargetBeforeCredentialConsumption(t *testing.T) {
	setup := platform.Setup{Origin: "https://app.example", Token: "unused"}
	intent := reconnectIntent{Schema: 1, HostID: "host-a", AgentID: "agent-a", Purpose: "restore", Generation: "2"}
	if e := checkReconnect(setup.Origin, setup, "host-a", "agent-a", 2, intent); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*reconnectIntent){func(i *reconnectIntent) { i.HostID = "host-b" }, func(i *reconnectIntent) { i.AgentID = "agent-b" }, func(i *reconnectIntent) { i.Purpose = "new" }, func(i *reconnectIntent) { i.Schema = 2 }, func(i *reconnectIntent) { i.Generation = "1" }} {
		bad := intent
		change(&bad)
		if checkReconnect(setup.Origin, setup, "host-a", "agent-a", 2, bad) == nil {
			t.Fatal("wrong setup target accepted")
		}
	}
	if checkReconnect("https://other.example", setup, "host-a", "agent-a", 2, intent) == nil {
		t.Fatal("wrong origin accepted")
	}
}
