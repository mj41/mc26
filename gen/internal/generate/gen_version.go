// gen_version generates data/version/version.go from version.json (the
// manifest Mojang ships inside the server jar).
package generate

import (
	"fmt"
	"path/filepath"
)

type versionJSON struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	WorldVersion    int    `json:"world_version"`
	ProtocolVersion int    `json:"protocol_version"`
	PackVersion     struct {
		ResourceMajor int `json:"resource_major"`
		ResourceMinor int `json:"resource_minor"`
		DataMajor     int `json:"data_major"`
		DataMinor     int `json:"data_minor"`
	} `json:"pack_version"`
	BuildTime   string `json:"build_time"`
	JavaVersion int    `json:"java_version"`
	Stable      bool   `json:"stable"`
}

func genVersion(jsonDir, goMCRoot string) error {
	jsonPath := filepath.Join(jsonDir, "version.json")
	outPath := filepath.Join(goMCRoot, "data", "version", "version.go")

	var v versionJSON
	if err := readJSON(jsonPath, &v); err != nil {
		return fmt.Errorf("genVersion: %w", err)
	}
	tmpl, err := loadTemplate(goMCRoot, "version.go.tmpl")
	if err != nil {
		return fmt.Errorf("genVersion: %w", err)
	}
	data := struct {
		Header string
		V      versionJSON
		Build  BuildInfo
	}{
		Header: generatedHeader("gen_version.go", "version.json"),
		V:      v,
		Build:  Build,
	}
	out, err := executeTemplate(tmpl, data)
	if err != nil {
		return fmt.Errorf("genVersion: %w", err)
	}
	if err := writeFile(outPath, out); err != nil {
		return fmt.Errorf("genVersion: %w", err)
	}
	logf("genVersion: wrote %s (%s, protocol %d, data version %d)", outPath, v.ID, v.ProtocolVersion, v.WorldVersion)
	return nil
}
