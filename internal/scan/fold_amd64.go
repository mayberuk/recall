package scan

// foldHasAssembly says whether foldASCIIBlocks is the vector routine or the stub
// that declines. A test asserts the wide path's contract only where it exists.
const foldHasAssembly = true

//go:noescape
func foldASCIIBlocks(b []byte) int
