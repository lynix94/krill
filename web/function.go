package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/vmihailenco/msgpack/v5"
)

const (
	SocketPath = "/tmp/pykrill.sock"
	PidFile    = "/tmp/pykrill.pid"
	DaemonStartTimeout = 5 * time.Second
)

var (
	daemonMutex sync.Mutex
)

// ProcessPipelineFunction processes a pipeline function stage
// Takes the current result and function name, returns the processed result
func ProcessPipelineFunction(currentResult QueryData, functionName string, stage KrillQLQuery) (QueryData, error) {
	// Determine function type: if not specified or "internal", use internal functions
	// If "python", execute external Python function
	funcType := stage.Type
	if funcType == "" || funcType == "internal" {
		return processInternalFunction(currentResult, functionName, stage)
	} else if funcType == "python" {
		return processPythonFunction(currentResult, functionName, stage)
	}
	
	return QueryData{}, fmt.Errorf("unsupported function type '%s'", funcType)
}

// generateRandomID generates a random hex string for unique filenames
func generateRandomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// processInternalFunction handles internal Go functions
func processInternalFunction(currentResult QueryData, functionName string, stage KrillQLQuery) (QueryData, error) {
	switch functionName {
	case "pass":
		return processPassFunction(currentResult, stage)
	default:
		return QueryData{}, fmt.Errorf("unknown internal function '%s'", functionName)
	}
}

// processPythonFunction handles external Python functions using daemon process
func processPythonFunction(currentResult QueryData, functionName string, stage KrillQLQuery) (QueryData, error) {
	// Ensure daemon is running
	if err := ensureDaemonRunning(); err != nil {
		return QueryData{}, fmt.Errorf("failed to start pykrill daemon: %v", err)
	}

	// Prepare input data as msgpack (binary format - faster than JSON)
	inputData, err := msgpack.Marshal(currentResult)
	if err != nil {
		return QueryData{}, fmt.Errorf("failed to marshal input data: %v", err)
	}

	// Create unique filenames in /dev/shm (shared memory - faster than disk)
	randomID := generateRandomID()
	inputFile := filepath.Join("/dev/shm", fmt.Sprintf("krill_input_%s.msgpack", randomID))
	outputFile := filepath.Join("/dev/shm", fmt.Sprintf("krill_output_%s.msgpack", randomID))
	
	// Ensure cleanup
	defer func() {
		os.Remove(inputFile)
		os.Remove(outputFile)
	}()

	// Write input data to shared memory file
	if err := os.WriteFile(inputFile, inputData, 0600); err != nil {
		return QueryData{}, fmt.Errorf("failed to write input file: %v", err)
	}

	// Send request to daemon via Unix socket
	request := map[string]string{
		"input_file":    inputFile,
		"output_file":   outputFile,
		"code":          stage.Code,
		"function_name": functionName,
		"module":        stage.Module,
	}
	
	response, err := sendToDaemon(request)
	if err != nil {
		return QueryData{}, fmt.Errorf("daemon communication failed: %v", err)
	}
	
	if !response["success"].(bool) {
		return QueryData{}, fmt.Errorf("python function execution failed: %v", response["error"])
	}

	// Read output from shared memory file
	outputData, err := os.ReadFile(outputFile)
	if err != nil {
		return QueryData{}, fmt.Errorf("failed to read output file: %v", err)
	}

	// Parse output msgpack from IPC
	var result QueryData
	err = msgpack.Unmarshal(outputData, &result)
	if err != nil {
		return QueryData{}, fmt.Errorf("failed to parse python function output: %v", err)
	}

	return result, nil
}

// processPassFunction returns the input data unchanged
func processPassFunction(data QueryData, stage KrillQLQuery) (QueryData, error) {
	// Pass function: return input as-is
	return data, nil
}

// ensureDaemonRunning checks if the daemon is running and starts it if not
func ensureDaemonRunning() error {
	daemonMutex.Lock()
	defer daemonMutex.Unlock()
	
	// Check if daemon is already running
	if isDaemonRunning() {
		return nil
	}
	
	// Start the daemon
	return startDaemon()
}

// isDaemonRunning checks if the pykrill daemon is running
func isDaemonRunning() bool {
	// Check if socket exists
	if _, err := os.Stat(SocketPath); os.IsNotExist(err) {
		return false
	}
	
	// Try to connect to socket
	conn, err := net.DialTimeout("unix", SocketPath, time.Second)
	if err != nil {
		// Socket exists but can't connect - clean up stale socket
		os.Remove(SocketPath)
		return false
	}
	conn.Close()
	
	return true
}

// startDaemon starts the pykrill daemon process
func startDaemon() error {
	// Find pykrill.py in the current directory or parent directory
	krillPath := ""
	possiblePaths := []string{
		"./pykrill/pykrill.py",
		"../pykrill/pykrill.py",
		"/home/lynix/git/krill/pykrill/pykrill.py",
	}
	
	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			krillPath = path
			break
		}
	}
	
	if krillPath == "" {
		return fmt.Errorf("pykrill/pykrill.py not found")
	}
	
	// Start daemon process in background
	cmd := exec.Command("python3", krillPath)
	cmd.Stderr = os.Stderr // Show daemon errors in server log
	
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start daemon: %v", err)
	}
	
	// Wait for daemon to be ready (with timeout)
	deadline := time.Now().Add(DaemonStartTimeout)
	for time.Now().Before(deadline) {
		if isDaemonRunning() {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	
	return fmt.Errorf("daemon failed to start within timeout")
}

// sendToDaemon sends a request to the pykrill daemon via Unix socket
func sendToDaemon(request map[string]string) (map[string]interface{}, error) {
	// Connect to daemon
	conn, err := net.DialTimeout("unix", SocketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon: %v", err)
	}
	defer conn.Close()

	// Set an overall deadline so we never block indefinitely.
	// The Python daemon has its own function timeout (DEFAULT_TIMEOUT=300s),
	// so we give a bit of headroom: 310 seconds.
	if err := conn.SetDeadline(time.Now().Add(310 * time.Second)); err != nil {
		return nil, fmt.Errorf("failed to set deadline: %v", err)
	}
	
	// Send request as JSON
	requestData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}
	
	if _, err := conn.Write(requestData); err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	
	// Close write side to signal end of request
	if tcpConn, ok := conn.(*net.UnixConn); ok {
		tcpConn.CloseWrite()
	}
	
	// Read response
	var responseData bytes.Buffer
	if _, err := io.Copy(&responseData, conn); err != nil {
		return nil, fmt.Errorf("failed to read response: %v", err)
	}
	
	var response map[string]interface{}
	if err := json.Unmarshal(responseData.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %v", err)
	}
	
	return response, nil
}
