package actioncontract

import "regexp"

type Prerequisite struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Package string `json:"package"`
	Path    string `json:"path,omitempty"`
	Module  string `json:"module,omitempty"`
}
type OriginPermission struct {
	GitHubID string   `json:"github_id"`
	OwnerID  string   `json:"owner_id"`
	Classes  []string `json:"classes"`
}
type Probe struct {
	GrantID        string         `json:"grant_id"`
	InstallationID string         `json:"installation_id"`
	ContentSHA     string         `json:"content_sha256"`
	DescriptorSHA  string         `json:"descriptor_sha256"`
	ID             string         `json:"id"`
	Version        int            `json:"version"`
	Revision       string         `json:"revision"`
	Origin         Origin         `json:"origin"`
	ExecutionClass string         `json:"execution_class"`
	Prerequisites  []Prerequisite `json:"prerequisites"`
}
type Readiness struct {
	GrantID        string   `json:"grant_id"`
	InstallationID string   `json:"installation_id"`
	ContentSHA     string   `json:"content_sha256"`
	DescriptorSHA  string   `json:"descriptor_sha256"`
	ID             string   `json:"id"`
	Revision       string   `json:"revision"`
	Ready          bool     `json:"ready"`
	Reason         *string  `json:"reason"`
	Missing        []string `json:"missing"`
}

func (p Probe) Valid() bool {
	if !uuid.MatchString(p.GrantID) || !uuid.MatchString(p.InstallationID) || !digest.MatchString(p.ContentSHA) || !digest.MatchString(p.DescriptorSHA) || !action.MatchString(p.ID) || p.Version < 1 || p.Version > 65535 || !counter.MatchString(p.Revision) || !counter.MatchString(p.Origin.GitHubID) || !counter.MatchString(p.Origin.OwnerID) || len(p.Prerequisites) > 16 {
		return false
	}
	if p.ExecutionClass != "unprivileged" && p.ExecutionClass != "host_admin" {
		return false
	}
	seen := map[string]bool{}
	for _, v := range p.Prerequisites {
		if !action.MatchString(v.ID) || seen[v.ID] || !regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,127}$`).MatchString(v.Package) {
			return false
		}
		seen[v.ID] = true
		switch v.Kind {
		case "executable":
			if v.Module != "" || !regexp.MustCompile(`^/usr/s?bin/[A-Za-z0-9_.+-]{1,128}$`).MatchString(v.Path) {
				return false
			}
		case "python_module":
			if v.Path != "" || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`).MatchString(v.Module) {
				return false
			}
		default:
			return false
		}
	}
	return true
}
