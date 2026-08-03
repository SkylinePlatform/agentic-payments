// Command registry runs the local agent key registry.
//
// Visa operates the production directory; this is the local equivalent, which is what makes the POC possible without a Visa account.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "registry: not implemented yet")
	os.Exit(1)
}
