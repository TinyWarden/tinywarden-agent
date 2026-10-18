package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResourceNegotiationPreservesManualAndOldResponses(t *testing.T) {
	var response packageResponse
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-TinyWarden-Capabilities") != "skill_runs.manual.v1" || r.Header.Get("X-TinyWarden-Package-Features") != "skill_resources.python313.v1" {
			t.Error("lost feature negotiation")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	lane := packageFixture(t, server)
	response = *lane.ledger.Cache
	// Old control planes ignore the feature header and keep schema 1.
	if _, e := lane.client.fetchPackages(context.Background(), lane.state, true, true); e != nil {
		t.Fatal(e)
	}
	response.SchemaVersion = 2
	response.Features = []string{"skill_resources.python313.v1"}
	response.Assignments[0].Format = 2
	if _, e := lane.client.fetchPackages(context.Background(), lane.state, true, true); e != nil {
		t.Fatal(e)
	}
	response.Features = nil
	if _, e := lane.client.fetchPackages(context.Background(), lane.state, true, true); e == nil {
		t.Fatal("accepted SDK2 without negotiated resources")
	}
	response.SchemaVersion = 1
	if validPackageResponse(lane.ledger.Scope, response) {
		t.Fatal("accepted SDK2 in v1 response")
	}
}
