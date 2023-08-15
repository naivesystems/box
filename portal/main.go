package main

import (
	"flag"
	"log"
	"net/http"
	"os"
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

	sigs := make(chan os.Signal, 1)
	// Ctrl-C triggers SIGINT. systemd is supposed to trigger SIGTERM.
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Printf("Received signal: %v", sig)
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
