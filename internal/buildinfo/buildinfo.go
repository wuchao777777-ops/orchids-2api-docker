package buildinfo

import "runtime"

// Set by the release workflow. Local builds deliberately retain source identity.
var Version = "dev"
var Commit = "unknown"
var Date = "unknown"
var BuildType = "source"
var Repository = "zhangdailin/API-Console"

type Info struct {
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	Date       string `json:"built_at"`
	BuildType  string `json:"build_type"`
	Repository string `json:"repository"`
	OS         string `json:"os"`
	Arch       string `json:"arch"`
}

func Current() Info {
	return Info{Version, Commit, Date, BuildType, Repository, runtime.GOOS, runtime.GOARCH}
}
