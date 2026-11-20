// Package mock has in-memory versions of every contract interface. The UI
// and the transfer engine run on these until P2's real ones land, and tests
// use them forever. None of them touch the network or the disk.
package mock

import (
	"github.com/vinayakkam/droplink/internal/contract"
)

// Compile-time checks: a contract change that breaks a mock fails the build.
var (
	_ contract.Discovery  = (*Discovery)(nil)
	_ contract.Crypto     = (*Crypto)(nil)
	_ contract.SecureConn = (*secureConn)(nil)
	_ contract.Transport  = (*Transport)(nil)
	_ contract.Stream     = (*stream)(nil)
	_ contract.Signaler   = (*Hub)(nil)
	_ contract.Room       = (*room)(nil)
)
