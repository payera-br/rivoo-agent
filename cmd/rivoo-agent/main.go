// Command rivoo-agent delivers Rivoo purchases to a game server over RCON.
package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	fmt.Fprintln(os.Stderr, "rivoo-agent: em construção")
	os.Exit(1)
}
