package gerrit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	RemoteURL    string // URL of the Gerrit instance.
	RemoteUser   string // Remote user to be set as the REMOTE_USER header.
	HTTPPassword string // The HTTP password obtained after login.

	RemoteUserName  string
	RemoteUserEmail string
}

type Project struct {
	ID          string `json:"id"`             // The URL encoded project name.
	Name        string `json:"name,omitempty"` // The name of the project.
	Description string `json:"description,omitempty"`
}

type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func NewClient(remoteURL, remoteUser, name, email string) *Client {
	return &Client{
		RemoteURL:       remoteURL,
		RemoteUser:      remoteUser,
		RemoteUserName:  name,
		RemoteUserEmail: email,
	}
}

func (c *Client) Login() error {
	// Create an HTTP client with a CookieJar
	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("failed to create cookie jar: %w", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}

	// Step 1: Initial Login Request
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/login/", c.RemoteURL), nil)
	if err != nil {
		return fmt.Errorf("failed to create initial login request: %w", err)
	}
	req.Header.Set("REMOTE_USER", c.RemoteUser)
	req.Header.Set("OIDC_CLAIM_name", c.RemoteUserName)
	req.Header.Set("OIDC_CLAIM_email", c.RemoteUserEmail)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send initial login request: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		return fmt.Errorf("unexpected status code during initial login: %d", resp.StatusCode)
	}

	// Step 2: Accessing the Settings Page to get XSRF Token
	resp, err = client.Get(fmt.Sprintf("%s/settings/", c.RemoteURL))
	if err != nil {
		return fmt.Errorf("failed to access settings page: %w", err)
	}
	resp.Body.Close()

	var xsrfToken string
	for _, cookie := range jar.Cookies(resp.Request.URL) {
		if cookie.Name == "XSRF_TOKEN" {
			xsrfToken = cookie.Value
			break
		}
	}

	if xsrfToken == "" {
		return errors.New("failed to retrieve XSRF token from cookies")
	}

	// Step 3: Generate New Password
	req, err = http.NewRequest(http.MethodPut, fmt.Sprintf("%s/accounts/self/password.http", c.RemoteURL), strings.NewReader(`{"generate":true}`))
	if err != nil {
		return fmt.Errorf("failed to create password generation request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-gerrit-auth", xsrfToken)

	resp, err = client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send password generation request: %w", err)
	}

	passwordResp, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("failed to read password generation response: %w", err)
	}

	const jsonPrefix = ")]}'\n"
	if !bytes.HasPrefix(passwordResp, []byte(jsonPrefix)) {
		return errors.New("unexpected response format from Gerrit")
	}

	trimmedPassword := strings.TrimSpace(string(passwordResp[len(jsonPrefix):]))
	c.HTTPPassword = strings.Trim(trimmedPassword, `"`)

	if c.HTTPPassword == "" {
		return errors.New("failed to generate or retrieve HTTP password")
	}

	return nil
}

func (c *Client) makeRequest(method, endpoint string, data io.Reader, contentType string) ([]byte, error) {
	url := fmt.Sprintf("%s/a/%s", strings.TrimRight(c.RemoteURL, "/"), strings.TrimLeft(endpoint, "/"))

	req, err := http.NewRequest(method, url, data)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", contentType)
	req.SetBasicAuth(c.RemoteUser, c.HTTPPassword)

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 10 * time.Second,
	}
	log.Println(url)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	responseData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errorSnippet := string(responseData)
		if len(errorSnippet) > 1000 {
			errorSnippet = errorSnippet[:1000] + "..."
		}
		return nil, fmt.Errorf("server returned unexpected status %s: %s", resp.Status, errorSnippet)
	}

	const jsonPrefix = ")]}'\n"
	if !bytes.HasPrefix(responseData, []byte(jsonPrefix)) {
		return nil, errors.New("unexpected response format from Gerrit")
	}
	return responseData[len(jsonPrefix):], nil
}

func (c *Client) MakeJSONRequest(method, endpoint string, data interface{}) ([]byte, error) {
	var reader io.Reader
	if data != nil {
		payload, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	return c.makeRequest(method, endpoint, reader, "application/json")
}

func (c *Client) MakePlainTextRequest(method, endpoint, data string) ([]byte, error) {
	return c.makeRequest(method, endpoint, bytes.NewBufferString(data), "text/plain")
}

func (c *Client) AddSSHKeyToAccount(accountID string, sshKey string) error {
	endpoint := fmt.Sprintf("accounts/%s/sshkeys", url.QueryEscape(accountID))
	_, err := c.MakePlainTextRequest(http.MethodPost, endpoint, sshKey)
	return err
}

func (c *Client) AddMemberToGroup(groupID, accountID string) error {
	endpoint := fmt.Sprintf("groups/%s/members/%s", url.QueryEscape(groupID), url.QueryEscape(accountID))
	_, err := c.MakePlainTextRequest(http.MethodPut, endpoint, "")
	return err
}

func (c *Client) ListProjects() ([]*Project, error) {
	responseData, err := c.MakePlainTextRequest(http.MethodGet, "projects/", "")
	if err != nil {
		return nil, err
	}

	var projectsMap map[string]*Project
	err = json.Unmarshal(responseData, &projectsMap)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	var projects []*Project
	for name, project := range projectsMap {
		if name == "All-Projects" || name == "All-Users" {
			continue
		}
		project.Name = name
		projects = append(projects, project)
	}

	return projects, nil
}

func (c *Client) GetGroup(groupID string) (*Group, error) {
	endpoint := fmt.Sprintf("groups/%s", url.QueryEscape(groupID))
	responseData, err := c.MakePlainTextRequest(http.MethodGet, endpoint, "")
	if err != nil {
		return nil, err
	}
	var group Group
	if err := json.Unmarshal(responseData, &group); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return &group, nil
}
