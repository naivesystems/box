package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var defaultRedmineImage = "naive.systems/box/redmine:dev"
var redmineImage = flag.String("redmine_image", defaultRedmineImage, "")

var redmineCmd *exec.Cmd

func StartRedmine() error {
	redmineDir := filepath.Join(*workdir, "redmine")
	err := os.MkdirAll(redmineDir, 0700)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", redmineDir, err)
	}
	PodmanKill("redmine")
	versionFile := filepath.Join(redmineDir, "data", "version.txt")
	if !exists(versionFile) {
		log.Printf("%s does not exist. Initializing...", versionFile)
		err := InitRedmine()
		if err != nil {
			return err
		}
		log.Printf("Redmine has been successfully initialized.")
	} else {
		bytes, err := os.ReadFile(versionFile)
		if err != nil {
			return fmt.Errorf("os.ReadFile(%s): %v", versionFile, err)
		}
		if strings.HasPrefix(string(bytes), "5.0.6+nsbox.2023110601") {
			backupDir := filepath.Join(*workdir, "backup")
			err := os.MkdirAll(backupDir, 0700)
			if err != nil {
				return fmt.Errorf("os.MkdirAll(%s): %v", backupDir, err)
			}
			redmineBackupDir := filepath.Join(backupDir, "redmine")
			_ = os.RemoveAll(redmineBackupDir)
			cmd := exec.Command("cp", "-rf", redmineDir, backupDir)
			output, err := cmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("%s: %v\n%s", cmd.String(), err, string(output))
			}
			log.Print("Upgrading redmine...")
			err = UpgradeRedmine()
			if err != nil {
				cmd := exec.Command("cp", "-rf", redmineBackupDir, *workdir)
				output, err := cmd.CombinedOutput()
				if err != nil {
					return fmt.Errorf("%s: %v\n%s", cmd.String(), err, string(output))
				}
				return err
			}
			log.Printf("Redmine has been successfully upgraded.")
		}
	}
	if err := RunRedmine(); err != nil {
		return err
	}
	// After redmine is started for the first time and before the admin user is
	// created, there is a race condition between UpdateRedmineAdminEmail and
	// the admin user creation. That is probably why I saw 404 Not Found
	// sometimes, but I couldn't reproduce reliably.
	// TODO: fix the race properly
	time.Sleep(5 * time.Second)
	// TODO: update all user emails in case that hostname changed
	if err := UpdateRedmineAdminEmail(); err != nil {
		StopRedmine()
		return err
	}
	return nil
}

func InitRedmine() error {
	_, err := PodmanRunRedmine(true, "/home/redmine/init")
	if err != nil {
		return fmt.Errorf("failed to initialize Redmine: %v", err)
	}
	return nil
}

func UpgradeRedmine() error {
	_, err := PodmanRunRedmine(true, "/home/redmine/upgrade")
	if err != nil {
		return fmt.Errorf("failed to upgrade Redmine: %v", err)
	}
	return nil
}

func RunRedmine() error {
	cmd, err := PodmanRunRedmine(false, "/home/redmine/run",
		"--bind", *bindIP,
		"--hostname", *hostname)
	if err != nil {
		return fmt.Errorf("failed to start Redmine: %v", err)
	}
	redmineCmd = cmd
	return nil
}

func PodmanRunRedmine(wait bool, args ...string) (*exec.Cmd, error) {
	if *releaseTag != "dev" && *redmineImage == defaultRedmineImage {
		err := flag.Set("redmine_image", "ghcr.io/naivesystems/box/redmine:"+*releaseTag)
		if err != nil {
			log.Fatalf("failed to set redmine_image: %v", err)
		}
	}

	dataDir := filepath.Join(*workdir, "redmine", "data")
	err := os.MkdirAll(dataDir, 0700)
	if err != nil {
		return nil, fmt.Errorf("os.MkdirAll(%s): %v", dataDir, err)
	}

	filesDir := filepath.Join(*workdir, "redmine", "files")
	err = os.MkdirAll(filesDir, 0700)
	if err != nil {
		return nil, fmt.Errorf("os.MkdirAll(%s): %v", filesDir, err)
	}

	logDir := filepath.Join(*workdir, "redmine", "log")
	err = os.MkdirAll(logDir, 0700)
	if err != nil {
		return nil, fmt.Errorf("os.MkdirAll(%s): %v", logDir, err)
	}

	cmdArgs := []string{
		"run", "--rm",
		"--name", "redmine", "--replace",
		"--userns=keep-id:uid=1000,gid=1000",
		"-v", dataDir + ":/home/redmine/redmine/data",
		"-v", filesDir + ":/home/redmine/redmine/files",
		"-v", logDir + ":/home/redmine/redmine/log",
		"--network=host",
		*redmineImage,
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("podman", cmdArgs...)
	err = RedirectPipes(cmd, "R", "\033[0;31m")
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

func StopRedmine() {
	err := redmineCmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		log.Printf("Failed to stop Redmine: %v", err)
	}
	PodmanKill("redmine")
}

func AddRedmineUser(username, firstname, lastname string) (int, error) {
	adminKeyFile := filepath.Join(*workdir, "redmine", "data", "admin_api_key.txt")
	redmineKey, err := os.ReadFile(adminKeyFile)
	if err != nil {
		return 0, fmt.Errorf("error reading redmine key: %v", err)
	}

	body := map[string]any{
		"user": map[string]any{
			"login":             username,
			"firstname":         firstname,
			"lastname":          lastname,
			"mail":              username + "@" + *hostname,
			"generate_password": true, // Let Redmine generate the password
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("error marshalling JSON: %v", err)
	}

	req, err := http.NewRequest("POST",
		fmt.Sprintf("http://%s:3000/users.json", *bindIP),
		bytes.NewBuffer(jsonBody))

	if err != nil {
		return 0, fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Redmine-API-Key", strings.TrimSpace(string(redmineKey)))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("error sending request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return 0, fmt.Errorf("unexpected status from Redmine: %s (not http.StatusCreated)", resp.Status)
	}

	var userResponse struct {
		User struct {
			ID int `json:"id"`
		} `json:"user"`
	}
	decoder := json.NewDecoder(resp.Body)
	err = decoder.Decode(&userResponse)
	if err != nil {
		return 0, fmt.Errorf("error decoding Redmine response: %v", err)
	}

	return userResponse.User.ID, nil
}

func DeleteRedmineUser(userID int) error {
	adminKeyFile := filepath.Join(*workdir, "redmine", "data", "admin_api_key.txt")
	redmineKey, err := os.ReadFile(adminKeyFile)
	if err != nil {
		return fmt.Errorf("error reading Redmine key: %v", err)
	}

	req, err := http.NewRequest("DELETE", fmt.Sprintf("http://%s:3000/users/%d.json", *bindIP, userID), nil)
	if err != nil {
		return fmt.Errorf("error creating DELETE request for Redmine user: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Redmine-API-Key", strings.TrimSpace(string(redmineKey)))

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error sending DELETE request to Redmine: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("error deleting user from Redmine, got status: %s", resp.Status)
	}

	return nil
}

func UpdateRedmineAdminEmail() error {
	adminKeyFile := filepath.Join(*workdir, "redmine", "data", "admin_api_key.txt")
	redmineKey, err := os.ReadFile(adminKeyFile)
	if err != nil {
		return fmt.Errorf("error reading redmine key: %v", err)
	}

	body := map[string]any{
		"user": map[string]any{
			"mail": "admin@" + *hostname,
		},
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("error marshalling JSON: %v", err)
	}

	req, err := http.NewRequest("PUT",
		fmt.Sprintf("http://%s:3000/my/account.json", *bindIP),
		bytes.NewBuffer(jsonBody))

	if err != nil {
		return fmt.Errorf("error creating request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Redmine-API-Key", strings.TrimSpace(string(redmineKey)))

	client := &http.Client{}
	var resp *http.Response

	const maxRetries = 5
	const retryDelay = 2 * time.Second

	for i := 0; i < maxRetries; i++ {
		if resp, err = client.Do(req); err == nil {
			break
		}
		fmt.Printf("Error sending request: %v, retrying in %v seconds...\n", err, retryDelay.Seconds())
		time.Sleep(retryDelay)
	}
	if err != nil {
		return fmt.Errorf("after %d retries, final error: %v", maxRetries, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status from Redmine: %s (not http.StatusNoContent)", resp.Status)
	}

	return nil
}
