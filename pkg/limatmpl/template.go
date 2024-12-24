package limatmpl

import (
	"strings"

	"github.com/lima-vm/lima/pkg/limayaml"
)

type Template struct {
	Locator string // template locator (path or URL)
	Bytes   []byte // file contents

	// The following fields are only used when the template represents a YAML config file.
	Name   string // instance name
	Config *limayaml.LimaYAML

	expr strings.Builder // yq expression to update template
}
