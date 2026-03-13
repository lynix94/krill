#!/usr/bin/env python3
"""
PyKrill Daemon - Persistent Python process pool for executing Krill functions
Uses concurrent.futures ProcessPoolExecutor for concurrent execution and fault tolerance
"""

import sys
import os
import socket
import msgpack
import signal
import json
import traceback
from concurrent.futures import ProcessPoolExecutor, TimeoutError as FutureTimeoutError
import time
import threading
from multiprocessing import cpu_count

SOCKET_PATH = "/tmp/pykrill.sock"
PID_FILE = "/tmp/pykrill.pid"
DEFAULT_TIMEOUT = 300  # 5 minutes
POOL_SIZE = max(4, cpu_count())  # At least 4 workers

def worker_execute_function(request):
    """Worker function to execute Python code in separate process
    
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

class PyKrillDaemon:
    def __init__(self, pool_size=POOL_SIZE, timeout=DEFAULT_TIMEOUT):
        self.running = True
        self.socket = None
        self.executor = None
        self.pool_size = pool_size
        self.timeout = timeout
        self.active_tasks = {}  # Track active futures
        self.task_lock = threading.Lock()
        
    def cleanup(self):
        """Clean up resources on shutdown"""
        print("Cleaning up daemon resources...", file=sys.stderr)
        
        # Shutdown executor
        if self.executor:
            print(f"Shutting down worker pool ({self.pool_size} workers)...", file=sys.stderr)
            self.executor.shutdown(wait=True, cancel_futures=True)
            self.executor = None
        
        # Close socket
        if self.socket:
            self.socket.close()
        
        # Remove socket and PID files
        if os.path.exists(SOCKET_PATH):
            os.remove(SOCKET_PATH)
        if os.path.exists(PID_FILE):
            os.remove(PID_FILE)
        
        print("Cleanup completed", file=sys.stderr)
    
    def signal_handler(self, signum, frame):
        """Handle shutdown signals"""
        print(f"Received signal {signum}, shutting down...", file=sys.stderr)
        self.running = False
    
    def execute_function(self, request):
        """Execute a Python function using the worker pool
        
        Args:
            request: Dict with 'input_file', 'output_file', 'code', 'function_name', 'module'
        
        Returns:
            Dict with 'success' and optional 'error' keys
        """
        future = None
        try:
            # Submit task to executor with timeout
            future = self.executor.submit(worker_execute_function, request)
            
            # Track active future
            task_id = id(future)
            with self.task_lock:
                self.active_tasks[task_id] = future
            
            try:
                # Wait for result with timeout
                result = future.result(timeout=self.timeout)
                return result
                
            except FutureTimeoutError:
                error_msg = f"Function execution timed out after {self.timeout} seconds"
                print(f"[TIMEOUT] {error_msg}", file=sys.stderr)
                future.cancel()  # Try to cancel the future
                return {'success': False, 'error': error_msg}
                
            except Exception as e:
                error_msg = f"Worker process error: {str(e)}\n{traceback.format_exc()}"
                print(f"[ERROR] {error_msg}", file=sys.stderr)
                return {'success': False, 'error': error_msg}
            
        except Exception as e:
            error_msg = f"Failed to submit task: {str(e)}\n{traceback.format_exc()}"
            print(f"[ERROR] {error_msg}", file=sys.stderr)
            return {'success': False, 'error': error_msg}
            
        finally:
            # Clean up task tracking
            if future is not None:
                task_id = id(future)
                with self.task_lock:
                    self.active_tasks.pop(task_id, None)
    
    def start(self):
        """Start the daemon server with process pool"""
        # Write PID file
        with open(PID_FILE, 'w') as f:
            f.write(str(os.getpid()))
        
        # Set up signal handlers
        signal.signal(signal.SIGTERM, self.signal_handler)
        signal.signal(signal.SIGINT, self.signal_handler)
        
        # Create process pool executor
        print(f"Creating worker pool with {self.pool_size} processes...", file=sys.stderr)
        self.executor = ProcessPoolExecutor(
            max_workers=self.pool_size,
            mp_context=None  # Use default context
        )
        
        # Remove existing socket if it exists
        if os.path.exists(SOCKET_PATH):
            os.remove(SOCKET_PATH)
        
        # Create Unix domain socket
        self.socket = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.socket.bind(SOCKET_PATH)
        self.socket.listen(10)  # Increased backlog
        
        # Make socket accessible
        os.chmod(SOCKET_PATH, 0o666)
        
        print(f"PyKrill daemon started, listening on {SOCKET_PATH}", file=sys.stderr)
        print(f"PID: {os.getpid()}", file=sys.stderr)
        print(f"Worker pool size: {self.pool_size}", file=sys.stderr)
        print(f"Timeout: {self.timeout}s", file=sys.stderr)
        
        try:
            while self.running:
                # Set timeout so we can check self.running periodically
                self.socket.settimeout(1.0)
                
                try:
                    conn, addr = self.socket.accept()
                except socket.timeout:
                    continue
                except Exception as e:
                    if self.running:
                        print(f"[ERROR] Accept failed: {e}", file=sys.stderr)
                    continue
                
                # Handle connection in separate thread to avoid blocking
                handler_thread = threading.Thread(
                    target=self._handle_connection,
                    args=(conn,),
                    daemon=True
                )
                handler_thread.start()
        
        finally:
            self.cleanup()
    
    def _handle_connection(self, conn):
        """Handle a single client connection"""
        try:
            # Read request (JSON with metadata)
            data = b''
            conn.settimeout(30)  # 30 second timeout for reading request
            
            while True:
                try:
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
                except socket.timeout:
                    print("[WARNING] Client request timeout", file=sys.stderr)
                    break
            
            # Remove timeout before executing the function so a long-running
            # function doesn't cause a socket timeout when we send the response.
            conn.settimeout(None)

            if data:
                try:
                    request = json.loads(data.decode('utf-8'))
                    response = self.execute_function(request)

                    # Send response
                    conn.sendall(json.dumps(response).encode('utf-8'))
                except json.JSONDecodeError as e:
                    error_response = {
                        'success': False,
                        'error': f'Invalid JSON request: {str(e)}'
                    }
                    conn.sendall(json.dumps(error_response).encode('utf-8'))
                except Exception as e:
                    error_response = {
                        'success': False,
                        'error': f'Request handling error: {str(e)}'
                    }
                    try:
                        conn.sendall(json.dumps(error_response).encode('utf-8'))
                    except:
                        pass
        
        except Exception as e:
            print(f"[ERROR] Connection handler error: {e}", file=sys.stderr)
        
        finally:
            try:
                conn.close()
            except:
                pass

if __name__ == '__main__':
    # Parse command line arguments
    import argparse
    
    parser = argparse.ArgumentParser(description='PyKrill Daemon - Python function execution pool')
    parser.add_argument('--pool-size', type=int, default=POOL_SIZE,
                        help=f'Number of worker processes (default: {POOL_SIZE})')
    parser.add_argument('--timeout', type=int, default=DEFAULT_TIMEOUT,
                        help=f'Function execution timeout in seconds (default: {DEFAULT_TIMEOUT})')
    
    args = parser.parse_args()
    
    daemon = PyKrillDaemon(pool_size=args.pool_size, timeout=args.timeout)
    daemon.start()
