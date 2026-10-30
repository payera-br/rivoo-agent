// Command sign makes the release signing key and signs manifests.
//
//	go run ./scripts/sign keygen            prints a new private key (base64 seed) and its public key
//	AGENT_SIGNING_KEY=… go run ./scripts/sign manifest.json   writes manifest.json.sig
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "uso: sign keygen | sign <arquivo>")
		os.Exit(2)
	}
	if os.Args[1] == "keygen" {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		fmt.Println("private:", base64.StdEncoding.EncodeToString(priv.Seed()))
		fmt.Println("public: ", base64.StdEncoding.EncodeToString(pub))
		return
	}
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("AGENT_SIGNING_KEY"))
	if err != nil || len(seed) != ed25519.SeedSize {
		fmt.Fprintln(os.Stderr, "AGENT_SIGNING_KEY ausente ou inválida")
		os.Exit(1)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), b)
	if err := os.WriteFile(os.Args[1]+".sig", []byte(base64.StdEncoding.EncodeToString(sig)+"\n"), 0o644); err != nil {
		panic(err)
	}
}
