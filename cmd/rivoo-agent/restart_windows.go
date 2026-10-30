//go:build windows

package main

import (
	"log"
	"os"
	"os/exec"
)

// restart starts the new binary and exits (Windows can't exec in place).
func restart() {
	exe, err := os.Executable()
	if err == nil {
		cmd := exec.Command(exe, os.Args[1:]...)
		cmd.Stdout, cmd.Stderr, cmd.Stdin = os.Stdout, os.Stderr, os.Stdin
		if err = cmd.Start(); err == nil {
			os.Exit(0)
		}
	}
	log.Printf("reinicie o agente para usar a nova versão (%v)", err)
}
