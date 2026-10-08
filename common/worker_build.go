package common

import "runtime/debug"

// DirtyWorkerBuildSuffix marks the build ID of a worker built with uncommitted
// changes on top of the preceding revision.
const DirtyWorkerBuildSuffix = "-dirty"

// GetWorkerBuildID uses embedded provenance because the running binary may not
// match the checkout on disk.
func GetWorkerBuildID() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	return workerBuildID(info.Settings)
}

func workerBuildID(settings []debug.BuildSetting) string {
	var vcs, revision, modified string
	for _, setting := range settings {
		switch setting.Key {
		case "vcs":
			vcs = setting.Value
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if vcs != "git" || revision == "" {
		return "unknown"
	}
	if modified != "false" {
		return revision + DirtyWorkerBuildSuffix
	}
	return revision
}
