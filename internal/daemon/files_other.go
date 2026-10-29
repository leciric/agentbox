//go:build !unix

package daemon

import "agentbox/internal/api"

func ownFiles() api.FileCount { return api.FileCount{Open: countOwnFDs()} }
