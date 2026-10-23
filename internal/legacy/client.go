package legacy

import (
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

const Version = agentrelease.Version

type Client struct{ controlplane.Client }

func NewClient(origin string) *Client { return &Client{Client: *controlplane.NewClient(origin)} }
