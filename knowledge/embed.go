// Package knowledge embeds the YAML compatibility database into the operator
// binary, so a deployed operator needs no mounted volume to know its tools.
package knowledge

import (
	"embed"
	"io/fs"
)

//go:embed tools/*.yaml
var files embed.FS

// Tools returns the embedded knowledge base with one <tool>.yaml file per tool
// at its root — the layout planner.Load expects.
func Tools() fs.FS {
	sub, err := fs.Sub(files, "tools")
	if err != nil {
		// fs.Sub only fails on an invalid path, and "tools" is a constant.
		panic(err)
	}
	return sub
}
