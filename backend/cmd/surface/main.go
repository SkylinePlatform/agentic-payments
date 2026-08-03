// Command surface runs the trusted surface.
//
// AP2 requires the trusted surface to be non-agentic: no model may sit in the consent or signing path.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "surface: not implemented yet")
	os.Exit(1)
}
