// Command credprovider runs the mock credential provider.
//
// This is where Skyline sits in a real deployment, so it is not throwaway work.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "credprovider: not implemented yet")
	os.Exit(1)
}
