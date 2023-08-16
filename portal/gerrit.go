package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var gerritImage = flag.String("gerrit_image", "naive.systems/box/gerrit:dev", "")

var gerritCmd *exec.Cmd

func StartGerrit() error {
	gerritDir := filepath.Join(*workdir, "gerrit")
	err := os.MkdirAll(gerritDir, 0700)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", gerritDir, err)
	}
	PodmanKill("gerrit")
	versionFile := filepath.Join(gerritDir, "version.txt")
	if !exists(versionFile) {
		log.Printf("%s does not exist. Initializing...", versionFile)
		err := InitGerrit()
		if err != nil {
			return err
		}
		log.Printf("Gerrit has been successfully initialized.")
	}
	err = RunGerrit()
	if err != nil {
		return err
	}
	WaitGerritUp()
	AddGerritUser("admin")
	return nil
}

func InitGerrit() error {
	_, err := PodmanRunGerrit(true, "/home/gerrit/init")
	if err != nil {
		return fmt.Errorf("failed to initialize Gerrit: %v", err)
	}
	return nil
}

func RunGerrit() error {
	cmd, err := PodmanRunGerrit(false, "/home/gerrit/run")
	if err != nil {
		return fmt.Errorf("failed to start Gerrit: %v", err)
	}
	gerritCmd = cmd
	return nil
}

func PodmanRunGerrit(wait bool, args ...string) (*exec.Cmd, error) {
	gerritDir := filepath.Join(*workdir, "gerrit")

	cmdArgs := []string{
		"run", "--rm",
		"--name", "gerrit", "--replace",
		"--userns=keep-id:uid=1000,gid=1000",
		"-v", gerritDir + ":/home/gerrit/review_site",
		"-p", "0.0.0.0:8081:8081/tcp",
		"-p", "0.0.0.0:29418:29418/tcp",
		*gerritImage,
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("podman", cmdArgs...)
	err := RedirectPipes(cmd, "G", "\033[1;32m")
	if err != nil {
		return nil, fmt.Errorf("failed to redirect pipes: %v", err)
	}
	log.Printf("Executing %s", cmd.String())
	if wait {
		return cmd, cmd.Run()
	} else {
		return cmd, cmd.Start()
	}
}

func StopGerrit() {
	err := gerritCmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		log.Printf("Failed to stop Gerrit: %v", err)
	}
	PodmanKill("gerrit")
}

func WaitGerritUp() {
	for {
		time.Sleep(2 * time.Second)
		version, err := GetGerritVersion()
		if version == "3.8.1" {
			break
		}
		if err == nil {
			log.Printf("Gerrit is not up: %s", version)
		} else {
			log.Printf("Gerrit is not up: %v", err)
		}
	}
	log.Println("Gerrit is up")
}

func GetGerritVersion() (string, error) {
	url := "http://127.0.0.1:8081/config/server/version"

	// Make the HTTP request
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Read the response body using io.ReadAll
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Split the response into lines and get the last line
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	return strings.Trim(lines[len(lines)-1], "\""), nil
}

func AddGerritUser(username string) error {
	url := "http://127.0.0.1:8081/login/"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %s", err)
	}

	// Set the header
	req.Header.Set("REMOTE_USER", username)

	// Send the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %s", err)
	}
	defer resp.Body.Close()

	// Print response status and headers
	log.Printf("%s %s\n", resp.Proto, resp.Status)
	for k, v := range resp.Header {
		log.Printf("%s: %s\n", k, v[0])
	}

	// Optionally, if you want to print the response body:
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %s", err)
	}
	log.Println(string(body))
	return nil
}
