//go:build !windows

package mint

import "os"

func enableANSI(_ *os.File) bool {
	return true
}
