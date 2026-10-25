package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSDK3NegotiationKeepsMonitoringAndResourcesSeparate(t *testing.T) {
	var response packageResponse
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TinyWarden-Package-Features") != "skill_resources.python313.v1,skill_packages.sdk3.v1" {
			t.Error("lost SDK3 negotiation")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	lane := packageFixture(t, server)
	response = *lane.ledger.Cache
	response.SchemaVersion = 2
	response.Features = []string{"skill_resources.python313.v1", "skill_packages.sdk3.v1"}
	response.Assignments[0].Format = 3
	if _, err := lane.client.fetchPackages(context.Background(), lane.state, true, true, true); err != nil {
		t.Fatal(err)
	}
	if response.Assignments[0].resources() {
		t.Fatal("scalar SDK3 became a resource run")
	}
	response.Assignments[0].ResourceMode = true
	if _, err := lane.client.fetchPackages(context.Background(), lane.state, true, true, true); err != nil {
		t.Fatal(err)
	}
	if !response.Assignments[0].resources() {
		t.Fatal("SDK3 resource contract ignored")
	}
	response.Features = []string{"skill_packages.sdk3.v1"}
	if validPackageResponse(lane.ledger.Scope, response) {
		t.Fatal("resource mode without resource feature accepted")
	}
	response.Assignments[0].ResourceMode = false
	response.Features = []string{"skill_resources.python313.v1"}
	if validPackageResponse(lane.ledger.Scope, response) {
		t.Fatal("SDK3 without feature accepted")
	}
	response.Features = []string{"skill_packages.sdk3.v1", "unknown.extension"}
	if validPackageResponse(lane.ledger.Scope, response) {
		t.Fatal("unknown feature accepted")
	}
}
