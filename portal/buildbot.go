package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"naive.systems/box/buildbot"
	"naive.systems/box/buildbot/pip"
)

var bb *buildbot.Buildbot

func StartBuildbot() error {
	buildbotDir := filepath.Join(*workdir, "buildbot")
	err := os.MkdirAll(buildbotDir, 0700)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", buildbotDir, err)
	}
	versionFile := filepath.Join(buildbotDir, "version.txt")
	if !exists(versionFile) {
		log.Printf("%s does not exist. Initializing...", versionFile)
		err := InitBuildbot()
		if err != nil {
			return err
		}
		log.Printf("Buildbot has been successfully initialized.")
	}
	err = RunBuildbot()
	if err != nil {
		return err
	}
	return nil
}

func InitBuildbot() error {
	buildbotDir := filepath.Join(*workdir, "buildbot")

	// create both sandbox and master directories
	if err := pip.InitSandbox(buildbotDir); err != nil {
		return err
	}

	// create ssh keys
	sshDir := filepath.Join(buildbotDir, "ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", sshDir, err)
	}

	privateKeyPath := filepath.Join(sshDir, "id_ed25519")
	if err := os.Remove(privateKeyPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("rm -f '%s': %v", privateKeyPath, err)
	}

	publicKeyPath := privateKeyPath + ".pub"
	if err := os.Remove(publicKeyPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("rm -f '%s': %v", publicKeyPath, err)
	}

	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", privateKeyPath, "-N", "", "-q")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v", cmd.String(), err)
	}

	// write version file
	buildbot := filepath.Join(*workdir, "buildbot", "sandbox", "bin", "buildbot")
	cmd = exec.Command(buildbot, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v", cmd.String(), err)
	}

	versionFile := filepath.Join(buildbotDir, "version.txt")
	if err := os.WriteFile(versionFile, output, 0755); err != nil {
		return fmt.Errorf("failed to write version file: %v", err)
	}

	return nil
}

func RunBuildbot() error {
	buildbotDir := filepath.Join(*workdir, "buildbot")

	bb = buildbot.New()
	bb.WorkDir = buildbotDir
	bb.IdentityFile = filepath.Join(buildbotDir, "ssh", "id_ed25519")
	bb.WorkersList = "worker,password"
	bb.WWWProtocol = "https"
	bb.WWWHost = *hostname
	bb.PublicPort = 9443

	return bb.Start()
}

func StopBuildbot() {
	if err := bb.Stop(); err != nil {
		log.Printf("Failed to stop Buildbot: %v", err)
	}
}
