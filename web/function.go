package web

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/vmihailenco/msgpack/v5"
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

// processPythonFunction handles external Python functions using shared memory IPC
func processPythonFunction(currentResult QueryData, functionName string, stage KrillQLQuery) (QueryData, error) {
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

	var cmd *exec.Cmd
	
	// If code is provided, execute it directly
	if stage.Code != "" {
		// Execute the provided Python code
		// The code should read from shared memory file and write to another file
		pythonScript := fmt.Sprintf(`
import sys
import msgpack

# Read input data from shared memory file (IPC via /dev/shm)
# msgpack is faster than JSON for Python structs
with open(sys.argv[1], 'rb') as f:
    input_data = msgpack.unpackb(f.read(), raw=False)

# Execute user-provided code
%s

# The user code should set a 'result' variable
# Write result to shared memory file (IPC via /dev/shm)
with open(sys.argv[2], 'wb') as f:
    f.write(msgpack.packb(result))
`, stage.Code)
		
		cmd = exec.Command("python3", "-c", pythonScript, inputFile, outputFile)
	} else {
		// If no code provided, try to import and execute a named function
		pythonScript := fmt.Sprintf(`
import sys
import msgpack

# Read input data from shared memory file (IPC via /dev/shm)
# msgpack is faster than JSON for Python structs
with open(sys.argv[1], 'rb') as f:
    input_data = msgpack.unpackb(f.read(), raw=False)

# Import and execute the function
# User should implement their Python function module
# For example: from my_functions import %s
# result = %s(input_data)

# For now, just return the input data unchanged
result = input_data

# Write result to shared memory file (IPC via /dev/shm)
with open(sys.argv[2], 'wb') as f:
    f.write(msgpack.packb(result))
`, functionName, functionName)
		
		cmd = exec.Command("python3", "-c", pythonScript, inputFile, outputFile)
	}

	// Capture stderr for error reporting
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return QueryData{}, fmt.Errorf("python function execution failed: %v, stderr: %s", err, stderr.String())
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
