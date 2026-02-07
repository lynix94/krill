#!/usr/bin/env python3
"""
PyKrill Daemon - Persistent Python process for executing Krill functions
Avoids the overhead of starting a new Python interpreter for each function call
"""

import sys
import os
import socket
import msgpack
import signal
import json
import traceback

SOCKET_PATH = "/tmp/pykrill.sock"
PID_FILE = "/tmp/pykrill.pid"

class PyKrillDaemon:
    def __init__(self):
        self.running = True
        self.socket = None
        
    def cleanup(self):
        """Clean up resources on shutdown"""
        if self.socket:
            self.socket.close()
        if os.path.exists(SOCKET_PATH):
            os.remove(SOCKET_PATH)
        if os.path.exists(PID_FILE):
            os.remove(PID_FILE)
    
    def signal_handler(self, signum, frame):
        """Handle shutdown signals"""
        print(f"Received signal {signum}, shutting down...", file=sys.stderr)
        self.running = False
    
    def execute_function(self, request):
        """Execute a Python function with the given input data
        
        Args:
            request: Dict with 'input_file', 'output_file', 'code', 'function_name', 'module'
        
        Returns:
            Dict with 'success' and optional 'error' keys
        """
        try:
            input_file = request.get('input_file')
            output_file = request.get('output_file')
            code = request.get('code', '')
            function_name = request.get('function_name', '')
            module_name = request.get('module', '')
            
            # Read input data from shared memory file
            with open(input_file, 'rb') as f:
                input_data = msgpack.unpackb(f.read(), raw=False)
            
            # Execute the code or load module
            if code:
                # Execute user-provided code
                # The code should set a 'result' variable
                local_vars = {'input_data': input_data}
                exec(code, {}, local_vars)
                result = local_vars.get('result', input_data)
            elif module_name:
                # Dynamically import module and execute function
                import importlib
                try:
                    # Import the module
                    module = importlib.import_module(module_name)
                    
                    # Get the function from the module
                    if not hasattr(module, function_name):
                        return {
                            'success': False,
                            'error': f"Function '{function_name}' not found in module '{module_name}'"
                        }
                    
                    func = getattr(module, function_name)
                    
                    # Call the function with input_data
                    result = func(input_data)
                    
                except ImportError as e:
                    return {
                        'success': False,
                        'error': f"Failed to import module '{module_name}': {str(e)}"
                    }
            else:
                # No code and no module - just pass through
                result = input_data
            
            # Write result to shared memory file
            with open(output_file, 'wb') as f:
                f.write(msgpack.packb(result))
            
            return {'success': True}
            
        except Exception as e:
            error_msg = f"{str(e)}\n{traceback.format_exc()}"
            return {'success': False, 'error': error_msg}
    
    def start(self):
        """Start the daemon server"""
        # Write PID file
        with open(PID_FILE, 'w') as f:
            f.write(str(os.getpid()))
        
        # Set up signal handlers
        signal.signal(signal.SIGTERM, self.signal_handler)
        signal.signal(signal.SIGINT, self.signal_handler)
        
        # Remove existing socket if it exists
        if os.path.exists(SOCKET_PATH):
            os.remove(SOCKET_PATH)
        
        # Create Unix domain socket
        self.socket = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.socket.bind(SOCKET_PATH)
        self.socket.listen(5)
        
        # Make socket accessible
        os.chmod(SOCKET_PATH, 0o666)
        
        print(f"PyKrill daemon started, listening on {SOCKET_PATH}", file=sys.stderr)
        print(f"PID: {os.getpid()}", file=sys.stderr)
        
        try:
            while self.running:
                # Set timeout so we can check self.running periodically
                self.socket.settimeout(1.0)
                
                try:
                    conn, addr = self.socket.accept()
                except socket.timeout:
                    continue
                
                try:
                    # Read request (JSON with metadata)
                    data = b''
                    while True:
                        chunk = conn.recv(4096)
                        if not chunk:
                            break
                        data += chunk
                        # Check for end of JSON (simple heuristic)
                        try:
                            json.loads(data.decode('utf-8'))
                            break
                        except:
                            continue
                    
                    if data:
                        request = json.loads(data.decode('utf-8'))
                        response = self.execute_function(request)
                        
                        # Send response
                        conn.sendall(json.dumps(response).encode('utf-8'))
                
                finally:
                    conn.close()
        
        finally:
            self.cleanup()

if __name__ == '__main__':
    daemon = PyKrillDaemon()
    daemon.start()
