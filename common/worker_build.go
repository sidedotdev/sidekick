package common

import "runtime/debug"

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
	if vcs != "git" || modified != "false" || revision == "" {
		return "unknown"
	}
	return revision
}
