// Package apt owns the privileged Debian adapter, independent of monitoring SDK assets.
package apt

import (
	"embed"
	"errors"
	"io/fs"
	"sort"
	"strings"
)

//go:embed helper/*.py
var helpers embed.FS

// Source is embedded into the signed executable; no host/user-supplied import path.
func Source() (string, error) {
	names, err := fs.Glob(helpers, "helper/*.py")
	if err != nil || len(names) != 9 {
		return "", errors.New("maintenance_adapter_invalid")
	}
	sort.Strings(names)
	var source strings.Builder
	for _, name := range names {
		data, err := helpers.ReadFile(name)
		if err != nil {
			return "", err
		}
		source.Write(data)
		source.WriteByte('\n')
	}
	return source.String(), nil
}
