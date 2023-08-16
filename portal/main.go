package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

var workdir = flag.String("workdir", "", "Absolute path to the working directory")
var hostname = flag.String("hostname", "nsbox.local", "")

func main() {
	flag.Parse()
	if *workdir == "" {
		log.Fatalln("-workdir must be specified")
	}
	if !filepath.IsAbs(*workdir) {
		log.Fatalf("-workdir %s is not an absolute path", *workdir)
	}
	if !exists(*workdir) {
		log.Fatalf("-workdir %s does not exist", *workdir)
	}
	if *hostname == "" {
		log.Fatalln("-hostname must be specified")
	}
	PrepareCerts()
	StartKeycloak()

	err := StartRedmine()
	if err != nil {
		log.Printf("Failed to start Redmine: %v", err)
		StopKeycloak()
		os.Exit(1)
	}

	sigs := make(chan os.Signal, 1)
	// Ctrl-C triggers SIGINT. systemd is supposed to trigger SIGTERM.
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Printf("Received signal: %v", sig)
		StopRedmine()
		StopKeycloak()
		os.Exit(0)
	}()

	// TODO
	log.Fatal(http.ListenAndServe("127.0.0.1:7777", nil))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		log.Fatalf("os.Stat(%s): %v", path, err)
	}
	return true
}

func PodmanKill(name string) {
	cmd := exec.Command("podman", "kill", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("podman kill %s: %v", name, err)
	}
}
