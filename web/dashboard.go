package web

const dashboardHTML = `<!DOCTYPE html>
<html lang="ko">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Krill TSDB Dashboard</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.min.js"></script>
    <style>
        * {
            margin: 0;
            padding: 0;
            box-sizing: border-box;
        }
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, Cantarell, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            padding: 20px;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
        }
        .header {
            background: white;
            border-radius: 12px;
            padding: 30px;
            margin-bottom: 20px;
            box-shadow: 0 10px 30px rgba(0,0,0,0.2);
        }
        .header h1 {
            color: #667eea;
            font-size: 2.5em;
            margin-bottom: 10px;
        }
        .header p {
            color: #666;
            font-size: 1.1em;
        }
        .grid {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 20px;
            margin-bottom: 20px;
        }
        @media (max-width: 1024px) {
            .grid {
                grid-template-columns: 1fr;
            }
        }
        .panel {
            background: white;
            border-radius: 12px;
            padding: 25px;
            box-shadow: 0 10px 30px rgba(0,0,0,0.2);
        }
        .panel h2 {
            color: #333;
            margin-bottom: 20px;
            font-size: 1.5em;
            border-bottom: 3px solid #667eea;
            padding-bottom: 10px;
        }
        .form-group {
            margin-bottom: 20px;
        }
        .form-group label {
            display: block;
            margin-bottom: 8px;
            font-weight: 600;
            color: #555;
        }
        .form-group select,
        .form-group input,
        .form-group button {
            width: 100%;
            padding: 12px;
            border: 2px solid #e0e0e0;
            border-radius: 8px;
            font-size: 14px;
            transition: all 0.3s;
        }
        .form-group select:focus,
        .form-group input:focus {
            outline: none;
            border-color: #667eea;
            box-shadow: 0 0 0 3px rgba(102,126,234,0.1);
        }
        button {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            border: none;
            cursor: pointer;
            font-weight: 600;
            transition: transform 0.2s;
        }
        button:hover {
            transform: translateY(-2px);
            box-shadow: 0 5px 15px rgba(102,126,234,0.4);
        }
        button:active {
            transform: translateY(0);
        }
        .btn-execute {
            padding: 18px 40px !important;
            font-size: 18px !important;
            font-weight: 700 !important;
            margin-top: 20px;
        }
        .btn-secondary {
            background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%);
            margin-top: 10px;
        }
        .result-box {
            background: #f8f9fa;
            border-radius: 8px;
            padding: 15px;
            margin-top: 20px;
            max-height: 400px;
            overflow-y: auto;
            border: 2px solid #e0e0e0;
        }
        .result-box pre {
            margin: 0;
            font-family: 'Courier New', monospace;
            font-size: 13px;
            white-space: pre-wrap;
            word-wrap: break-wrap;
        }
        .chart-container {
            position: relative;
            min-height: 450px;
            height: 60vh;
            max-height: 700px;
            margin-top: 20px;
        }
        .metric-list {
            display: flex;
            flex-wrap: wrap;
            gap: 10px;
            margin-top: 15px;
        }
        .metric-badge {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 8px 16px;
            border-radius: 20px;
            font-size: 13px;
            font-weight: 600;
        }
        .stats {
            display: grid;
            grid-template-columns: repeat(3, 1fr);
            gap: 15px;
            margin-top: 20px;
        }
        .stat-card {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            padding: 20px;
            border-radius: 10px;
            text-align: center;
        }
        .stat-card .value {
            font-size: 2em;
            font-weight: bold;
            margin-bottom: 5px;
        }
        .stat-card .label {
            font-size: 0.9em;
            opacity: 0.9;
        }
        .time-range {
            display: grid;
            grid-template-columns: 1fr 1fr;
            gap: 10px;
        }
        .alert {
            padding: 15px;
            border-radius: 8px;
            margin-top: 15px;
            font-weight: 500;
        }
        .alert-success {
            background: #d4edda;
            color: #155724;
            border: 2px solid #c3e6cb;
        }
        .alert-error {
            background: #f8d7da;
            color: #721c24;
            border: 2px solid #f5c6cb;
        }
        .loading {
            display: inline-block;
            width: 20px;
            height: 20px;
            border: 3px solid rgba(255,255,255,.3);
            border-radius: 50%;
            border-top-color: white;
            animation: spin 1s ease-in-out infinite;
        }
        @keyframes spin {
            to { transform: rotate(360deg); }
        }
        .spinner {
            display: inline-block;
            width: 40px;
            height: 40px;
            border: 4px solid rgba(102, 126, 234, 0.3);
            border-radius: 50%;
            border-top-color: #667eea;
            animation: spin 1s ease-in-out infinite;
        }
        .tabs {
            display: flex;
            background: white;
            border-radius: 12px;
            padding: 5px;
            margin-bottom: 20px;
            box-shadow: 0 10px 30px rgba(0,0,0,0.2);
        }
        .tab-button {
            flex: 1;
            padding: 15px 30px;
            background: transparent;
            color: #666;
            border: none;
            border-radius: 8px;
            font-size: 1.1em;
            font-weight: 600;
            cursor: pointer;
            transition: all 0.3s;
        }
        .tab-button:hover {
            background: rgba(102, 126, 234, 0.1);
            color: #667eea;
            transform: none;
            box-shadow: none;
        }
        .tab-button.active {
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            color: white;
            box-shadow: 0 4px 12px rgba(102, 126, 234, 0.4);
        }
        .tab-content {
            display: none;
        }
        .tab-content.active {
            display: block;
        }
        .autocomplete-container {
            position: relative;
        }
        .autocomplete-list {
            position: absolute;
            top: 100%;
            left: 0;
            right: 0;
            background: white;
            border: 2px solid #667eea;
            border-top: none;
            border-radius: 0 0 8px 8px;
            max-height: 200px;
            overflow-y: auto;
            z-index: 1000;
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.15);
        }
        .autocomplete-item {
            padding: 10px 12px;
            cursor: pointer;
            transition: background 0.2s;
            font-size: 14px;
        }
        .autocomplete-item:hover {
            background: rgba(102, 126, 234, 0.1);
        }
        .autocomplete-item.selected {
            background: rgba(102, 126, 234, 0.2);
        }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>📊 Krill TSDB Dashboard</h1>
            <p>High-Performance Time Series Database with Gorilla Compression</p>
        </div>

        <div class="tabs">
            <button class="tab-button active" onclick="switchTab('read')">🔍 Read / Query</button>
            <button class="tab-button" onclick="switchTab('write')">✏️ Write Data</button>
            <button class="tab-button" onclick="switchTab('metrics')">📋 Metrics List</button>
        </div>

        <!-- Read Tab -->
        <div id="readTab" class="tab-content active">
            <div class="panel">
                <h2>🔍 Query Metrics</h2>
                <div class="form-group autocomplete-container">
                    <label for="metricInput">Metric Name:</label>
                    <input type="text" id="metricInput" placeholder="Type metric name..." autocomplete="off">
                    <div id="autocompleteList" class="autocomplete-list" style="display: none;"></div>
                </div>
                <div class="form-group">
                    <label for="queryType">Query Type:</label>
                    <select id="queryType" onchange="toggleTimeRange()">
                        <option value="range" selected>Range Query (Time Series)</option>
                        <option value="instant">Instant Query (Latest Value)</option>
                    </select>
                </div>
                <div id="timeRangeGroup" class="form-group" style="display: block;">
                    <div class="form-group">
                        <label for="quickTimeRange">Quick Time Range:</label>
                        <div style="display: flex; gap: 8px; align-items: center;">
                            <select id="quickTimeRange" onchange="applyQuickRange()">
                            <option value="">-- Select a time range --</option>
                            <option value="300">Last 5 minutes</option>
                            <option value="900">Last 15 minutes</option>
                            <option value="1800">Last 30 minutes</option>
                            <option value="3600" selected>Last 1 hour</option>
                            <option value="10800">Last 3 hours</option>
                            <option value="21600">Last 6 hours</option>
                            <option value="43200">Last 12 hours</option>
                            <option value="86400">Last 24 hours</option>
                            <option value="172800">Last 2 days</option>
                            <option value="604800">Last 7 days</option>
                            <option value="2592000">Last 30 days</option>
                            <option value="7776000">Last 90 days</option>
                            </select>
                            <button class="btn-execute" type="button" onclick="refreshQuickRange()" style="padding: 4px 8px; font-size: 0.8em; width: auto; min-width: 0; display: inline-flex; align-items: center; justify-content: center;">Refresh</button>
                        </div>
                    </div>
                    <label>Custom Range:</label>
                    <div class="time-range">
                        <div>
                            <label for="startTime">From:</label>
                            <input type="datetime-local" id="startTime" step="1">
                        </div>
                        <div>
                            <label for="endTime">To:</label>
                            <input type="datetime-local" id="endTime" step="1">
                        </div>
                    </div>
                    <div style="margin-top: 10px;">
                        <label style="display: inline-flex; align-items: center; margin-right: 10px;">
                            <input type="checkbox" id="autoStep" checked onchange="toggleStepInput()" style="margin-right: 5px;">
                            Auto Step
                        </label>
                        <label for="stepInput" style="display: inline-flex; align-items: center;">
                            Step (seconds):
                            <input type="number" id="stepInput" min="1" value="15" disabled style="margin-left: 5px; width: 80px; padding: 4px;">
                        </label>
                        <small style="margin-left: 5px; color: #888;">(1 = raw data)</small>
                    </div>
                </div>
                <button class="btn-execute" onclick="executeQuery()">Execute Query</button>
                
                <div id="queryResult" class="result-box" style="display: none;">
                    <pre id="queryOutput"></pre>
                </div>
                
                <div id="chartContainer" class="chart-container" style="display: none;">
                    <canvas id="metricsChart"></canvas>
                </div>

                <div class="stats" id="statsContainer" style="margin-top: 30px;">
                    <div class="stat-card">
                        <div class="value" id="metricCount">-</div>
                        <div class="label">Total Metrics</div>
                    </div>
                    <div class="stat-card">
                        <div class="value" id="queryCount">0</div>
                        <div class="label">Queries</div>
                    </div>
                    <div class="stat-card">
                        <div class="value" id="writeCount">0</div>
                        <div class="label">Writes</div>
                    </div>
                </div>
            </div>
        </div>

        <!-- Write Tab -->
        <div id="writeTab" class="tab-content">
            <div class="panel">
                <h2>✏️ Write Data</h2>
                <div class="form-group autocomplete-container">
                    <label for="writeMetric">Metric Name:</label>
                    <input type="text" id="writeMetric" placeholder="e.g., cpu.usage" autocomplete="off">
                    <div id="writeAutocompleteList" class="autocomplete-list" style="display: none;"></div>
                </div>
                <div class="form-group">
                    <label for="writeValue">Value:</label>
                    <input type="number" id="writeValue" step="0.01" placeholder="e.g., 42.5">
                </div>
                <div class="form-group">
                    <label for="writeTags">Labels (JSON, optional):</label>
                    <input type="text" id="writeTags" placeholder='e.g., {"host":"server1","env":"prod"}'>
                </div>
                <div class="form-group">
                    <label for="writeTime">Timestamp (optional):</label>
                    <input type="number" id="writeTime" placeholder="Unix timestamp (leave empty for now)">
                </div>
                <button class="btn-execute" onclick="writeData()">Write Data Point</button>
                <div id="writeResult"></div>

                <div class="stats" style="margin-top: 30px;">
                    <div class="stat-card">
                        <div class="value" id="metricCount2">-</div>
                        <div class="label">Total Metrics</div>
                    </div>
                    <div class="stat-card">
                        <div class="value" id="queryCount2">0</div>
                        <div class="label">Queries</div>
                    </div>
                    <div class="stat-card">
                        <div class="value" id="writeCount2">0</div>
                        <div class="label">Writes</div>
                    </div>
                </div>
            </div>
        </div>

        <!-- Metrics Tab -->
        <div id="metricsTab" class="tab-content">
            <div class="panel">
                <h2>📋 Metrics List</h2>
                <div class="form-group">
                    <label for="metricsFilter">Filter (regex):</label>
                    <input type="text" id="metricsFilter" placeholder="e.g., cpu|memory, ^http, usage$" onkeyup="filterMetrics()">
                </div>
                <button class="btn-execute" onclick="loadAllMetrics()">Refresh Metrics</button>
                
                <div id="metricsResult" class="result-box" style="margin-top: 20px;">
                    <div style="text-align: center; padding: 40px; color: #999;">
                        Click "Refresh Metrics" to load the metrics list
                    </div>
                </div>

                <div class="stats" style="margin-top: 30px;">
                    <div class="stat-card">
                        <div class="value" id="metricCount3">-</div>
                        <div class="label">Total Metrics</div>
                    </div>
                    <div class="stat-card">
                        <div class="value" id="filteredMetricCount">-</div>
                        <div class="label">Filtered Metrics</div>
                    </div>
                    <div class="stat-card">
                        <div class="value" id="lastUpdate">-</div>
                        <div class="label">Last Update</div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <script>
        let chart = null;
        let queryCounter = 0;
        let writeCounter = 0;
        let allMetrics = [];
        let selectedIndex = -1;

        // Load metrics on page load
        window.addEventListener('DOMContentLoaded', function() {
            loadMetrics();
            setupAutocomplete();
            toggleTimeRange();
            applyQuickRange();
        });

        function switchTab(tab) {
            // Update tab buttons
            document.querySelectorAll('.tab-button').forEach(btn => {
                btn.classList.remove('active');
            });
            event.target.classList.add('active');

            // Update tab content
            document.querySelectorAll('.tab-content').forEach(content => {
                content.classList.remove('active');
            });
            document.getElementById(tab + 'Tab').classList.add('active');
        }

        function setupAutocomplete() {
            const metricInput = document.getElementById('metricInput');
            const autocompleteList = document.getElementById('autocompleteList');
            const writeMetricInput = document.getElementById('writeMetric');
            const writeAutocompleteList = document.getElementById('writeAutocompleteList');

            // Read tab autocomplete
            metricInput.addEventListener('input', function() {
                showAutocomplete(this.value, autocompleteList, metricInput);
            });

            metricInput.addEventListener('keydown', function(e) {
                handleAutocompleteKeydown(e, autocompleteList, metricInput);
            });

            metricInput.addEventListener('blur', function() {
                setTimeout(() => autocompleteList.style.display = 'none', 200);
            });

            // Write tab autocomplete
            writeMetricInput.addEventListener('input', function() {
                showAutocomplete(this.value, writeAutocompleteList, writeMetricInput);
            });

            writeMetricInput.addEventListener('keydown', function(e) {
                handleAutocompleteKeydown(e, writeAutocompleteList, writeMetricInput);
            });

            writeMetricInput.addEventListener('blur', function() {
                setTimeout(() => writeAutocompleteList.style.display = 'none', 200);
            });
        }

        function showAutocomplete(value, listElement, inputElement) {
            selectedIndex = -1;
            
            if (!value) {
                listElement.style.display = 'none';
                return;
            }

            const filtered = allMetrics.filter(metric => 
                metric.toLowerCase().includes(value.toLowerCase())
            );

            if (filtered.length === 0) {
                listElement.style.display = 'none';
                return;
            }

            listElement.innerHTML = '';
            filtered.slice(0, 10).forEach((metric, index) => {
                const item = document.createElement('div');
                item.className = 'autocomplete-item';
                item.textContent = metric;
                item.addEventListener('click', function() {
                    inputElement.value = metric;
                    listElement.style.display = 'none';
                });
                listElement.appendChild(item);
            });

            listElement.style.display = 'block';
        }

        function handleAutocompleteKeydown(e, listElement, inputElement) {
            const items = listElement.querySelectorAll('.autocomplete-item');
            
            if (e.key === 'ArrowDown') {
                e.preventDefault();
                selectedIndex = Math.min(selectedIndex + 1, items.length - 1);
                updateSelectedItem(items);
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                selectedIndex = Math.max(selectedIndex - 1, -1);
                updateSelectedItem(items);
            } else if (e.key === 'Enter') {
                e.preventDefault();
                if (selectedIndex >= 0 && items[selectedIndex]) {
                    inputElement.value = items[selectedIndex].textContent;
                    listElement.style.display = 'none';
                    selectedIndex = -1;
                }
            } else if (e.key === 'Escape') {
                listElement.style.display = 'none';
                selectedIndex = -1;
            }
        }

        function updateSelectedItem(items) {
            items.forEach((item, index) => {
                if (index === selectedIndex) {
                    item.classList.add('selected');
                    item.scrollIntoView({ block: 'nearest' });
                } else {
                    item.classList.remove('selected');
                }
            });
        }

        function toggleTimeRange() {
            const queryType = document.getElementById('queryType').value;
            const timeRangeGroup = document.getElementById('timeRangeGroup');
            timeRangeGroup.style.display = queryType === 'range' ? 'block' : 'none';
            if (queryType === 'range') {
                initTimeInputs();
            }
        }

        function initTimeInputs() {
            const now = new Date();
            const oneHourAgo = new Date(now.getTime() - 3600000);
            document.getElementById('endTime').value = formatDateTimeLocal(now);
            document.getElementById('startTime').value = formatDateTimeLocal(oneHourAgo);
        }

        function formatDateTimeLocal(date) {
            const year = date.getFullYear();
            const month = String(date.getMonth() + 1).padStart(2, '0');
            const day = String(date.getDate()).padStart(2, '0');
            const hours = String(date.getHours()).padStart(2, '0');
            const minutes = String(date.getMinutes()).padStart(2, '0');
            const seconds = String(date.getSeconds()).padStart(2, '0');
            return year + '-' + month + '-' + day + 'T' + hours + ':' + minutes + ':' + seconds;
        }

        function setTimeRange(seconds) {
            const now = new Date();
            const start = new Date(now.getTime() - (seconds * 1000));
            document.getElementById('endTime').value = formatDateTimeLocal(now);
            document.getElementById('startTime').value = formatDateTimeLocal(start);
        }

        function applyQuickRange() {
            const select = document.getElementById('quickTimeRange');
            const seconds = parseInt(select.value);
            if (seconds > 0) {
                setTimeRange(seconds);
            }
        }

        function refreshQuickRange() {
            applyQuickRange();
        }

        function toggleStepInput() {
            const autoStep = document.getElementById('autoStep').checked;
            const stepInput = document.getElementById('stepInput');
            stepInput.disabled = autoStep;
        }

        async function loadMetrics() {
            try {
                const response = await fetch('/api/v1/labels/__name__/values');
                const data = await response.json();
                
                if (data.status === 'success' && data.data) {
                    allMetrics = data.data;
                    
                    // Update count
                    document.getElementById('metricCount').textContent = allMetrics.length;
                    document.getElementById('metricCount2').textContent = allMetrics.length;
                }
            } catch (error) {
                console.error('Failed to load metrics:', error);
            }
        }

        async function executeQuery() {
            const metric = document.getElementById('metricInput').value.trim();
            if (!metric) {
                alert('Please enter a metric name');
                return;
            }

            const queryType = document.getElementById('queryType').value;
            const resultDiv = document.getElementById('queryResult');
            const outputPre = document.getElementById('queryOutput');
            const chartContainer = document.getElementById('chartContainer');
            
            // Start timing
            const startTime = performance.now();
            
            try {
                let response, data;
                
                if (queryType === 'instant') {
                    response = await fetch('/api/v1/query?query=' + encodeURIComponent(metric) + '&profile=1');
                    const responseReceivedTime = performance.now();
                    data = await response.json();
                    
                    const endTime = performance.now();
                    const responseTime = (endTime - startTime).toFixed(2);
                    const serverProcessing = data.data?.profile?.timings_ms?.total_ms?.toFixed(2) || '0';
                    const serverResponse = (responseReceivedTime - startTime).toFixed(2);
                    const uiTime = (endTime - responseReceivedTime).toFixed(2);
                    
                    resultDiv.style.display = 'block';
                    chartContainer.style.display = 'none';
                    
                    const header = document.createElement('div');
                    header.style.marginBottom = '10px';
                    
                    const serverProcSpan = document.createElement('strong');
                    serverProcSpan.style.color = '#667eea';
                    serverProcSpan.textContent = '🖥️ Server Processing: ' + serverProcessing + ' ms';
                    
                    const sep1 = document.createTextNode(' | ');
                    
                    const serverRespSpan = document.createElement('strong');
                    serverRespSpan.style.color = '#764ba2';
                    serverRespSpan.textContent = '📡 Server Response: ' + serverResponse + ' ms';
                    
                    const sep2 = document.createTextNode(' | ');
                    
                    const uiSpan = document.createElement('strong');
                    uiSpan.style.color = '#f093fb';
                    uiSpan.textContent = '🖼️ UI: ' + uiTime + ' ms';
                    
                    const sep3 = document.createTextNode(' | ');
                    
                    const responseSpan = document.createElement('strong');
                    responseSpan.style.color = '#4facfe';
                    responseSpan.textContent = '⏱️ Total: ' + responseTime + ' ms';
                    
                    header.appendChild(serverProcSpan);
                    header.appendChild(sep1);
                    header.appendChild(serverRespSpan);
                    header.appendChild(sep2);
                    header.appendChild(uiSpan);
                    header.appendChild(sep3);
                    header.appendChild(responseSpan);
                    
                    outputPre.textContent = '\n\n' + JSON.stringify(data, null, 2);
                    outputPre.prepend(header);
                } else {
                    const startInput = document.getElementById('startTime').value;
                    const endInput = document.getElementById('endTime').value;
                    
                    if (!startInput || !endInput) {
                        alert('Please select time range');
                        return;
                    }
                    
                    const start = Math.floor(new Date(startInput).getTime() / 1000);
                    const end = Math.floor(new Date(endInput).getTime() / 1000);
                    
                    // Calculate appropriate step based on time range or use manual input
                    const timeRange = end - start;
                    let step;
                    
                    if (document.getElementById('autoStep').checked) {
                        // Auto calculate step
                        step = 15; // default 15 seconds
                        if (timeRange > 604800) {
                            step = 3600; // 1 hour for ranges > 7 days
                        } else if (timeRange > 86400) {
                            step = 600; // 10 minutes for ranges > 1 day
                        } else if (timeRange > 3600) {
                            step = 60; // 1 minute for ranges > 1 hour
                        }
                    } else {
                        // Use manual input (minimum 1 for raw data)
                        const inputValue = parseInt(document.getElementById('stepInput').value);
                        step = isNaN(inputValue) || inputValue < 1 ? 15 : inputValue;
                    }
                    
                    // Build query URL with step parameter
                    let queryUrl = '/api/v1/query_range?query=' + encodeURIComponent(metric) + 
                        '&start=' + start + '&end=' + end + '&step=' + step + '&profile=1';
                    
                    response = await fetch(queryUrl);
                    const responseReceivedTime = performance.now();
                    data = await response.json();
                    
                    const endTime = performance.now();
                    const responseTime = (endTime - startTime).toFixed(2);
                    const serverProcessing = data.data?.profile?.timings_ms?.total_ms?.toFixed(2) || '0';
                    const serverResponse = (responseReceivedTime - startTime).toFixed(2);
                    const uiTime = (endTime - responseReceivedTime).toFixed(2);
                    const dataPoints = data.data?.result?.[0]?.values?.length || 0;
                    
                    resultDiv.style.display = 'block';
                    
                    const header = document.createElement('div');
                    header.style.marginBottom = '10px';
                    
                    const serverProcSpan = document.createElement('strong');
                    serverProcSpan.style.color = '#667eea';
                    serverProcSpan.textContent = '🖥️ Server Processing: ' + serverProcessing + ' ms';
                    
                    const sep1 = document.createTextNode(' | ');
                    
                    const serverRespSpan = document.createElement('strong');
                    serverRespSpan.style.color = '#764ba2';
                    serverRespSpan.textContent = '📡 Server Response: ' + serverResponse + ' ms';
                    
                    const sep2 = document.createTextNode(' | ');
                    
                    const uiSpan = document.createElement('strong');
                    uiSpan.style.color = '#f093fb';
                    uiSpan.textContent = '🖼️ UI: ' + uiTime + ' ms';
                    
                    const sep3 = document.createTextNode(' | ');
                    
                    const responseSpan = document.createElement('strong');
                    responseSpan.style.color = '#4facfe';
                    responseSpan.textContent = '⏱️ Total: ' + responseTime + ' ms';
                    
                    const sep4 = document.createTextNode(' | ');
                    
                    const dataSpan = document.createElement('strong');
                    dataSpan.style.color = '#00f2fe';
                    dataSpan.textContent = '📊 Points: ' + dataPoints;
                    
                    header.appendChild(serverProcSpan);
                    header.appendChild(sep1);
                    header.appendChild(serverRespSpan);
                    header.appendChild(sep2);
                    header.appendChild(uiSpan);
                    header.appendChild(sep3);
                    header.appendChild(responseSpan);
                    header.appendChild(sep4);
                    header.appendChild(dataSpan);
                    
                    outputPre.textContent = '\n\n' + JSON.stringify(data, null, 2);
                    outputPre.prepend(header);
                    
                    // Draw chart with all series
                    if (data.status === 'success' && data.data.result && data.data.result.length > 0) {
                        const seriesWithData = data.data.result.filter(s => s.values && s.values.length > 0);
                        if (seriesWithData.length > 0) {
                            drawChart(seriesWithData, metric, start, end, step);
                            chartContainer.style.display = 'block';
                        } else {
                            chartContainer.style.display = 'block';
                            const canvas = document.getElementById('metricsChart');
                            const ctx = canvas.getContext('2d');
                            ctx.clearRect(0, 0, canvas.width, canvas.height);
                            ctx.font = '14px Arial';
                            ctx.fillStyle = '#666';
                            ctx.fillText('No data points to display', 10, 20);
                        }
                    }
                }
                
                queryCounter++;
                document.getElementById('queryCount').textContent = queryCounter;
                document.getElementById('queryCount2').textContent = queryCounter;
                
            } catch (error) {
                const endTime = performance.now();
                const responseTime = (endTime - startTime).toFixed(2);
                
                resultDiv.style.display = 'block';
                chartContainer.style.display = 'none';
                
                const header = document.createElement('strong');
                header.style.color = '#f5576c';
                header.textContent = '⏱️ Response: ' + responseTime + ' ms (Error)';
                
                outputPre.textContent = '\n\nError: ' + error.message;
                outputPre.prepend(header);
            }
        }

        function drawChart(seriesArray, metricName, start, end, step) {
            const canvas = document.getElementById('metricsChart');
            const ctx = canvas.getContext('2d');
            
            if (chart) {
                chart.destroy();
            }
            
            // Colors for different series
            const colors = [
                'rgb(102, 126, 234)',
                'rgb(118, 75, 162)',
                'rgb(240, 147, 251)',
                'rgb(79, 172, 254)',
                'rgb(0, 242, 254)',
                'rgb(245, 87, 108)',
                'rgb(255, 159, 64)',
                'rgb(75, 192, 192)',
                'rgb(153, 102, 255)',
                'rgb(255, 205, 86)'
            ];
            
            // Collect all unique timestamps across all series
            const allTimestamps = new Set();
            seriesArray.forEach(series => {
                if (series.values) {
                    series.values.forEach(point => allTimestamps.add(point[0]));
                }
            });
            
            // Sort timestamps
            const sortedTimestamps = Array.from(allTimestamps).sort((a, b) => a - b);
            
            if (sortedTimestamps.length === 0) {
                return;
            }
            
            // Create labels from timestamps with smart formatting
            let prevDate = null;
            let prevHour = null;
            const labels = sortedTimestamps.map(ts => {
                const date = new Date(ts * 1000);
                const year = date.getFullYear();
                const month = String(date.getMonth() + 1).padStart(2, '0');
                const day = String(date.getDate()).padStart(2, '0');
                const hour = String(date.getHours()).padStart(2, '0');
                const minute = String(date.getMinutes()).padStart(2, '0');
                const second = String(date.getSeconds()).padStart(2, '0');
                
                const currentDate = year + '-' + month + '-' + day;
                const currentHour = hour;
                
                let label;
                if (prevDate === null || prevDate !== currentDate) {
                    // Different date: show full date and time
                    label = month + '-' + day + ' ' + hour + ':' + minute + ':' + second;
                    prevDate = currentDate;
                    prevHour = currentHour;
                } else if (prevHour !== currentHour) {
                    // Same date, different hour: show hour:minute:second
                    label = hour + ':' + minute + ':' + second;
                    prevHour = currentHour;
                } else {
                    // Same hour: show minute:second only
                    label = minute + ':' + second;
                }
                
                return label;
            });
            
            // Create datasets for each series
            const datasets = seriesArray.map((series, index) => {
                // Create a map for quick lookup
                const dataMap = new Map();
                if (series.values) {
                    series.values.forEach(point => {
                        dataMap.set(point[0], parseFloat(point[1]));
                    });
                }
                
                // Build data array matching timestamps
                const data = sortedTimestamps.map(ts => dataMap.has(ts) ? dataMap.get(ts) : null);
                
                // Create series label from metric tags
                let seriesLabel = metricName;
                if (series.metric) {
                    const tags = Object.entries(series.metric)
                        .filter(([key]) => key !== '__name__')
                        .map(([key, value]) => key + '="' + value + '"')
                        .join(', ');
                    if (tags) {
                        seriesLabel = metricName + '{' + tags + '}';
                    }
                }
                
                const color = colors[index % colors.length];
                return {
                    label: seriesLabel,
                    data: data,
                    borderColor: color,
                    backgroundColor: color.replace('rgb', 'rgba').replace(')', ', 0.1)'),
                    tension: 0.1,
                    fill: false,
                    spanGaps: false,
                    pointRadius: 2,
                    pointHoverRadius: 5,
                    borderWidth: 2
                };
            });
            
            chart = new Chart(ctx, {
                type: 'line',
                data: {
                    labels: labels,
                    datasets: datasets
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    interaction: {
                        mode: 'index',
                        intersect: false
                    },
                    plugins: {
                        legend: {
                            display: true,
                            position: 'top',
                            maxHeight: 120,
                            labels: {
                                boxWidth: 10,
                                font: {
                                    size: 10
                                },
                                padding: 8,
                                generateLabels: function(chart) {
                                    const labels = Chart.defaults.plugins.legend.labels.generateLabels(chart);
                                    // Limit to first 10 series in legend, rest can be seen in tooltip
                                    if (labels.length > 10) {
                                        const visibleLabels = labels.slice(0, 10);
                                        visibleLabels.push({
                                            text: '... +' + (labels.length - 10) + ' more (hover to see all)',
                                            fillStyle: '#999',
                                            hidden: false,
                                            lineCap: 'butt',
                                            lineDash: [],
                                            lineDashOffset: 0,
                                            lineJoin: 'miter',
                                            lineWidth: 0,
                                            strokeStyle: '#999',
                                            pointStyle: 'circle',
                                            datasetIndex: -1
                                        });
                                        return visibleLabels;
                                    }
                                    return labels;
                                }
                            }
                        },
                        tooltip: {
                            callbacks: {
                                label: function(context) {
                                    if (context.parsed.y === null) {
                                        return context.dataset.label + ': No data';
                                    }
                                    return context.dataset.label + ': ' + context.parsed.y.toFixed(6);
                                }
                            }
                        }
                    },
                    scales: {
                        y: {
                            beginAtZero: false,
                            ticks: {
                                callback: function(value) {
                                    return value.toFixed(2);
                                }
                            }
                        },
                        x: {
                            ticks: {
                                maxRotation: 0,
                                minRotation: 0,
                                maxTicksLimit: 15,
                                font: {
                                    size: 10
                                }
                            }
                        }
                    }
                }
            });
        }

        async function writeData() {
            const metric = document.getElementById('writeMetric').value.trim();
            const value = document.getElementById('writeValue').value;
            const timestamp = document.getElementById('writeTime').value;
            const tagsStr = document.getElementById('writeTags').value.trim();
            
            if (!metric || !value) {
                showWriteResult('Please fill in metric name and value', 'error');
                return;
            }
            
            const payload = {
                metric: metric,
                value: parseFloat(value)
            };
            
            if (timestamp) {
                payload.time = parseInt(timestamp);
            }

            // Parse tags if provided
            if (tagsStr) {
                try {
                    payload.tags = JSON.parse(tagsStr);
                } catch (e) {
                    showWriteResult('Invalid JSON in tags field', 'error');
                    return;
                }
            }
            
            try {
                const response = await fetch('/api/v1/write', {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify(payload)
                });
                
                const data = await response.json();
                
                if (data.status === 'success') {
                    showWriteResult('Data written successfully!', 'success');
                    writeCounter++;
                    document.getElementById('writeCount').textContent = writeCounter;
                    document.getElementById('writeCount2').textContent = writeCounter;
                    
                    // Clear form
                    document.getElementById('writeValue').value = '';
                    document.getElementById('writeTime').value = '';
                    document.getElementById('writeTags').value = '';
                    
                    // Refresh metrics
                    setTimeout(loadMetrics, 500);
                } else {
                    showWriteResult('Error: ' + data.error, 'error');
                }
            } catch (error) {
                showWriteResult('Error: ' + error.message, 'error');
            }
        }

        function showWriteResult(message, type) {
            const resultDiv = document.getElementById('writeResult');
            resultDiv.className = 'alert alert-' + type;
            resultDiv.textContent = message;
            resultDiv.style.display = 'block';
            
            setTimeout(() => {
                resultDiv.style.display = 'none';
            }, 5000);
        }

        // Metrics List Functions
        let cachedMetrics = [];
        let currentPage = 1;
        const pageSize = 100; // Show 100 metrics per page
        const seriesCache = {};
        
        async function loadAllMetrics() {
            const startTime = performance.now();
            const resultDiv = document.getElementById('metricsResult');
            
            // Show loading indicator
            resultDiv.innerHTML = '<div style="text-align: center; padding: 40px;"><div class="spinner"></div><p>Loading metrics...</p></div>';
            
            try {
                const filter = document.getElementById('metricsFilter').value.trim();
                let url = '/api/v1/labels/__name__/values';
                const params = new URLSearchParams();
                
                if (filter) {
                    params.append('filter', filter);
                }
                
                // Request with limit for faster initial load
                params.append('limit', '1000'); // Get first 1000 for faster response
                
                if (params.toString()) {
                    url += '?' + params.toString();
                }
                
                const response = await fetch(url, {
                    signal: AbortSignal.timeout(30000) // 30 second timeout
                });
                
                if (!response.ok) {
                    throw new Error('HTTP ' + response.status + ': ' + response.statusText);
                }
                
                const data = await response.json();
                
                const endTime = performance.now();
                const responseTime = (endTime - startTime).toFixed(2);
                
                if (data.status === 'success') {
                    cachedMetrics = data.data || [];
                    const metadata = data.metadata || {};
                    
                    currentPage = 1;
                    displayMetrics(cachedMetrics, responseTime, metadata);
                    
                    // Update stats
                    const now = new Date().toLocaleTimeString();
                    document.getElementById('lastUpdate').textContent = now;
                } else {
                    resultDiv.innerHTML = 
                        '<div class="alert alert-error">Error: ' + (data.error || 'Unknown error') + '</div>';
                }
            } catch (error) {
                const endTime = performance.now();
                const responseTime = (endTime - startTime).toFixed(2);
                
                let errorMsg = error.message;
                if (error.name === 'AbortError' || error.name === 'TimeoutError') {
                    errorMsg = 'Request timeout - too many metrics. Try using a filter to narrow results.';
                }
                
                resultDiv.innerHTML = 
                    '<div class="alert alert-error">Error loading metrics (' + responseTime + ' ms): ' + errorMsg + 
                    '<br><br><strong>Suggestions:</strong><ul style="margin-top: 10px; text-align: left;">' +
                    '<li>Use filter to search specific metrics (e.g., "node_cpu.*")</li>' +
                    '<li>Server may be processing too many metrics</li>' +
                    '<li>Check browser console for details</li></ul></div>';
            }
        }

        function displayMetrics(metrics, responseTime, metadata = {}) {
            const resultDiv = document.getElementById('metricsResult');
            
            if (!metrics || metrics.length === 0) {
                resultDiv.innerHTML = '<div style="text-align: center; padding: 40px; color: #999;">No metrics found</div>';
                document.getElementById('filteredMetricCount').textContent = '0';
                return;
            }

            const totalCount = metadata.total || metrics.length;
            const offset = metadata.offset || 0;
            const limit = metadata.limit || metrics.length;
            
            let html = '<div style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 15px; border-radius: 8px; margin-bottom: 15px; color: white;">';
            html += '<strong>⏱️ Response Time: ' + responseTime + ' ms</strong> | ';
            html += '<strong>Showing: ' + metrics.length + ' metrics</strong>';
            
            if (totalCount > metrics.length) {
                html += ' <strong>(Total: ' + totalCount + ')</strong>';
                html += '<br><small>⚠️ Showing first ' + limit + ' results. Use filter to narrow search.</small>';
            }
            
            html += '</div>';
            
            // Pagination controls for client-side paging
            const totalPages = Math.ceil(metrics.length / pageSize);
            if (totalPages > 1) {
                html += '<div style="margin-bottom: 15px; text-align: center;">';
                html += 'Page: ';
                for (let i = 1; i <= Math.min(totalPages, 10); i++) {
                    if (i === currentPage) {
                        html += '<strong style="margin: 0 5px; color: #667eea;">' + i + '</strong>';
                    } else {
                        html += '<a href="#" onclick="changePage(' + i + '); return false;" style="margin: 0 5px;">' + i + '</a>';
                    }
                }
                if (totalPages > 10) {
                    html += '... ' + totalPages;
                }
                html += '</div>';
            }
            
            html += '<div style="max-height: 500px; overflow-y: auto; background: white; border: 1px solid #ddd; border-radius: 8px; padding: 15px;">';
            
            const start = (currentPage - 1) * pageSize;
            const end = Math.min(start + pageSize, metrics.length);
            const pageMetrics = metrics.slice(start, end);
            
            pageMetrics.forEach((metric, index) => {
                const globalIndex = start + index + 1;
                const safeMetricAttr = encodeURIComponent(metric);
                const seriesId = 'series_' + globalIndex;
                html += '<div style="padding: 8px; border-bottom: 1px solid #eee; font-family: monospace; font-size: 0.95em;">';
                html += '<div style="display: flex; align-items: center; justify-content: space-between;">';
                html += '<div style="cursor: pointer; color: #2b6cb0;" data-metric="' + safeMetricAttr + '" data-series-id="' + seriesId + '" onclick="toggleSeries(this.dataset.metric, this.dataset.seriesId)">' + globalIndex + '. ' + metric + '</div>';
                html += '<div style="cursor: pointer; color: #718096; padding-left: 8px;" title="Copy metric" onclick="copyText(\'' + safeMetricAttr + '\')">📋</div>';
                html += '</div>';
                html += '<div id="' + seriesId + '" style="margin-top: 6px; padding-left: 16px; display: none;"></div>';
                html += '</div>';
            });
            
            html += '</div>';
            resultDiv.innerHTML = html;
            
            // Update stats
            document.getElementById('metricCount3').textContent = totalCount;
            document.getElementById('filteredMetricCount').textContent = metrics.length;
        }

        function changePage(page) {
            currentPage = page;
            displayMetrics(cachedMetrics, '0', {});
            window.scrollTo(0, 0);
        }

        function filterMetrics() {
            currentPage = 1; // Reset to first page
            loadAllMetrics();
        }

        async function toggleSeries(metric, containerId) {
            metric = decodeURIComponent(metric);
            const container = document.getElementById(containerId);
            if (!container) return;

            if (container.style.display === 'none') {
                container.style.display = 'block';
            } else {
                container.style.display = 'none';
                return;
            }

            if (container.dataset.loaded === 'true') {
                return;
            }

            container.innerHTML = '<div style="color:#666;">Loading series...</div>';

            try {
                const url = '/api/v1/series?match[]=' + encodeURIComponent(metric);
                const response = await fetch(url, { signal: AbortSignal.timeout(30000) });
                if (!response.ok) {
                    throw new Error('HTTP ' + response.status + ': ' + response.statusText);
                }
                const data = await response.json();
                if (data.status !== 'success') {
                    throw new Error(data.error || 'Unknown error');
                }

                const seriesList = data.data || [];
                if (seriesList.length === 0) {
                    container.innerHTML = '<div style="color:#999;">No series found</div>';
                    container.dataset.loaded = 'true';
                    return;
                }

                let html = '<div style="color:#444; font-size: 0.9em; margin-bottom: 4px;">Series: ' + seriesList.length + '</div>';
                html += '<div style="max-height: 200px; overflow-y: auto; border-left: 2px solid #e2e8f0; padding-left: 8px;">';

                seriesList.forEach((s, idx) => {
                    const labels = s.metric || {};
                    const entries = Object.entries(labels).map(([k, v]) => k + '="' + v + '"').join(', ');
                    const encoded = encodeURIComponent('{' + entries + '}');
                    html += '<div style="display: flex; align-items: center; justify-content: space-between; padding: 4px 0; font-family: monospace;">';
                    html += '<div>' + (idx + 1) + '. {' + entries + '}</div>';
                    html += '<div style="cursor: pointer; color: #718096; padding-left: 8px;" title="Copy series" onclick="copyText(\'' + encoded + '\')">📋</div>';
                    html += '</div>';
                });

                html += '</div>';
                container.innerHTML = html;
                container.dataset.loaded = 'true';
            } catch (error) {
                container.innerHTML = '<div style="color:#c53030;">Error: ' + error.message + '</div>';
            }
        }

        async function copyText(encodedText) {
            const text = decodeURIComponent(encodedText);
            try {
                if (navigator.clipboard && navigator.clipboard.writeText) {
                    await navigator.clipboard.writeText(text);
                    return;
                }
            } catch (_) {
                // fallback below
            }

            const temp = document.createElement('textarea');
            temp.value = text;
            temp.setAttribute('readonly', '');
            temp.style.position = 'absolute';
            temp.style.left = '-9999px';
            document.body.appendChild(temp);
            temp.select();
            document.execCommand('copy');
            document.body.removeChild(temp);
        }
    </script>
</body>
</html>
`
