package webui

import "embed"

// Dist is replaced with the Vite production bundle during the container build.
//
//go:embed dist/*
var Dist embed.FS
