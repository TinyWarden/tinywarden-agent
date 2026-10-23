package host

import (
	"errors"
	"os"
	"runtime"
	"strings"
)

func Metadata() (string, string, string, string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", "", "", "", err
	}
	if len(hostname) < 1 || len(hostname) > 253 {
		return "", "", "", "", errors.New("invalid hostname")
	}
	for _, char := range hostname {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '.' && char != '-' {
			return "", "", "", "", errors.New("invalid hostname")
		}
	}
	contents, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "", "", "", "", err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(contents), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found && (key == "ID" || key == "VERSION_ID") {
			values[key] = strings.Trim(value, `"'`)
		}
	}
	if values["ID"] != "debian" || values["VERSION_ID"] != "13" {
		return "", "", "", "", errors.New("unsupported distribution")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return "", "", "", "", errors.New("unsupported architecture")
	}
	return hostname, values["ID"], values["VERSION_ID"], runtime.GOARCH, nil
}
