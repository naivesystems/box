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
	"syscall"
)

var mailpitBin = flag.String("mailpit_bin", "", "Absolute path to the mailpit binary")
var mailpitTag = flag.String("mailpit_tag", "1.8.2+nsbox.2023110501", "Release tag for the mailpit binary")

var mailpitCmd *exec.Cmd

func downloadMailpit() error {
	tempDir, err := os.MkdirTemp("", "mailpit_download_")
	if err != nil {
		return fmt.Errorf("os.MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tempDir)
	url := fmt.Sprintf("https://github.com/naivesystems/mailpit/releases/download/%s/mailpit-linux-amd64.tar.gz", *mailpitTag)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	tarballPath := filepath.Join(tempDir, "mailpit.tar.gz")
	out, err := os.Create(tarballPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}

	cmd := exec.Command("tar", "-xzf", tarballPath, "-C", filepath.Dir(*mailpitBin))
	err = cmd.Run()
	if err != nil {
		return err
	}

	return nil
}

func StartMailpit() error {
	mailpitDir := filepath.Join(*workdir, "mailpit")
	if err := os.MkdirAll(mailpitDir, 0700); err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", mailpitDir, err)
	}

	if *mailpitBin == "" {
		err := flag.Set("mailpit_bin", filepath.Join(*workdir, "mailpit", "mailpit"))
		if err != nil {
			return fmt.Errorf("failed to set mailpit_bin: %v", err)
		}
		if !exists(*mailpitBin) {
			err := downloadMailpit()
			if err != nil {
				return fmt.Errorf("failed to download mailpit: %v", err)
			}
		}
	}
	if !filepath.IsAbs(*mailpitBin) {
		return fmt.Errorf("--mailpit_bin %s is not an absolute path", *mailpitBin)
	}
	if !exists(*mailpitBin) {
		return fmt.Errorf("--mailpit_bin %s does not exist", *mailpitBin)
	}

	mailpitCmd = exec.Command(*mailpitBin,
		"--db-file", filepath.Join(mailpitDir, "mails.db"),
		"--max", "100000",
		"--listen", *bindIP+":8025",
		"--smtp", *bindIP+":9025")

	if err := RedirectPipes(mailpitCmd, "M", "\033[0;34m"); err != nil {
		return fmt.Errorf("failed to redirect pipes: %v", err)
	}

	log.Printf("Executing %s", mailpitCmd.String())
	return mailpitCmd.Start()
}

func StopMailpit() {
	if err := mailpitCmd.Process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("Failed to stop Mailpit: %v", err)
	}
}
