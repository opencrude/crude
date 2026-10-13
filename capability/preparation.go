package capability

import (
	"context"
	"slices"

	captypes "crude/types/capability"
)

// packagePreparation derives the command that installs a registry package into
// its runner's cache without starting the server: npm executes a no-op with
// the package installed, and uv resolves the tool environment and runs an
// empty interpreter. The subsequent launch then reuses the warm cache and
// completes its handshake within the ordinary connection budget. Connections
// whose arguments no longer contain the pinned package spec, and connections
// executed on another host, are launched directly.
func packagePreparation(config captypes.ConnectionConfig) (captypes.PreparationRequired, bool) {
	if config.Transport != "stdio" || config.ExecutionHost() != "core" || config.PackageID == "" || config.Version == "" {
		return captypes.PreparationRequired{}, false
	}
	var spec string
	switch config.PackageType {
	case "npm":
		spec = config.PackageID + "@" + config.Version
	case "pypi":
		spec = config.PackageID + "==" + config.Version
	default:
		return captypes.PreparationRequired{}, false
	}
	index := slices.Index(config.Args, spec)
	if index < 0 {
		return captypes.PreparationRequired{}, false
	}
	runtimeArgs := config.Args[:index]
	var args []string
	if config.PackageType == "npm" {
		if len(runtimeArgs) > 0 && (runtimeArgs[0] == "-y" || runtimeArgs[0] == "--yes") {
			runtimeArgs = runtimeArgs[1:]
		}
		args = append(append([]string{"--yes"}, runtimeArgs...), "--package="+spec, "--call", "exit 0")
	} else {
		args = append(append([]string(nil), runtimeArgs...), "--from", spec, "python", "-c", "")
	}
	request := captypes.PreparationRequired{ConnectionID: config.ID, Name: config.Name, Command: config.Command, Args: args}
	request.Key = previewToken(struct {
		Command string
		Args    []string
	}{request.Command, request.Args})
	return request, true
}

// prepareLaunch returns PreparationRequired until the exact package is known
// to be available in this process: a session completed its preparation, or a
// launch of the same preparation identity already succeeded.
func (service *Service) prepareLaunch(ctx context.Context, config captypes.ConnectionConfig) (string, error) {
	request, needed := packagePreparation(config)
	if !needed {
		return "", nil
	}
	service.mu.RLock()
	prepared := service.prepared[request.Key]
	service.mu.RUnlock()
	if prepared || captypes.PreparedFrom(ctx) == request.Key {
		return request.Key, nil
	}
	return "", &request
}

func (service *Service) markPrepared(key string) {
	if key == "" {
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.prepared == nil {
		service.prepared = make(map[string]bool)
	}
	service.prepared[key] = true
}
