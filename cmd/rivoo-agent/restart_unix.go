//go:build !windows

package main

import (
	"log"
	"os"
	"syscall"
)

// restart replaces this process with the new binary (same args and env).
func restart() {
	exe, err := os.Executable()
	if err == nil {
		err = syscall.Exec(exe, os.Args, os.Environ())
	}
	log.Printf("reinicie o agente para usar a nova versão (%v)", err)
}
