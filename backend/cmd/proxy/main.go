// Command proxy runs the verifying proxy placed in front of the mock merchant.
//
// TAP verification happens at the merchant edge — Visa's own reference architecture puts a CDN proxy there.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "proxy: not implemented yet")
	os.Exit(1)
}
