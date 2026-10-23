package controlplane

import (
	"encoding/base64"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

const SetupPrefix = "tw_setup_v1."

var enrollmentPattern = regexp.MustCompile(`^tw_e_[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}\.[A-Za-z0-9_-]{43}$`)

type Setup struct {
	Origin string `json:"origin"`
	Token  string `json:"token"`
}

func Origin(s string) (string, error) {
	if len(s) > 1024 || s == "" || strings.IndexFunc(s, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return "", errors.New("invalid_origin")
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || strings.TrimSuffix(s, "/") != "https://"+u.Host {
		return "", errors.New("invalid_origin")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 || n == 443 || strconv.Itoa(n) != port {
			return "", errors.New("invalid_origin")
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid_origin")
	}
	if u.Host != strings.ToLower(u.Host) {
		return "", errors.New("invalid_origin")
	}
	return strings.TrimSuffix(s, "/"), nil
}

func ParseSetup(code string) (Setup, error) {
	var result Setup
	if len(code) > 4096 {
		return result, errors.New("invalid_setup_code")
	}
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, SetupPrefix) {
		return result, errors.New("unsupported_setup_code")
	}
	encoded := strings.TrimPrefix(code, SetupPrefix)
	data, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(data) > 3072 || base64.RawURLEncoding.EncodeToString(data) != encoded {
		return result, errors.New("invalid_setup_code")
	}
	if err := agentrelease.Decode(data, &result); err != nil {
		return Setup{}, errors.New("invalid_setup_code")
	}
	result.Origin, err = Origin(result.Origin)
	if err != nil || !enrollmentPattern.MatchString(result.Token) {
		return Setup{}, errors.New("invalid_setup_code")
	}
	secret := strings.Split(result.Token, ".")[1]
	b, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil || len(b) != 32 {
		return Setup{}, errors.New("invalid_setup_code")
	}
	return result, nil
}
