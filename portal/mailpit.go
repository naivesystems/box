package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

var mailpitBin = flag.String("mailpit_bin", "", "Absolute path to the mailpit binary")

var mailpitCmd *exec.Cmd

func StartMailpit() error {
	if *mailpitBin == "" {
		return errors.New("--mailpit_bin must be specified")
	}
	if !filepath.IsAbs(*mailpitBin) {
		return fmt.Errorf("--mailpit_bin %s is not an absolute path", *mailpitBin)
	}
	if !exists(*mailpitBin) {
		return fmt.Errorf("--mailpit_bin %s does not exist", *mailpitBin)
	}

	mailpitDir := filepath.Join(*workdir, "mailpit")
	if err := os.MkdirAll(mailpitDir, 0700); err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", mailpitDir, err)
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
