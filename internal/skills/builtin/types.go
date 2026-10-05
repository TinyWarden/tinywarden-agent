// Package builtin preserves the compiled observation protocol during skill extraction.
package builtin

import "github.com/TinyWarden/tinywarden-agent/internal/skills/protocol"

type Key = protocol.Key

const (
	Packages = protocol.Packages
	Reboot   = protocol.Reboot
	Fstrim   = protocol.Fstrim
	MaxCount = protocol.MaxCount
	MaxEpoch = protocol.MaxEpoch
)

type Window = protocol.Window
type Execution = protocol.Execution
type Observation = protocol.Observation
type PackageEvidence = protocol.PackageEvidence
type RebootEvidence = protocol.RebootEvidence
type Condition = protocol.Condition
type TimerEvidence = protocol.TimerEvidence
type ServiceEvidence = protocol.ServiceEvidence
type FstrimEvidence = protocol.FstrimEvidence
