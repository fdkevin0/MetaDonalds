package main

import "runtime/debug"

// Release builds set this with -ldflags="-X main.version=vX.Y.Z".
var version string

func commandVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
