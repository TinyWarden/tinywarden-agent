package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
)

// Give command adapters a safe category without exposing URLs, secrets or raw HTTP errors.
func ErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	var response *ResponseError
	if errors.As(err, &response) {
		if response.Status == 401 {
			return "unauthorized"
		}
		if response.Status == 409 {
			return "conflict"
		}
		return fmt.Sprintf("server_%d", response.Status)
	}
	var netError net.Error
	if errors.As(err, &netError) {
		return "network"
	}
	return "unavailable"
}
