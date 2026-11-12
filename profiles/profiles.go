// Package profiles holds the profiles embedded into the proxy.
package profiles

import "embed"

// FS holds one profile.yaml per profile directory.
//
//go:embed */profile.yaml
var FS embed.FS
