// Command benchrun measures recall against a generated corpus. It writes
// bench/RESULTS.md, enforces the wall-clock gates, and records and checks the
// allocation baseline, so those three cannot disagree about what was measured.
//
// Usage:
//
//	benchrun report|gate|baseline|compare|packages [flags]
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "report":
		err = report(os.Args[2:])
	case "gate":
		err = gate(os.Args[2:])
	case "baseline":
		err = baseline(os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	case "packages":
		err = packages()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "benchrun:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: benchrun report|gate|baseline|compare|packages [flags]")
}
