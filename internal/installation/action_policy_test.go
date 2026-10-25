package installation

import (
	"testing"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
)

func TestActionPolicyRequiresExplicitOriginAndClass(t *testing.T) {
	policy := actionPolicy{Schema: 1, Origins: []actioncontract.OriginPermission{}}
	for _, values := range [][3]string{{"bad:owner", "host_admin", ""}, {"12:34", "shell", ""}, {"12:34", "host_admin", "unprivileged"}, {"12:34", "", ""}, {"bad:bad", "", "host_admin"}} {
		if _, err := changeActionPolicy(policy, values[0], values[1], values[2]); err == nil {
			t.Fatal("invalid policy change accepted", values)
		}
	}
	allowed, err := changeActionPolicy(policy, "12:34", "host_admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Origins) != 0 || len(allowed.Origins) != 1 || allowed.Origins[0].Classes[0] != "host_admin" {
		t.Fatal("authority changed unexpectedly")
	}
	allowed, err = changeActionPolicy(allowed, "12:34", "host_admin", "")
	if err != nil || len(allowed.Origins[0].Classes) != 1 {
		t.Fatal("repeat widened permission")
	}
	denied, err := changeActionPolicy(allowed, "12:34", "", "host_admin")
	if err != nil || len(denied.Origins) != 0 {
		t.Fatal("deny did not remove authority")
	}
}
