package pip

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed output/*
var content embed.FS

func extract(contentFS embed.FS, currentDir, targetDir string) error {
	entries, err := contentFS.ReadDir(currentDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		sourcePath := filepath.Join(currentDir, entry.Name())
		destPath := filepath.Join(targetDir, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(destPath, os.ModePerm); err != nil {
				return err
			}
			if err := extract(contentFS, sourcePath, destPath); err != nil {
				return err
			}
		} else {
			data, err := contentFS.ReadFile(sourcePath)
			if err != nil {
				return err
			}
			if err := os.WriteFile(destPath, data, os.ModePerm); err != nil {
				return err
			}
		}
	}
	return nil
}

func InitSandbox(workdir string) error {
	tempDir, err := os.MkdirTemp("", "extracted_packages_")
	if err != nil {
		return fmt.Errorf("os.MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tempDir)

	if err := extract(content, "output", tempDir); err != nil {
		return fmt.Errorf("extract: %v", err)
	}
	fmt.Println("Files extracted successfully to:", tempDir)

	cmd := exec.Command("python3", "-m", "venv", "sandbox")
	cmd.Dir = workdir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v", cmd.String(), err)
	}

	pip := filepath.Join(workdir, "sandbox", "bin", "pip")

	cmd = exec.Command(pip, "install", "--upgrade", "pip")
	cmd.Dir = workdir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v", cmd.String(), err)
	}

	cmd = exec.Command(pip, "install", "--no-index", "--find-links="+tempDir, "wheel", "buildbot[bundle]", "buildbot-www-react", "txrequests")
	cmd.Dir = workdir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v", cmd.String(), err)
	}

	buildbot := filepath.Join(workdir, "sandbox", "bin", "buildbot")

	cmd = exec.Command(buildbot, "create-master", "master")
	cmd.Dir = workdir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v", cmd.String(), err)
	}

	return nil
}
