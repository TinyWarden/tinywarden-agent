package controlplane

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	rand "math/rand/v2"
	"net"
	"time"
)

func Terminal(err error) bool {
	var response *ResponseError
	if errors.As(err, &response) {
		return response.Status != 429 && response.Status < 500
	}
	var authority x509.UnknownAuthorityError
	var hostname x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verification *tls.CertificateVerificationError
	if errors.As(err, &authority) || errors.As(err, &hostname) ||
		errors.As(err, &invalid) || errors.As(err, &verification) {
		return true
	}
	var readError *ResponseReadError
	if errors.As(err, &readError) {
		return false
	}
	var netError net.Error
	return !errors.As(err, &netError)
}

func RetryWait(err error, base time.Duration) time.Duration {
	delay := base/2 + time.Duration(rand.Int64N(int64(base/2)+1))
	var response *ResponseError
	if errors.As(err, &response) && response.RetryAfter > delay {
		return response.RetryAfter
	}
	return delay
}

func DegradedWait(err error) time.Duration {
	delay := 300*time.Second + time.Duration(rand.Int64N(int64(30*time.Second)+1))
	var response *ResponseError
	if errors.As(err, &response) && response.RetryAfter > delay {
		return response.RetryAfter
	}
	return delay
}
