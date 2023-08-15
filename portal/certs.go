package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

var nsboxKeyFile = flag.String("nsbox_key_file", "nsbox.key", "")
var nsboxCrtFile = flag.String("nsbox_crt_file", "nsbox.crt", "")

func PrepareSelfSignedKeyPair(certsDir string) {
	keyFile := filepath.Join(certsDir, *nsboxKeyFile)
	crtFile := filepath.Join(certsDir, *nsboxCrtFile)
	if exists(keyFile) && exists(crtFile) {
		log.Printf("Both %s and %s exist. Skip key generation.", keyFile, crtFile)
		return
	}
	cmd := exec.Command("openssl", "req", "-x509", "-newkey", "rsa:4096",
		"-keyout", keyFile, "-noenc", "-out", crtFile, "-days", "3650",
		"-subj", "/CN="+*hostname, "-addext", "subjectAltName=DNS:"+*hostname)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	log.Printf("Execute command: %s", cmd.String())
	err := cmd.Run()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	log.Printf("Generated %s and %s", keyFile, crtFile)
}

func PrepareCerts() {
	certsDir := filepath.Join(*workdir, "certs")
	err := os.MkdirAll(certsDir, 0700)
	if err != nil {
		log.Fatalf("os.MkdirAll(%s): %v", certsDir, err)
	}
	PrepareSelfSignedKeyPair(certsDir)
}
