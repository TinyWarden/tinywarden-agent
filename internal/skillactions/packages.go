// Package skillactions connects delivered action packages to generic coordination.
package skillactions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/TinyWarden/tinywarden-agent/internal/controlplane"
	"github.com/TinyWarden/tinywarden-agent/internal/hostmutation"
	"github.com/TinyWarden/tinywarden-agent/internal/maintenance"
	"github.com/TinyWarden/tinywarden-agent/internal/release"
	"github.com/TinyWarden/tinywarden-agent/internal/skillruntime"
)

type Download func(context.Context, maintenance.Intent, string) (string, error)
type Loader struct {
	Base, Policy string
	Runtime      skillruntime.Client
	Guard        *os.File
	Verify       func() error
	Download     Download
}

type actionMetadata struct {
	Descriptor struct {
		ID               string `json:"id"`
		Version          int    `json:"version"`
		RequiresApproval bool   `json:"requires_approval"`
		PrepareEffect    string `json:"prepare_effect"`
	} `json:"descriptor"`
	SHA string `json:"descriptor_sha256"`
}

func (l Loader) Registry() maintenance.Registry {
	return maintenance.Registry{{Resolve: l.resolve}}
}

func (l Loader) resolve(intent maintenance.Intent) (maintenance.Definition, error) {
	if intent.Binding == nil || intent.Validate() != nil || l.Verify == nil || l.Guard == nil {
		return maintenance.Definition{}, errors.New("action_binding_invalid")
	}
	if err := l.Verify(); err != nil {
		return maintenance.Definition{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory, metadata, err := l.ensure(ctx, intent)
	if err != nil {
		return maintenance.Definition{}, err
	}
	var admitted struct {
		Actions []actionMetadata `json:"actions"`
	}
	// Metadata already passed the pinned static SDK validator. Project only the
	// generic orchestration flags; its full schemas are checked again by the runner.
	if json.Unmarshal(metadata, &admitted) != nil {
		return maintenance.Definition{}, errors.New("action_metadata_invalid")
	}
	for _, entry := range admitted.Actions {
		if entry.Descriptor.ID != intent.Action || entry.Descriptor.Version != intent.ProviderVersion || entry.SHA != intent.Binding.DescriptorSHA {
			continue
		}
		provider := &Provider{Loader: l, Intent: intent, Package: directory}
		// Every delivered action uses plans and an irreversible start record.
		// requires_approval controls human review in the app, never this host gate.
		return maintenance.Definition{ID: intent.Action, Version: intent.ProviderVersion, Approval: true,
			PrepareEffect: entry.Descriptor.PrepareEffect, New: func() maintenance.Provider { return provider }}, nil
	}
	return maintenance.Definition{}, errors.New("action_metadata_invalid")
}

func (l Loader) ensure(ctx context.Context, intent maintenance.Intent) (string, []byte, error) {
	store := filepath.Join(l.Base, "updater", "private", "action-packages")
	if err := os.MkdirAll(store, 0700); err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(store)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != 0 {
		return "", nil, errors.New("action_cache_invalid")
	}
	directory := filepath.Join(store, intent.Binding.ContentSHA)
	directoryInfo, directoryErr := os.Lstat(directory)
	if directoryErr == nil && (!directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 || directoryInfo.Sys().(*syscall.Stat_t).Uid != 0) {
		return "", nil, errors.New("action_cache_invalid")
	}
	if directoryErr != nil && !os.IsNotExist(directoryErr) {
		return "", nil, directoryErr
	}
	metadataPath := filepath.Join(store, "."+intent.Binding.ContentSHA+".metadata.json")
	runtimeSHA, err := release.FileDigest(filepath.Join(l.Runtime.Assets, "artifact.json"), 65536)
	if err != nil {
		return "", nil, err
	}
	if raw, err := release.ReadPublic(metadataPath, 1<<20); err == nil && directoryErr == nil {
		var cached struct {
			RuntimeSHA string          `json:"runtime_sha256"`
			ContentSHA string          `json:"content_sha256"`
			Metadata   json.RawMessage `json:"metadata"`
		}
		if controlplane.DecodeArtifact(raw, &cached, 180000) == nil && cached.RuntimeSHA == runtimeSHA && cached.ContentSHA == intent.Binding.ContentSHA {
			// The publisher already passed bounded static admission. Immutable cache
			// bytes are rechecked before import; CLI recovery needs no new cgroup.
			return directory, cached.Metadata, nil
		}
	}
	var metadata []byte
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		if fence, err := hostmutation.ReadFence(l.Base); err != nil {
			return "", nil, err
		} else if fence != nil && fence.JobID == intent.ID {
			journal, err := maintenance.Load(l.Base)
			if err != nil || journal.ApplyAttempt != nil || journal.Attention {
				return "", nil, errors.New("action_package_unavailable")
			}
		}
		if l.Download == nil {
			return "", nil, errors.New("action_package_unavailable")
		}
		archive, err := l.Download(ctx, intent, store)
		if err != nil {
			return "", nil, err
		}
		defer os.Remove(archive)
		metadata, err = l.Runtime.Invoke(ctx, skillruntime.Request{Action: "publish_archive", Store: store, Archive: archive,
			ArchiveSHA256: intent.Archive.SHA256, ContentSHA256: intent.Binding.ContentSHA, Official: true})
		if err != nil {
			return "", nil, err
		}
	} else if err != nil {
		return "", nil, err
	}
	if metadata == nil {
		metadata, err = l.Runtime.Invoke(ctx, skillruntime.Request{Action: "inspect", Package: directory, ContentSHA256: intent.Binding.ContentSHA, Official: true})
		if err != nil {
			return "", nil, err
		}
	}
	var identity struct {
		ContentSHA string `json:"content_sha256"`
	}
	if json.Unmarshal(metadata, &identity) != nil || identity.ContentSHA != intent.Binding.ContentSHA {
		return "", nil, errors.New("action_metadata_invalid")
	}
	cache, err := json.Marshal(struct {
		RuntimeSHA string          `json:"runtime_sha256"`
		ContentSHA string          `json:"content_sha256"`
		Metadata   json.RawMessage `json:"metadata"`
	}{runtimeSHA, intent.Binding.ContentSHA, metadata})
	if err != nil || len(cache) > 1<<20 {
		return "", nil, errors.New("action_metadata_invalid")
	}
	if err = hostmutation.PrivateWrite(metadataPath, cache); err != nil {
		return "", nil, err
	}
	return directory, metadata, nil
}
