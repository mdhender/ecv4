// Copyright (c) 2026 Michael D Henderson. All rights reserved.

package ecv4

import (
	"github.com/maloquacious/semver"
)

var (
	version = semver.Version{
		Major:      0,
		Minor:      3,
		Patch:      1,
		Build:      semver.Commit(),
	}
)

func Version() semver.Version {
	return version
}
