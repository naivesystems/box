package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
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

var keycloakImage = flag.String("keycloak_image", "naive.systems/box/keycloak:dev", "")

var keycloakHTTPSAddr = flag.String("keycloak_https_addr", "0.0.0.0:9992", "")

var keycloakCmd *exec.Cmd

func StartKeycloak() {
	keycloakDir := filepath.Join(*workdir, "keycloak")
	err := os.MkdirAll(keycloakDir, 0700)
	if err != nil {
		log.Fatalf("os.MkdirAll(%s): %v", keycloakDir, err)
	}
	PodmanKill("keycloak")
	versionFile := filepath.Join(keycloakDir, "version.txt")
	if exists(versionFile) {
		RunKeycloak()
	} else {
		InstallAndRunKeycloak()
	}
}

func InstallAndRunKeycloak() {
	ExtractKeycloak()

	// Let it initialize its database
	RunKeycloak()
	WaitKeycloakUp()
	StopKeycloak()

	time.Sleep(1 * time.Second)

	RunKeycloak()
	WaitKeycloakUp()
	InitKeycloak()
}

func GetKeycloakStatus() (string, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	resp, err := client.Get("https://127.0.0.1:9992/health/ready")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var status struct {
		Status string `json:"status"`
	}
	err = json.Unmarshal(body, &status)
	if err != nil {
		return "", err
	}

	return status.Status, nil
}

func WaitKeycloakUp() {
	for {
		time.Sleep(2 * time.Second)
		status, err := GetKeycloakStatus()
		if status == "UP" {
			break
		}
		if err == nil {
			log.Printf("Keycloak is not up: %s", status)
		} else {
			log.Printf("Keycloak is not up: %v", err)
		}
	}
	log.Println("Keycloak is up")
}

func ExtractKeycloak() {
	keycloakDir := filepath.Join(*workdir, "keycloak")
	cmd := exec.Command("podman", "run", "--rm",
		"--name", "keycloak", "--replace",
		"--userns=keep-id:uid=1000,gid=1000",
		"-v", keycloakDir+":/home/keycloak/keycloak",
		*keycloakImage,
		"/home/keycloak/extract")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Fatalf("Failed to extract Keycloak: %v", err)
	}
}

func InitKeycloak() {
	cmd := exec.Command("podman", "exec", "keycloak", "/home/keycloak/init")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		log.Fatalf("Failed to initialize Keycloak: %v", err)
	}
}

func RunKeycloak() {
	certsDir := filepath.Join(*workdir, "certs")
	keycloakDir := filepath.Join(*workdir, "keycloak")
	keycloakCmd = exec.Command("podman", "run", "--rm",
		"--name", "keycloak", "--replace",
		"--userns=keep-id:uid=1000,gid=1000",
		"-v", certsDir+":/certs",
		"-v", keycloakDir+":/home/keycloak/keycloak",
		"-p", *keycloakHTTPSAddr+":9992/tcp",
		"--add-host", *hostname+":127.0.0.1",
		*keycloakImage,
		"/home/keycloak/run", "--hostname", *hostname)
	err := RedirectPipes(keycloakCmd, "K", "\033[0;33m")
	if err != nil {
		log.Fatalf("Failed to redirect pipes: %v", err)
	}
	log.Printf("Executing %s", keycloakCmd.String())
	err = keycloakCmd.Start()
	if err != nil {
		log.Fatalf("Failed to start Keycloak: %v", err)
	}
}

func StopKeycloak() {
	err := keycloakCmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		log.Printf("Failed to stop Keycloak: %v", err)
	}
	PodmanKill("keycloak")
}

func AddKeycloakUser(username, firstname, lastname string) (string, error) {
	log.Printf("AddKeycloakUser('%s')", username)

	cmd := exec.Command("podman", "exec", "keycloak",
		"/home/keycloak/createuser",
		"--hostname", *hostname,
		"--username", username,
		"--first-name", firstname,
		"--last-name", lastname)

	cmd.Stderr = os.Stderr

	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("error running createuser script: %v", err)
	}

	lines := strings.Split(out.String(), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "[PASSWORD_OUTPUT]:") {
			// Extracting the password from the line
			password := strings.Split(line, ": ")[2]
			return password, nil
		}
	}

	return "", fmt.Errorf("unable to find password: %s", out.String())
}
