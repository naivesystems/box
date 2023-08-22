package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

var workdir = flag.String("workdir", "", "Absolute path to the working directory")
var hostname = flag.String("hostname", "nsbox.local", "")
var bindIP = flag.String("bind", "127.0.0.1", "Address behind httpd reverse proxy")

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

	err = StartGerrit()
	if err != nil {
		log.Printf("Failed to start Gerrit: %v", err)
		StopRedmine()
		StopKeycloak()
		os.Exit(1)
	}

	err = StartHttpd()
	if err != nil {
		log.Printf("Failed to start httpd: %v", err)
		StopGerrit()
		StopRedmine()
		StopKeycloak()
		os.Exit(1)
	}

	sigs := make(chan os.Signal, 1)
	// Ctrl-C triggers SIGINT. systemd is supposed to trigger SIGTERM.
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		fmt.Println()
		os.Stdout.Sync()
		os.Stderr.Sync()
		log.Printf("Received signal: %v", sig)
		StopHttpd()
		StopGerrit()
		StopRedmine()
		StopKeycloak()
		os.Exit(0)
	}()

	// TODO
	log.Fatal(http.ListenAndServe(*bindIP+":7777", nil))
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
	time.Sleep(1 * time.Second)
	cmd := exec.Command("podman", "kill", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Printf("podman kill %s: %v", name, err)
	}
}

func RedirectPipes(cmd *exec.Cmd, prefix, color string) error {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	RedirectPipe(stdout, prefix+"O: ", color)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	RedirectPipe(stderr, prefix+"E: ", color)

	return nil
}

func RedirectPipe(pipe io.ReadCloser, prefix, color string) {
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("%s%s%s\033[0m\n", color, prefix, line)
			os.Stdout.Sync()
		}
		err := scanner.Err()
		if err != nil {
			log.Printf("RedirectPipe: %s%v", prefix, err)
		}
	}()
}
