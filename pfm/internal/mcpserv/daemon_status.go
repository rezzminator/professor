package mcpserv

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/obs"
)

// DaemonStatus is the stable local health document consumed by doctor and the
// single-instance probe.
type DaemonStatus struct {
	PFMVersion        string              `json:"pfmVersion"`
	ProtocolVersion   string              `json:"protocolVersion"`
	Servers           map[string][]string `json:"servers"`
	PID               int                 `json:"pid"`
	StartTime         string              `json:"startTime"`
	Endpoint          string              `json:"endpoint"`
	HarvesterExternal string              `json:"harvesterExternal,omitempty"`
}

// ProbeDaemon reads a healthy loopback daemon's status document.
func ProbeDaemon(address string) (DaemonStatus, bool) {
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/status", http.NoBody)
	if err != nil {
		return DaemonStatus{}, false
	}
	client := obs.WrapClient(&http.Client{Timeout: 300 * time.Millisecond})
	response, err := client.Do(request)
	if err != nil {
		return DaemonStatus{}, false
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "pfm mcp serve: close daemon probe response: %v\n", err)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return DaemonStatus{}, false
	}
	var status DaemonStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil || status.PID < 1 {
		return DaemonStatus{}, false
	}
	return status, true
}

// DaemonReachability reports a configured daemon probe failure explicitly.
func DaemonReachability(runtime config.Runtime) (DaemonStatus, error) {
	address := "127.0.0.1:" + strconv.Itoa(runtime.Config.MCP.HTTP.Port)
	status, ok := ProbeDaemon(address)
	if !ok {
		return DaemonStatus{}, fmt.Errorf("unreachable at http://%s/status", address)
	}
	return status, nil
}
