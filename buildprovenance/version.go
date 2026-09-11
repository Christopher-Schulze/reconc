package buildprovenance

import "runtime/debug"

// DevelopmentVersion identifies direct builds without assigning a release.
// Missing VCS metadata remains explicitly unversioned.
func DevelopmentVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	return developmentVersion(info.Settings)
}

func developmentVersion(settings []debug.BuildSetting) string {
	version, dirty := "dev", false
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			if len(setting.Value) >= 12 {
				version += "+" + setting.Value[:12]
			}
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if dirty {
		version += "-dirty"
	}
	return version
}
