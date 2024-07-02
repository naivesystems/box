package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/DataDog/datadog-api-client-go/v2/api/datadog"
	"github.com/DataDog/datadog-api-client-go/v2/api/datadogV2"
)

const (
	DD_CLIENT_TOKEN = "pub210ef0252cfd7b0a0e93346902c0da48"
	DD_SITE         = "datadoghq.com"
	DD_SOURCE       = "v2cli"
	DD_SERVICE      = "nsbox"
)

type LogsConfig struct {
	hostname string
	service  string
	source   string
	tags     string
}

type DatadogAgent struct {
	*datadogV2.LogsApi

	config LogsConfig
	ctx    context.Context
}

type jsonObject = map[string]any

var initData = jsonObject{}

func NewDatadogAgent() (*DatadogAgent, error) {
	ctx := context.WithValue(
		context.Background(),
		datadog.ContextAPIKeys,
		map[string]datadog.APIKey{
			"apiKeyAuth": {
				Key: DD_CLIENT_TOKEN,
			},
		},
	)
	ctx = context.WithValue(
		ctx,
		datadog.ContextServerVariables,
		map[string]string{"site": DD_SITE},
	)
	configuration := datadog.NewConfiguration()
	apiClient := datadog.NewAPIClient(configuration)
	api := datadogV2.NewLogsApi(apiClient)
	hostname, err := os.Hostname()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when getting host name: %v\n", err)
		return nil, err
	}
	config := LogsConfig{
		hostname: hostname,
		source:   DD_SOURCE,
		service:  DD_SERVICE,
		tags:     *releaseTag,
	}
	agent := &DatadogAgent{
		LogsApi: api,
		config:  config,
		ctx:     ctx,
	}
	return agent, nil
}

func (d *DatadogAgent) Send(msg string) {
	body := []datadogV2.HTTPLogItem{
		{
			Ddsource: datadog.PtrString(d.config.source),
			Ddtags:   datadog.PtrString(d.config.tags),
			Hostname: datadog.PtrString(d.config.hostname),
			Message:  msg,
			Service:  datadog.PtrString(d.config.service),
		},
	}
	resp, r, err := d.SubmitLog(d.ctx, body, *datadogV2.NewSubmitLogOptionalParameters().WithContentEncoding(datadogV2.CONTENTENCODING_DEFLATE))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `LogsApi.SubmitLog`: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}

	responseContent, _ := json.MarshalIndent(resp, "", "  ")
	// 202 empty JSON: Request accepted for processing.
	if string(responseContent) != "{}" {
		fmt.Fprintf(os.Stdout, "Response from `LogsApi.SubmitLog`:\n%s\n", responseContent)
	}
}

func charArrayToString(ca []int8) string {
	var bs []byte
	for _, c := range ca {
		if c == 0 {
			break
		}
		bs = append(bs, byte(c))
	}
	return string(bs)
}

func initUtsname() {
	var utsname syscall.Utsname

	err := syscall.Uname(&utsname)
	if err != nil {
		return
	}

	initData["utsname"] = jsonObject{
		"sysname":    charArrayToString(utsname.Sysname[:]),
		"nodename":   charArrayToString(utsname.Nodename[:]),
		"release":    charArrayToString(utsname.Release[:]),
		"version":    charArrayToString(utsname.Version[:]),
		"machine":    charArrayToString(utsname.Machine[:]),
		"domainname": charArrayToString(utsname.Domainname[:]),
	}
}

// extractCPUModelName reads /proc/cpuinfo and extracts the CPU model name.
func extractCPUModelName() (string, error) {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1]), nil
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", nil // No model name found
}

func initCPUInfo() {
	cpuModelName, err := extractCPUModelName()
	if err != nil {
		return
	}

	// Ensure the nested maps exist
	if _, ok := initData["proc"]; !ok {
		initData["proc"] = make(jsonObject)
	}
	procInfo, _ := initData["proc"].(jsonObject)

	if _, ok := procInfo["cpuinfo"]; !ok {
		procInfo["cpuinfo"] = make(jsonObject)
	}
	cpuInfo, _ := procInfo["cpuinfo"].(jsonObject)

	// Add model name to the map
	cpuInfo["model name"] = cpuModelName
}

func initDMIInfo() {
	baseDir := "/sys/class/dmi/id/"

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return
	}

	// Ensure the nested maps exist
	if _, ok := initData["sys"]; !ok {
		initData["sys"] = make(jsonObject)
	}
	sysInfo, _ := initData["sys"].(jsonObject)

	if _, ok := sysInfo["class"]; !ok {
		sysInfo["class"] = make(jsonObject)
	}
	classInfo, _ := sysInfo["class"].(jsonObject)

	if _, ok := classInfo["dmi"]; !ok {
		classInfo["dmi"] = make(jsonObject)
	}
	dmiInfo, _ := classInfo["dmi"].(jsonObject)

	if _, ok := dmiInfo["id"]; !ok {
		dmiInfo["id"] = make(jsonObject)
	}
	idInfo, _ := dmiInfo["id"].(jsonObject)

	for _, entry := range entries {
		if entry.IsDir() {
			// Skip directories
			continue
		}

		filePath := filepath.Join(baseDir, entry.Name())

		content, err := os.ReadFile(filePath)
		if err != nil {
			// Skip files that cannot be read (e.g., due to permissions)
			continue
		}

		// Trim space and add to map
		idInfo[entry.Name()] = strings.TrimSpace(string(content))
	}
}

func initMemInfo() {
	var sysinfo syscall.Sysinfo_t

	err := syscall.Sysinfo(&sysinfo)
	if err != nil {
		return
	}

	initData["mem"] = jsonObject{
		"totalram": sysinfo.Totalram,
		"freeram":  sysinfo.Freeram,
	}
	fmt.Printf("Total RAM: %d\n", sysinfo.Totalram)
	fmt.Printf("Free RAM: %d\n", sysinfo.Freeram)
}


func getPublicIP() string {
	resp, err := http.Get("http://ifconfig.me/ip")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: retrieving public IP: %v\n", err)
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "Unexpected status code: %v\n", resp.StatusCode)
		return ""
	}
	ipBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: reading response body: %v\n", err)
		return ""
	}
	publicIP := string(ipBytes)
	return publicIP
}

func sendTelemetry() {
	hostname, err := os.Hostname()
	if err == nil {
		initData["hostname"] = hostname
	}
	logAgent, err := NewDatadogAgent()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating a Datadog agent: %v\n", err)
	}
	publicIP := getPublicIP()
	if runtime.GOOS == "linux" {
		initUtsname()
		initCPUInfo()
		initDMIInfo()
		initMemInfo()
	}
	if publicIP != "" {
		initData["network"] = jsonObject{"client": jsonObject{"ip": publicIP}}
	}
	messagesJson, err := json.Marshal(initData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: json.Marshal: %v\n", err)
		return
	}
	logAgent.Send(string(messagesJson))
}
