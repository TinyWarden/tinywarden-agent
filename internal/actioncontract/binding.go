// Package actioncontract owns domain-neutral immutable action identities.
package actioncontract

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
)

const Capability = "skill_actions.v1"

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var counter = regexp.MustCompile(`^[1-9][0-9]{0,15}$`)
var skill = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}/[a-z][a-z0-9-]{0,63}$`)
var action = regexp.MustCompile(`^[a-z][a-z0-9_.]{1,63}$`)

type Origin struct {
	GitHubID string `json:"github_id"`
	OwnerID  string `json:"owner_id"`
}

type Archive struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (a Archive) Valid() bool { return digest.MatchString(a.SHA256) && a.Size > 0 && a.Size <= 10<<20 }

func SameArchive(a, b *Archive) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

type Binding struct {
	InstallationID     string `json:"installation_id"`
	SkillID            string `json:"skill_id"`
	ContentSHA         string `json:"content_sha256"`
	ActionID           string `json:"action_id"`
	ActionVersion      int    `json:"action_version"`
	DescriptorSHA      string `json:"descriptor_sha256"`
	PermissionRevision string `json:"permission_revision"`
	SettingsRevision   string `json:"settings_revision"`
	SettingsSHA        string `json:"settings_sha256"`
	InputSHA           string `json:"input_sha256"`
	Origin             Origin `json:"origin"`
}

func (b Binding) Validate() error {
	if !uuid.MatchString(b.InstallationID) || !skill.MatchString(b.SkillID) || !action.MatchString(b.ActionID) || b.ActionVersion < 1 || b.ActionVersion > 65535 || !counter.MatchString(b.Origin.GitHubID) || !counter.MatchString(b.Origin.OwnerID) {
		return errors.New("action_binding_invalid")
	}
	for _, value := range []string{b.ContentSHA, b.DescriptorSHA, b.SettingsSHA, b.InputSHA} {
		if !digest.MatchString(value) {
			return errors.New("action_binding_invalid")
		}
	}
	for _, value := range []string{b.PermissionRevision, b.SettingsRevision} {
		n, err := strconv.ParseUint(value, 10, 64)
		if !counter.MatchString(value) || err != nil || n > 1<<53-1 {
			return errors.New("action_binding_invalid")
		}
	}
	return nil
}

func Equal(a, b *Binding) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

type PackageReason struct {
	Key    string                     `json:"key"`
	Params map[string]json.RawMessage `json:"params"`
}
