package installation

import (
	"context"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/TinyWarden/tinywarden-agent/internal/actioncontract"
	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
	agentrelease "github.com/TinyWarden/tinywarden-agent/internal/release"
)

type actionPolicy struct {
	Schema  int                               `json:"schema_version"`
	Origins []actioncontract.OriginPermission `json:"origins"`
}

func validActionPolicy(value actionPolicy) bool {
	if value.Schema != 1 || value.Origins == nil || len(value.Origins) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, origin := range value.Origins {
		key := origin.GitHubID + ":" + origin.OwnerID
		if seen[key] || !regexp.MustCompile(`^[1-9][0-9]{0,15}$`).MatchString(origin.GitHubID) || !regexp.MustCompile(`^[1-9][0-9]{0,15}$`).MatchString(origin.OwnerID) || len(origin.Classes) > 2 {
			return false
		}
		seen[key] = true
		classes := map[string]bool{}
		for _, class := range origin.Classes {
			if classes[class] || class != "host_admin" && class != "unprivileged" {
				return false
			}
			classes[class] = true
		}
	}
	return true
}

func readActionPolicy(base string) (actionPolicy, error) {
	raw, err := agentrelease.ReadPublic(actionPolicyPath(base), maintenance.MaxControl)
	if os.IsNotExist(err) {
		return actionPolicy{Schema: 1, Origins: []actioncontract.OriginPermission{}}, nil
	}
	if err != nil {
		return actionPolicy{}, err
	}
	var policy actionPolicy
	if controlplane.Decode(raw, &policy) != nil || !validActionPolicy(policy) {
		return policy, errors.New("action_policy_invalid")
	}
	return policy, nil
}

func changeActionPolicy(policy actionPolicy, origin, allow, deny string) (actionPolicy, error) {
	parts := strings.Split(origin, ":")
	if !validActionPolicy(policy) || len(parts) != 2 || allow != "" && deny != "" || allow == "" && deny == "" {
		return policy, errors.New("action_policy_invalid")
	}
	for _, value := range parts {
		if !regexp.MustCompile(`^[1-9][0-9]{0,15}$`).MatchString(value) {
			return policy, errors.New("action_policy_invalid")
		}
	}
	class := allow
	if class == "" {
		class = deny
	}
	if class != "host_admin" && class != "unprivileged" {
		return policy, errors.New("action_policy_invalid")
	}
	result := actionPolicy{Schema: 1, Origins: []actioncontract.OriginPermission{}}
	matched := false
	for _, entry := range policy.Origins {
		if entry.GitHubID != parts[0] || entry.OwnerID != parts[1] {
			result.Origins = append(result.Origins, entry)
			continue
		}
		matched = true
		classes := []string{}
		for _, value := range entry.Classes {
			if value != class {
				classes = append(classes, value)
			}
		}
		if allow != "" {
			classes = append(classes, class)
		}
		if len(classes) > 0 {
			sort.Strings(classes)
			entry.Classes = classes
			result.Origins = append(result.Origins, entry)
		}
	}
	if !matched && allow != "" {
		result.Origins = append(result.Origins, actioncontract.OriginPermission{GitHubID: parts[0], OwnerID: parts[1], Classes: []string{class}})
	}
	if !validActionPolicy(result) {
		return policy, errors.New("action_policy_invalid")
	}
	sort.Slice(result.Origins, func(i, j int) bool {
		return result.Origins[i].GitHubID+":"+result.Origins[i].OwnerID < result.Origins[j].GitHubID+":"+result.Origins[j].OwnerID
	})
	return result, nil
}

func ActionPolicy(ctx context.Context, base, origin, allow, deny string) (actionPolicy, error) {
	if err := Preflight(base, false); err != nil {
		return actionPolicy{}, err
	}
	if origin == "" && allow == "" && deny == "" {
		return readActionPolicy(base)
	}
	guard, err := hostmutation.Lock(base)
	if err != nil {
		return actionPolicy{}, err
	}
	defer guard.Close()
	policy, err := readActionPolicy(base)
	if err != nil {
		return policy, err
	}
	if origin == "" && allow == "" && deny == "" {
		return policy, nil
	}
	policy, err = changeActionPolicy(policy, origin, allow, deny)
	if err != nil {
		return policy, err
	}
	if err = atomicFile(actionPolicyPath(base), policy, 0600); err != nil {
		return policy, err
	}
	manifest, err := maintenanceCurrent(base)
	if err != nil {
		return policy, err
	}
	return policy, maintenanceStatus(ctx, base, manifest)
}
