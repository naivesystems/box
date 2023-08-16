package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

var redmineImage = flag.String("redmine_image", "naive.systems/box/redmine:dev", "")

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
	}
	return RunRedmine()
}

func InitRedmine() error {
	_, err := PodmanRunRedmine(true, "/home/redmine/init")
	if err != nil {
		return fmt.Errorf("failed to initialize Redmine: %v", err)
	}
	return nil
}

func RunRedmine() error {
	cmd, err := PodmanRunRedmine(false, "/home/redmine/run")
	if err != nil {
		return fmt.Errorf("failed to start Redmine: %v", err)
	}
	redmineCmd = cmd
	return nil
}

func PodmanRunRedmine(wait bool, args ...string) (*exec.Cmd, error) {
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
		"-p", "0.0.0.0:3000:3000/tcp",
		*redmineImage,
	}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command("podman", cmdArgs...)
	err = RedirectPipes(cmd, "R", "\033[1;31m")
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
