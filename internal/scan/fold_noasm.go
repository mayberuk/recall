//go:build !arm64 && !amd64

package scan

const foldHasAssembly = false

// foldASCIIBlocks declines on an architecture with no hand-written routine,
// leaving fold on the word-at-a-time path it would otherwise take second. It is
// a stub rather than a Go implementation of the same thing on purpose: a second
// Go version would be a second thing to keep in agreement with the reference,
// for no gain over the eight-byte loop already there.
func foldASCIIBlocks(b []byte) int { return 0 }
