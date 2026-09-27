package cli

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
)

//go:embed en.json
var english []byte

type catalog struct {
	Name             string `json:"name"`
	Usage            string `json:"usage"`
	NotReady         string `json:"notReady"`
	InvalidArguments string `json:"invalidArguments"`
}

const Version = "0.0.1"

// Run deliberately has no network, configuration, or process-execution capability.
func Run(args []string, stdout, stderr io.Writer) int {
	var text catalog
	if err := json.Unmarshal(english, &text); err != nil {
		panic(err)
	}
	if len(args) == 1 {
		switch args[0] {
		case "-version", "--version":
			fmt.Fprintf(stdout, "%s %s\n", text.Name, Version)
			return 0
		case "-h", "--help":
			fmt.Fprintln(stdout, text.Usage)
			return 0
		}
	}
	if len(args) > 0 {
		fmt.Fprintln(stderr, text.InvalidArguments)
	} else {
		fmt.Fprintln(stderr, text.NotReady)
	}
	return 2
}
