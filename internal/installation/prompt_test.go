package installation

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type failedInput struct{}

func (failedInput) Read([]byte) (int, error) { return 0, errors.New("private input failure") }

func TestSetupCodeInput(t *testing.T) {
	for _, ending := range []string{"", "\n", "\r\n"} {
		for _, code := range []string{"tw_setup_v1.synthetic", strings.Repeat("x", 4096)} {
			got, err := readSetup("", strings.NewReader(code+ending))
			if err != nil || got != code {
				t.Fatalf("valid bounded input rejected: %v", err)
			}
		}
	}
	for _, input := range []string{"", "\n", "code\nsecond", "code\n\n", strings.Repeat("x", 4097), strings.Repeat("x", 100000)} {
		if _, err := readSetup("", strings.NewReader(input)); err == nil || err.Error() != "invalid_setup_code" {
			t.Fatal("invalid input accepted or exposed")
		}
	}
	if _, err := readSetup("", failedInput{}); err == nil || err.Error() != "invalid_setup_code" {
		t.Fatal("reader failure exposed or accepted")
	}
	if _, err := readSetup("/not-opened", strings.NewReader("code")); err == nil {
		t.Fatal("mixed input accepted")
	}
}

func TestInstallSetupInputConflicts(t *testing.T) {
	for _, options := range []InstallOptions{
		{CodeInput: strings.NewReader("code"), CodeFile: "/not-opened"},
		{CodeInput: strings.NewReader("code"), Adopt: true},
	} {
		if err := Install(context.Background(), options); err == nil || err.Error() != "invalid_setup_input_options" {
			t.Fatal("input conflict reached release verification or installation")
		}
	}
}

// Invoked in a real terminal by the release check; no host installation occurs.
func TestSetupInteractivePrompt(t *testing.T) {
	if os.Getenv("TW_TEST_INTERACTIVE_SETUP") != "1" {
		return
	}
	code, err := readSetup("", nil)
	if err != nil || code != "tw_setup_v1.synthetic-interactive\n" {
		t.Fatal("interactive setup input failed")
	}
}

func TestStdinFlagRejectsOtherModes(t *testing.T) {
	if os.Geteuid() != 0 {
		// Dispatch's root gate is verified separately; the release check runs
		// only this argument-rejection case as root with no valid install inputs.
		return
	}
	copy := SetupMessages{Arguments: "invalid input options"}
	for _, args := range [][]string{
		{"install", "--setup-code-stdin", "--setup-code-file", "/not-opened"},
		{"install", "--setup-code-stdin", "--adopt"},
		{"reconnect", "--setup-code-stdin"},
		{"update", "--setup-code-stdin"},
		{"repair", "--setup-code-stdin"},
		{"auto-updates", "--setup-code-stdin", "on"},
		{"uninstall", "--setup-code-stdin"},
	} {
		handled, err := Dispatch(context.Background(), args, io.Discard, io.Discard, ReconnectMessages{}, copy)
		if !handled || err == nil || err.Error() != copy.Arguments {
			t.Fatalf("input conflict reached administrative operation: %v", args)
		}
	}
}
