package skybox

import (
	_ "embed"
	"encoding/json"
)

//go:embed package.json
var packageJSON []byte

// Version is the version field of package.json, embedded at build time.
var Version = parseVersion(packageJSON)

func parseVersion(data []byte) string {
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		panic("skybox: embedded package.json is not valid JSON: " + err.Error())
	}
	return manifest.Version
}
