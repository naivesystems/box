package main

import (
	"crypto/rand"
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
	"sync"
	"syscall"
)

var defaultHttpdImage = "naive.systems/box/httpd:dev"
var httpdImage = flag.String("httpd_image", defaultHttpdImage, "")

var httpdCmd *exec.Cmd

var (
	stopHttpd      bool
	stopHttpdMutex sync.Mutex
)

func StartHttpd() error {
	httpdDir := filepath.Join(*workdir, "httpd")
	err := os.MkdirAll(httpdDir, 0700)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", httpdDir, err)
	}
	PodmanKill("httpd")
	versionFile := filepath.Join(httpdDir, "version.txt")
	if !exists(versionFile) {
		log.Printf("%s does not exist. Initializing...", versionFile)
		err := InitHttpd()
		if err != nil {
			return err
		}
		log.Printf("httpd has been successfully initialized.")
	}
	err = RunHttpd()
	if err != nil {
		return err
	}
	return nil
}

func InitHttpd() error {
	confDir := filepath.Join(*workdir, "httpd", "conf.d")
	err := os.MkdirAll(confDir, 0700)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", confDir, err)
	}

	logsDir := filepath.Join(*workdir, "httpd", "logs")
	err = os.MkdirAll(logsDir, 0700)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", logsDir, err)
	}

	metadataDir := filepath.Join(*workdir, "httpd", "metadata")
	err = os.MkdirAll(metadataDir, 0700)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", metadataDir, err)
	}

	versionFile := filepath.Join(*workdir, "httpd", "version.txt")
	return os.WriteFile(versionFile, []byte("Apache/2.4.57 (Fedora Linux)"), 0600)
}

func RunHttpd() error {
	httpdDir := filepath.Join(*workdir, "httpd")
	confDir := filepath.Join(httpdDir, "conf.d")
	logsDir := filepath.Join(httpdDir, "logs")
	metadataDir := filepath.Join(httpdDir, "metadata")

	os.MkdirAll(confDir, 0700)
	os.MkdirAll(logsDir, 0700)
	os.MkdirAll(metadataDir, 0700)

	// Generate x0auth_openidc.conf
	passphrase, err := GenerateOIDCCryptoPassphrase(80)
	if err != nil {
		return err
	}

	clientSecret, err := LoadHttpdClientSecret()
	if err != nil {
		return err
	}

	confStr := fmt.Sprintf(`
OIDCRedirectURI /OIDCRedirectURI
OIDCCryptoPassphrase "%s"
OIDCMetadataDir /var/cache/httpd/mod_auth_openidc/metadata
OIDCScope "openid"
OIDCClientID "httpd"
OIDCClientSecret "%s"
OIDCCookieDomain %s
OIDCStateMaxNumberOfCookies 100 true
OIDCSessionInactivityTimeout 72000
OIDCSessionMaxDuration 72000
OIDCSessionType server-cache:persistent
OIDCCacheType file
OIDCCacheDir /var/cache/httpd/mod_auth_openidc/cache
OIDCCacheFileCleanInterval 72000
OIDCDiscoverURL "https://%s:8443/discover.html"
OIDCDefaultURL "https://%s:8443/index.html"
OIDCRemoteUserClaim "preferred_username"
`, passphrase, clientSecret, *hostname, *hostname, *hostname)

	oidcConf := filepath.Join(confDir, "x0auth_openidc.conf")
	err = os.WriteFile(oidcConf, []byte(confStr), 0600)
	if err != nil {
		return err
	}

	// Generate metadata
	providerPath := filepath.Join(metadataDir, *hostname+"%3A9992%2Frealms%2Fnsbox.provider")
	err = WriteOpenIDConfiguration(providerPath)
	if err != nil {
		return err
	}

	clientStr := fmt.Sprintf(`{
  "client_id": "httpd",
  "client_secret": "%s",
  "response_type": "code"
}
`, clientSecret)

	clientPath := filepath.Join(metadataDir, *hostname+"%3A9992%2Frealms%2Fnsbox.client")
	err = os.WriteFile(clientPath, []byte(clientStr), 0600)
	if err != nil {
		return err
	}

	return PodmanRunHttpd()
}

func PodmanRunHttpd() error {
	if *releaseTag != "dev" && *httpdImage == defaultHttpdImage {
		err := flag.Set("httpd_image", "ghcr.io/naivesystems/box/httpd:"+*releaseTag)
		if err != nil {
			return fmt.Errorf("failed to set httpd_image: %v", err)
		}
	}

	certsDir := filepath.Join(*workdir, "certs")
	httpdDir := filepath.Join(*workdir, "httpd")
	confDir := filepath.Join(httpdDir, "conf.d")
	logsDir := filepath.Join(httpdDir, "logs")
	metadataDir := filepath.Join(httpdDir, "metadata")

	// Start the container
	httpdCmd = exec.Command("podman", "run", "--rm",
		"--name", "httpd", "--replace",
		"-v", certsDir+":/certs",
		"-v", logsDir+":/etc/httpd/logs",
		"-v", confDir+":/mnt/conf.d",
		"-v", metadataDir+":/var/cache/httpd/mod_auth_openidc/metadata:O",
		"--network=host",
		*httpdImage,
		"/usr/local/bin/run_httpd", "--hostname", *hostname)
	if err := RedirectPipes(httpdCmd, "H", "\033[0;35m"); err != nil {
		return fmt.Errorf("failed to redirect pipes: %v", err)
	}
	log.Printf("Executing %s", httpdCmd.String())
	if err := httpdCmd.Start(); err != nil {
		return fmt.Errorf("failed to start httpd: %v", err)
	}
	go WaitAndRestartHttpd()
	return nil
}

func WaitAndRestartHttpd() {
	if err := httpdCmd.Wait(); err != nil {
		log.Printf("httpd exited with error: %v", err)
	}
	stopHttpdMutex.Lock()
	defer stopHttpdMutex.Unlock()
	if stopHttpd {
		return
	}
	/*
		TODO

		Restart causes an issue that mod_auth_openidc cookies are invalidated
		and the user is redirected to the discover page once again. To mitigate
		this, we probably should persist the cookies directory.
	*/
	log.Printf("*** RESTARTING HTTPD ***")
	if err := PodmanRunHttpd(); err != nil {
		log.Printf("Failed to restart httpd: %v", err)
	}
}

func GenerateOIDCCryptoPassphrase(length int) (string, error) {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		if _, err := rand.Read(b[i : i+1]); err != nil {
			return "", err
		}
		b[i] = charset[b[i]%byte(len(charset))]
	}
	return string(b), nil
}

func LoadHttpdClientSecret() (string, error) {
	path := filepath.Join(*workdir, "keycloak", "client_secret.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var secret struct {
		ID     string `json:"id"`
		Secret string `json:"secret"`
	}
	err = json.Unmarshal(data, &secret)
	if err != nil {
		return "", err
	}
	return secret.Secret, nil
}

func WriteOpenIDConfiguration(path string) error {
	// httpd/run uses sed to replace 127.0.0.1 with the actual hostname
	url := "https://127.0.0.1:9992/realms/nsbox/.well-known/openid-configuration"

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: tr}

	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP request failed with status: %s", resp.Status)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %v", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write to file: %v", err)
	}

	return nil
}

func StopHttpd() {
	stopHttpdMutex.Lock()
	defer stopHttpdMutex.Unlock()
	stopHttpd = true
	err := httpdCmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		log.Printf("Failed to stop httpd: %v", err)
	}
	PodmanKill("httpd")
}
