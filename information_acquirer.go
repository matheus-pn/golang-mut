// The goal of this step is to acquire information about the files and directories
package main

import (
	"os/exec"
	"strings"
)

type PackageInfo struct {
	Dir          string
	ImportPath   string
	GoFiles      []string
	TestGoFiles  []string
	ReachDefined bool
}

// FIXME: Don't use an external command
func GetPackageInfo(cfg Config) []PackageInfo {
	// Output will be a series of lines
	// each line will have a semicolon separated list of:
	// Dir ; ImportPath ; GoFiles ; TestGofiles
	// GoFiles and TestGofiles are Comma Separated Values

	Verbosef("AT (%s) EXEC go list -f {{.Dir}};{{.ImportPath}};{{range .GoFiles}}{{.}},{{end}};{{range .TestGoFiles}}{{.}},{{end}} ./...\n", TMP_ROOT)
	cmd := exec.Command(
		"go", "list", "-f",
		"{{.Dir}};{{.ImportPath}};{{range .GoFiles}}{{.}},{{end}};{{range .TestGoFiles}}{{.}},{{end}}",
		cfg.Package,
	)
	cmd.Dir = TMP_ROOT
	out, err := cmd.Output()

	if err != nil {
		panic(err)
	}
	lines := strings.Split(string(out), "\n")
	// Ignore empty last line
	lines = lines[:len(lines)-1]

	infoList := []PackageInfo{}
	for _, line := range lines {
		fields := strings.Split(line, ";")
		goFiles := strings.Split(fields[2], ",")
		gotestFiles := strings.Split(fields[3], ",")

		pack := PackageInfo{
			fields[0],
			fields[1],
			// Ignore trailing comma
			goFiles[:len(goFiles)-1],
			gotestFiles[:len(gotestFiles)-1],
			false,
		}
		infoList = append(infoList, pack)
	}
	return infoList
}
