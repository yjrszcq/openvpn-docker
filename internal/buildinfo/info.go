// Package buildinfo exposes immutable build metadata injected with ldflags.
package buildinfo

import "runtime"

var (
	Version   = "4.0.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

const DataSchema = 4

// Info is the stable version query object.
type Info struct {
	Version    string `json:"version"`
	DataSchema int    `json:"data_schema"`
	Commit     string `json:"commit"`
	BuildDate  string `json:"build_date"`
	GoVersion  string `json:"go_version"`
}

// Current returns the current binary's build metadata.
func Current() Info {
	return Info{
		Version:    Version,
		DataSchema: DataSchema,
		Commit:     Commit,
		BuildDate:  BuildDate,
		GoVersion:  runtime.Version(),
	}
}
