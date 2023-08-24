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
	"syscall"
)

var httpdImage = flag.String("httpd_image", "naive.systems/box/httpd:dev", "")

var httpdCmd *exec.Cmd

var socatCmds []*exec.Cmd

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

	socketsDir := filepath.Join(*workdir, "httpd", "sockets")
	err = os.MkdirAll(socketsDir, 0755)
	if err != nil {
		return fmt.Errorf("os.MkdirAll(%s): %v", socketsDir, err)
	}

	versionFile := filepath.Join(*workdir, "httpd", "version.txt")
	return os.WriteFile(versionFile, []byte("Apache/2.4.57 (Fedora Linux)"), 0600)
}

func RunHttpd() error {
	certsDir := filepath.Join(*workdir, "certs")
	httpdDir := filepath.Join(*workdir, "httpd")
	confDir := filepath.Join(httpdDir, "conf.d")
	logsDir := filepath.Join(httpdDir, "logs")
	metadataDir := filepath.Join(httpdDir, "metadata")
	socketsDir := filepath.Join(httpdDir, "sockets")

	os.MkdirAll(confDir, 0700)
	os.MkdirAll(logsDir, 0700)
	os.MkdirAll(metadataDir, 0700)
	os.MkdirAll(socketsDir, 0755)

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

	// Generate sockets
	buildbotSock := filepath.Join(socketsDir, "buildbot.sock")
	err = socat(buildbotSock, *bindIP+":8010")
	if err != nil {
		return err
	}

	gerritSock := filepath.Join(socketsDir, "gerrit.sock")
	err = socat(gerritSock, *bindIP+":8081")
	if err != nil {
		return err
	}

	redmineSock := filepath.Join(socketsDir, "redmine.sock")
	err = socat(redmineSock, *bindIP+":3000")
	if err != nil {
		return err
	}

	// Start the container
	httpdCmd = exec.Command("podman", "run", "--rm",
		"--name", "httpd", "--replace",
		// "--userns=keep-id:uid=1000,gid=1000",
		"-v", certsDir+":/certs",
		"-v", logsDir+":/etc/httpd/logs",
		"-v", confDir+":/mnt/conf.d",
		"-v", socketsDir+":/mnt/sockets",
		"-v", metadataDir+":/var/cache/httpd/mod_auth_openidc/metadata:O",
		"-p", "0.0.0.0:8080:8080/tcp",
		"-p", "0.0.0.0:8443:8443/tcp",
		"-p", "0.0.0.0:9441:9441/tcp",
		"-p", "0.0.0.0:9442:9442/tcp",
		"-p", "0.0.0.0:9443:9443/tcp",
		*httpdImage,
		"/usr/local/bin/run_httpd", "--hostname", *hostname)
	err = RedirectPipes(httpdCmd, "H", "\033[0;35m")
	if err != nil {
		return fmt.Errorf("failed to redirect pipes: %v", err)
	}
	log.Printf("Executing %s", httpdCmd.String())
	err = httpdCmd.Start()
	if err != nil {
		return fmt.Errorf("failed to start httpd: %v", err)
	}
	return nil
}

func socat(udsPath, tcpAddr string) error {
	cmd := exec.Command("socat", "-d", "-d",
		"UNIX-LISTEN:"+udsPath+",mode=777,fork", "TCP4:"+tcpAddr)
	err := RedirectPipes(cmd, "S", "\033[0;35m")
	if err != nil {
		return fmt.Errorf("failed to redirect pipes: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		return err
	}
	socatCmds = append(socatCmds, cmd)
	return nil
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
	err := httpdCmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		log.Printf("Failed to stop httpd: %v", err)
	}
	PodmanKill("httpd")
	for i, cmd := range socatCmds {
		err := cmd.Process.Signal(syscall.SIGTERM)
		if err != nil {
			log.Printf("Failed to stop socat%d: %v", i, err)
		}
	}
}
