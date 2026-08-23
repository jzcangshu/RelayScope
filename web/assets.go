package web

import "embed"

// Assets contains the public and administrator static applications.
//
//go:embed public admin
var Assets embed.FS
