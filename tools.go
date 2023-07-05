//go:build tools
// +build tools

// Tooling dependencies
// https://github.com/golang/go/wiki/Modules#how-can-i-track-tool-dependencies-for-a-module
// https://github.com/go-modules-by-example/index/blob/master/010_tools/README.md

// This file may incorporate tools that may be *both* used as CLI and as lib
// Keep in mind that these global tools change the go.mod/go.sum dependency tree
// Other tooling may be installed as *static binary* directly within the Dockerfile

package tools

import (
	_ "github.com/uw-labs/lichen"
)
