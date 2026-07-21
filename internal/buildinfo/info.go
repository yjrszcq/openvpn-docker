// Package buildinfo exposes immutable build metadata injected with ldflags.
package buildinfo

import "runtime"

var (
	Version   = "4.0.0"
	Commit    = "unknown"
	BuildDate = "unknown"
)

const DataSchema = 4

const (
	SQLiteDriver = "github.com/mattn/go-sqlite3 v1.14.48"
	YAMLLibrary  = "go.yaml.in/yaml/v3 v3.0.4"
)

// Info is the stable version query object.
type Info struct {
	Version      string `json:"version"`
	DataSchema   int    `json:"data_schema"`
	Commit       string `json:"commit"`
	BuildDate    string `json:"build_date"`
	GoVersion    string `json:"go_version"`
	Dependencies struct {
		SQLite string `json:"sqlite"`
		YAML   string `json:"yaml"`
	} `json:"dependencies"`
}

// Current returns the current binary's build metadata.
func Current() Info {
	info := Info{
		Version:    Version,
		DataSchema: DataSchema,
		Commit:     Commit,
		BuildDate:  BuildDate,
		GoVersion:  runtime.Version(),
	}
	info.Dependencies.SQLite = SQLiteDriver
	info.Dependencies.YAML = YAMLLibrary
	return info
}
