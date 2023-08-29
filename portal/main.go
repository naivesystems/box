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

	err = StartBuildbot()
	if err != nil {
		log.Printf("Failed to start Buildbot: %v", err)
		StopGerrit()
		StopRedmine()
		StopKeycloak()
		os.Exit(1)
	}

	err = StartHttpd()
	if err != nil {
		log.Printf("Failed to start httpd: %v", err)
		StopBuildbot()
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
		StopBuildbot()
		StopGerrit()
		StopRedmine()
		StopKeycloak()
		os.Exit(0)
	}()

	// TODO
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/users/new", handleNewUser)
	http.HandleFunc("/users/create", handleCreateUser)
	log.Fatal(http.ListenAndServe(*bindIP+":7777", nil))
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	u := r.Header.Get("X-Remote-User")
	if u != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	w.Header().Add("Content-Type", "text/html")
	w.Write([]byte(fmt.Sprintf(`<!DOCTYPE html>
<html>
	<head>
		<meta charset="utf-8"/>
		<title>nsbox</title>
	</head>
	<body>
		<p>Hello %s</p>
		<a href="/users/new">Create a new user</a>
	</body>
</html>
`, u)))
}

func handleNewUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	u := r.Header.Get("X-Remote-User")
	if u != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	w.Header().Add("Content-Type", "text/html")
	w.Write([]byte(fmt.Sprintf(`<!DOCTYPE html>
<html>
	<head>
		<meta charset="utf-8"/>
		<title>nsbox</title>
	</head>
	<body>
		<p>Hello %s</p>
		<form method="POST" action="/users/create">
			<p>
				<label for="username">Username</label>
				<input type="text" id="username" name="username" required/>
			</p>
			<p>
				<label for="first_name">First Name</label>
				<input type="text" id="first_name" name="first_name" required/>
			</p>
			<p>
				<label for="last_name">Last Name</label>
				<input type="text" id="last_name" name="last_name" required/>
			</p>
			<p>
				<button type="submit">Create</button>
			</p>
		</form>
	</body>
</html>
`, u)))
}

func handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	u := r.Header.Get("X-Remote-User")
	if u != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	err := r.ParseForm()
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	username := r.FormValue("username")
	firstName := r.FormValue("first_name")
	lastName := r.FormValue("last_name")

	if username == "" || firstName == "" || lastName == "" {
		http.Error(w, "All fields are required", http.StatusBadRequest)
		return
	}

	// Add user to Gerrit (idempotent operation)
	err = AddGerritUser(username)
	if err != nil {
		http.Error(w, "Failed to add user to Gerrit: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Add user to Redmine and obtain the user's ID
	redmineUserID, err := AddRedmineUser(username, firstName, lastName)
	if err != nil {
		http.Error(w, "Failed to add user to Redmine: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Add user to Keycloak
	password, err := AddKeycloakUser(username, firstName, lastName)
	if err != nil {
		// Rollback Redmine user creation using the user ID
		if deleteErr := DeleteRedmineUser(redmineUserID); deleteErr != nil {
			log.Printf("Error deleting Redmine user (ID: %d) during rollback: %v", redmineUserID, deleteErr)
		}
		http.Error(w, "Failed to add user to Keycloak: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Send a response back in HTML format
	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)

	htmlResponse := `
<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1, shrink-to-fit=no">
	<title>User Creation Success</title>
	<style>
		body {
			font-family: Arial, sans-serif;
			margin: 40px;
			background-color: #f5f5f5;
		}
		.container {
			background-color: white;
			padding: 20px;
			border-radius: 5px;
			box-shadow: 0px 0px 15px rgba(0, 0, 0, 0.1);
		}
		.highlight {
			font-weight: bold;
		}
	</style>
</head>
<body>
	<div class="container">
		<h2>Success</h2>
		<p>User added successfully. Please note down the provided initial password: <span class="highlight">%s</span></p>
	</div>
</body>
</html>
`
	w.Write([]byte(fmt.Sprintf(htmlResponse, password)))
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
