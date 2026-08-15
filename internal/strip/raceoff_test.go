//go:build !race

package strip

// raceDetector reports whether this binary was built with -race, which slows
// execution several-fold and makes a wall-clock gate meaningless.
const raceDetector = false
