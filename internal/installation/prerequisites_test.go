package installation

import (
	"strings"
	"testing"
)

func TestProgramPrerequisitesOnlySuggestMissingPackages(t *testing.T) {
	for _, test := range []struct {
		name    string
		missing []string
		command string
	}{
		{"ready", nil, ""},
		{"python", []string{"/usr/bin/python3.13"}, "apt install python3"},
		{"sandbox", []string{"/usr/bin/bwrap"}, "apt install bubblewrap"},
		{"account tools", []string{"/usr/bin/getent", "/usr/sbin/runuser"}, "apt install libc-bin util-linux"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := programPrerequisites(func(path string) bool {
				for _, missing := range test.missing {
					if path == missing {
						return false
					}
				}
				return true
			})
			if test.command == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			want := "missing prerequisites: " + strings.Join(test.missing, ", ") + "; install missing packages: " + test.command
			if err == nil || err.Error() != want {
				t.Fatalf("got %v, want %s", err, want)
			}
		})
	}
}
