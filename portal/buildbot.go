package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"naive.systems/box/buildbot"
	"naive.systems/box/buildbot/pip"
	"naive.systems/box/portal/gerrit"
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

	bb = buildbot.New()
	bb.WorkDir = buildbotDir
	bb.IdentityFile = filepath.Join(buildbotDir, "ssh", "id_ed25519")
	bb.WorkersList = "worker,password"
	bb.WWWProtocol = "https"
	bb.WWWHost = *hostname
	bb.PublicPort = 9443
	bb.Gerrit.Server = *bindIP
	bb.Gerrit.Port = 29418

	gps, err := PrepareBuildbotAccountInGerrit()
	if err != nil {
		return err
	}
	err = bb.Start(gps)
	if err != nil {
		return err
	}
	go WatchGerritProjects()
	go WatchGerritChanges()
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

func testGerritConnection(username, host string, portNumber int) error {
	buildbotDir := filepath.Join(*workdir, "buildbot")
	sshDir := filepath.Join(buildbotDir, "ssh")
	privateKeyPath := filepath.Join(sshDir, "id_ed25519")

	port := strconv.Itoa(portNumber)
	cmd := exec.Command("ssh", "-T",
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-i", privateKeyPath,
		"-l", username,
		"-p", port,
		host)
	out, err := cmd.CombinedOutput()
	if !strings.Contains(string(out), "you have successfully connected over SSH") {
		log.Printf("ssh: '%s'", string(out))
		if err != nil {
			log.Printf("ssh: %v", err)
		}
		return errors.New("unable to connect over SSH")
	}
	log.Printf("tested connection to %s:%d", host, portNumber)
	return nil
}

func testGerritProjectAccess(project, username, host string, portNumber int) error {
	buildbotDir := filepath.Join(*workdir, "buildbot")
	sshDir := filepath.Join(buildbotDir, "ssh")
	privateKeyPath := filepath.Join(sshDir, "id_ed25519")

	port := strconv.Itoa(portNumber)
	cmd := exec.Command("ssh", "-T",
		"-i", privateKeyPath,
		"-l", username,
		"-p", port,
		host,
		"gerrit", "ls-projects", "-p", project)
	out, err := cmd.CombinedOutput()
	foundProject := false
	for _, line := range strings.Split(string(out), "\n") {
		if line == project {
			foundProject = true
			break
		}
	}
	if !foundProject {
		log.Printf("ssh: '%s'", string(out))
		if err != nil {
			log.Printf("ssh: %v", err)
		}
		return fmt.Errorf("unable to find project '%s'", project)
	}
	if err := testCloneProject(project, username, host, portNumber); err != nil {
		return fmt.Errorf("unable to clone '%s': %v", project, err)
	}
	log.Printf("%s has access to project '%s'", username, project)
	return nil
}

func testCloneProject(project, username, host string, portNumber int) error {
	buildbotDir := filepath.Join(*workdir, "buildbot")
	sshDir := filepath.Join(buildbotDir, "ssh")
	privateKeyPath := filepath.Join(sshDir, "id_ed25519")

	// Construct the SSH URL
	sshURL := fmt.Sprintf("ssh://%s@%s:%d/%s.git", username, host, portNumber, project)

	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "project-clone-")
	if err != nil {
		return errors.New("failed to create temporary directory: " + err.Error())
	}
	defer func() {
		// Clean up the temporary directory after usage.
		_ = os.RemoveAll(tempDir)
	}()

	// Execute the git clone command
	cmd := exec.Command("git", "clone", "-c", "core.sshCommand=ssh -i "+privateKeyPath, sshURL, tempDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.New("failed to clone project: " + string(output))
	}

	return nil
}

func grantGerritPermissions(client *gerrit.Client, project, host string, portNumber int) error {
	group, err := client.GetGroup("Service Users")
	if err != nil {
		return err
	}
	log.Printf("%s group ID is %s", group.Name, group.ID)

	payload := map[string]any{
		"add": map[string]any{
			"refs/*": map[string]any{
				"permissions": map[string]any{
					"read": map[string]any{
						"rules": map[string]any{
							group.ID: map[string]any{
								"action": "ALLOW",
							},
						},
					},
				},
			},
			"refs/heads/*": map[string]any{
				"permissions": map[string]any{
					"label-Verified": map[string]any{
						"label": "Verified",
						"rules": map[string]any{
							group.ID: map[string]any{
								"action": "ALLOW",
								"min":    -1,
								"max":    +1,
							},
						},
					},
					"read": map[string]any{
						"rules": map[string]any{
							"global:Anonymous-Users": map[string]any{
								"action": "DENY",
							},
						},
					},
				},
			},
		},
	}

	endpoint := fmt.Sprintf("projects/%s/access", project)
	_, err = client.MakeJSONRequest(http.MethodPost, endpoint, payload)
	return err
}

func testGerritConnectionWithRetries(username, host string, portNumber int) error {
	for i := 0; i < 10; i++ {
		err := testGerritConnection(username, host, portNumber)
		if err == nil {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return testGerritConnection(username, host, portNumber)
}

func createVerifiedLabel(client *gerrit.Client) error {
	payload := map[string]any{
		"commit_message": "Create Verified Label",
		"values": map[string]string{
			"-1": "Fails",
			" 0": "No score",
			"+1": "Verified",
		},
		"function":       "MaxWithBlock",
		"copy_condition": "changekind:NO_CHANGE",
	}
	endpoint := "projects/All-Projects/labels/Verified"
	_, err := client.MakeJSONRequest(http.MethodPut, endpoint, payload)
	return err
}

func PrepareBuildbotAccountInGerrit() ([]*gerrit.Project, error) {
	const username = "buildbot"

	/*
		TODO set account full name

		Otherwise some messages do not look good. For example:
		remote: The following approvals got outdated and were removed:
		remote: * Verified+1 by Name of user not set #1000001
	*/

	sshKey, err := bb.PublicKey()
	if err != nil {
		return nil, fmt.Errorf("error loading public key: %w", err)
	}

	// Ensure the user exists
	if err := AddGerritUser(username); err != nil {
		return nil, fmt.Errorf("error ensuring user exists: %w", err)
	}

	client := gerrit.NewClient("http://"+*bindIP+":8081", "admin")
	if err := client.Login(); err != nil {
		return nil, fmt.Errorf("error logging into gerrit: %w", err)
	}

	// Create the Verified label
	if err := createVerifiedLabel(client); err != nil {
		return nil, fmt.Errorf("error creating Verified label: %w", err)
	}

	// Grant permissions to Service Users
	if err := grantGerritPermissions(client, "All-Projects", *bindIP, 29418); err != nil {
		return nil, fmt.Errorf("error granting Gerrit permissions: %w", err)
	}

	// Add the user to the "Service Users" group
	if err := client.AddMemberToGroup("Service Users", username); err != nil {
		return nil, fmt.Errorf("error adding user to group: %w", err)
	}

	// Ensure the user has the specified SSH key
	if err := client.AddSSHKeyToAccount(username, sshKey); err != nil {
		return nil, fmt.Errorf("error ensuring SSH key is added: %w", err)
	}

	if err := testGerritConnectionWithRetries(username, *bindIP, 29418); err != nil {
		return nil, fmt.Errorf("error testing Gerrit connection: %w", err)
	}

	if err := testGerritProjectAccess("All-Projects", username, *bindIP, 29418); err != nil {
		return nil, fmt.Errorf("error testing Gerrit project access: %w", err)
	}

	log.Println("Prepared buildbot account successfully")

	gps, err := client.ListProjects()
	if err != nil {
		return nil, fmt.Errorf("error listing projects: %w", err)
	}
	return gps, nil
}

func StopBuildbot() {
	if err := bb.Stop(); err != nil {
		log.Printf("Failed to stop Buildbot: %v", err)
	}
}

func WatchGerritProjects() {
	for {
		client := gerrit.NewClient("http://"+*bindIP+":8081", "admin")
		if err := client.Login(); err != nil {
			log.Printf("WatchGerritProjects: error logging into gerrit: %v", err)
			time.Sleep(30 * time.Second)
			continue
		}

		oldProjects, err := client.ListProjects()
		if err != nil {
			log.Printf("WatchGerritProjects: error listing old projects: %v", err)
			time.Sleep(30 * time.Second)
			continue
		}
		sortProjects(oldProjects)

		for {
			time.Sleep(5 * time.Second)

			newProjects, err := client.ListProjects()
			if err != nil {
				log.Printf("WatchGerritProjects: error listing new projects: %v", err)
				time.Sleep(30 * time.Second)
				break
			}
			sortProjects(newProjects)

			if sameProjects(oldProjects, newProjects) {
				continue
			}

			if err := bb.RewriteConfig(newProjects); err != nil {
				log.Printf("WatchGerritProjects: error rewriting buildbot config: %v", err)
				return
			}

			if err := bb.Restart(); err != nil {
				log.Printf("WatchGerritProjects: error restarting buildbot: %v", err)
				return
			}

			oldProjects = newProjects
		}
	}
}

func sortProjects(projects []*gerrit.Project) {
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].ID < projects[j].ID
	})
}

func sameProjects(a, b []*gerrit.Project) bool {
	if len(a) != len(b) {
		return false
	}

	for i, x := range a {
		if !sameProject(x, b[i]) {
			return false
		}
	}

	return true
}

func sameProject(a, b *gerrit.Project) bool {
	return a.ID == b.ID && a.Name == b.Name && a.Description == b.Description
}

func WatchGerritChanges() {
	buildbotDir := filepath.Join(*workdir, "buildbot")
	sshDir := filepath.Join(buildbotDir, "ssh")
	privateKeyPath := filepath.Join(sshDir, "id_ed25519")

	for {
		time.Sleep(5 * time.Second)

		cmd := exec.Command("ssh", "-T",
			"-i", privateKeyPath,
			"-l", "buildbot",
			"-p", "29418",
			*bindIP,
			"gerrit", "stream-events", "-s", "patchset-created", "-s", "change-merged")
		cmd.Stderr = os.Stderr
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("Error getting stdout pipe: %v\n", err)
			continue
		}

		if err := cmd.Start(); err != nil {
			log.Printf("Error starting command: %v\n", err)
			continue
		}

		log.Println("WatchGerritChanges: started")
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			log.Printf("WatchGerritChanges: %s", line)
			processGerritEvent(line)
		}

		if err := cmd.Wait(); err != nil {
			log.Printf("Error waiting for command: %v\n", err)
		}
	}
}

type GerritEvent struct {
	Type   string       `json:"type"`
	Change GerritChange `json:"change"`
}

type GerritChange struct {
	URL           string `json:"url"`
	CommitMessage string `json:"commitMessage"`
}

func processGerritEvent(line string) {
	var event GerritEvent
	err := json.Unmarshal([]byte(line), &event)
	if err != nil {
		log.Printf("Error unmarshalling JSON: %v\n", err)
		return
	}

	r := regexp.MustCompile(`(?i)(?m)^Bug-Id:\s*(\d+)`)
	matches := r.FindAllStringSubmatch(event.Change.CommitMessage, -1)
	log.Printf("WatchGerritChanges: %d matches", len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			sendUpdateToRedmine(match[1], event)
		}
	}
}

func sendUpdateToRedmine(bugID string, event GerritEvent) {
	log.Printf("WatchGerritChanges: Bug-Id: %s", bugID)

	adminKeyFile := filepath.Join(*workdir, "redmine", "data", "admin_api_key.txt")
	redmineKey, err := os.ReadFile(adminKeyFile)
	if err != nil {
		log.Printf("Error reading redmine key: %v\n", err)
		return
	}

	indentedCommitMsg := indentCommitMessage(event.Change.CommitMessage)
	body := map[string]interface{}{
		"issue": map[string]string{
			"notes": event.Type + " " + event.Change.URL + "\n\n" + indentedCommitMsg,
		},
	}

	jsonBody, _ := json.Marshal(body)
	client := &http.Client{}
	req, err := http.NewRequest("PUT", fmt.Sprintf("http://%s:3000/issues/%s.json", *bindIP, bugID), bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("Error creating request: %v\n", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Redmine-API-Key", strings.TrimSpace(string(redmineKey)))
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending request: %v\n", err)
		return
	}
	defer resp.Body.Close()

	log.Println(resp.Status)
}

func indentCommitMessage(commitMsg string) string {
	lines := strings.Split(commitMsg, "\n")
	for i, line := range lines {
		lines[i] = "    " + line // Indent each line by 4 spaces
	}
	return strings.Join(lines, "\n")
}
