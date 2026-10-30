package controlplane

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"time"
)

var workCursor = regexp.MustCompile(`^[a-f0-9]{64}$`)

type WorkHint struct {
	Available bool
	Cursor    string
}

// WorkWaitClient has independent timeouts; waiting cannot occupy ordinary polling.
func (client *Client) WorkWaitClient() *Client {
	httpClient := *client.HTTP
	transport, ok := httpClient.Transport.(*http.Transport)
	if !ok {
		// Retain the agent's no-proxy/TLS policy for a fresh fallback transport.
		transport = NewClient(client.Origin).HTTP.Transport.(*http.Transport)
	}
	cloned := transport.Clone()
	cloned.ResponseHeaderTimeout = 30 * time.Second
	httpClient.Transport, httpClient.Timeout = cloned, 35*time.Second
	return &Client{Origin: client.Origin, HTTP: &httpClient}
}

// WaitForWork returns only a hint. Ordinary discovery retains all authority.
func (client *Client) WaitForWork(ctx context.Context, credential string, cursor *string, lane string) (WorkHint, error) {
	input := map[string]any{"schema_version": 1, "wait_seconds": 25, "cursor": cursor}
	if lane == "monitoring" {
		input["lane"] = lane
	} else if lane != "maintenance" {
		return WorkHint{}, errors.New("invalid_work_lane")
	}
	var reply struct {
		Schema    int    `json:"schema_version"`
		Available *bool  `json:"available"`
		Cursor    string `json:"cursor"`
	}
	_, err := client.Post(ctx, "/api/v2/agent/work-wait", credential, input, false, func(raw []byte) error {
		if err := Decode(raw, &reply); err != nil {
			return err
		}
		if reply.Schema != 1 || reply.Available == nil || !workCursor.MatchString(reply.Cursor) {
			return errors.New("invalid_work_reply")
		}
		return nil
	}, 1024, 1024)
	if err != nil {
		return WorkHint{}, err
	}
	return WorkHint{Available: *reply.Available, Cursor: reply.Cursor}, nil
}
