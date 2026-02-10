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
            height: 350px;
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
                        <option value="instant">Instant Query (Latest Value)</option>
                        <option value="range">Range Query (Time Series)</option>
                    </select>
                </div>
                <div id="timeRangeGroup" class="form-group" style="display: none;">
                    <div class="form-group">
                        <label for="quickTimeRange">Quick Time Range:</label>
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
                    </div>
                    <label>Custom Range:</label>
                    <div class="time-range">
                        <div>
                            <label for="startTime">From:</label>
                            <input type="datetime-local" id="startTime">
                        </div>
                        <div>
                            <label for="endTime">To:</label>
                            <input type="datetime-local" id="endTime">
                        </div>
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
                    <label for="writeTags">Tags (JSON, optional):</label>
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
            return year + '-' + month + '-' + day + 'T' + hours + ':' + minutes;
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

        async function loadMetrics() {
            try {
                const response = await fetch('/api/v1/label/__name__/values');
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
                    response = await fetch('/api/v1/query?query=' + encodeURIComponent(metric));
                    data = await response.json();
                    
                    const endTime = performance.now();
                    const responseTime = (endTime - startTime).toFixed(2);
                    
                    resultDiv.style.display = 'block';
                    chartContainer.style.display = 'none';
                    outputPre.innerHTML = '<strong style="color: #667eea;">⏱️ Response Time: ' + responseTime + ' ms</strong>\n\n' + 
                                          JSON.stringify(data, null, 2);
                } else {
                    const startInput = document.getElementById('startTime').value;
                    const endInput = document.getElementById('endTime').value;
                    
                    if (!startInput || !endInput) {
                        alert('Please select time range');
                        return;
                    }
                    
                    const start = Math.floor(new Date(startInput).getTime() / 1000);
                    const end = Math.floor(new Date(endInput).getTime() / 1000);
                    
                    // Calculate appropriate step based on time range
                    const timeRange = end - start;
                    let step = 15; // default 15 seconds
                    if (timeRange > 604800) {
                        step = 3600; // 1 hour for ranges > 7 days
                    } else if (timeRange > 86400) {
                        step = 600; // 10 minutes for ranges > 1 day
                    } else if (timeRange > 3600) {
                        step = 60; // 1 minute for ranges > 1 hour
                    }
                    
                    response = await fetch(
                        '/api/v1/query_range?query=' + encodeURIComponent(metric) + 
                        '&start=' + start + '&end=' + end + '&step=' + step
                    );
                    data = await response.json();
                    
                    const endTime = performance.now();
                    const responseTime = (endTime - startTime).toFixed(2);
                    const dataPoints = data.data?.result?.[0]?.values?.length || 0;
                    
                    resultDiv.style.display = 'block';
                    outputPre.innerHTML = '<strong style="color: #667eea;">⏱️ Response Time: ' + responseTime + ' ms</strong> | ' +
                                          '<strong style="color: #764ba2;">📊 Data Points: ' + dataPoints + '</strong>\n\n' +
                                          JSON.stringify(data, null, 2);
                    
                    // Draw chart
                    if (data.status === 'success' && data.data.result && data.data.result.length > 0) {
                        drawChart(data.data.result[0], metric, start, end, step);
                        chartContainer.style.display = 'block';
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
                outputPre.innerHTML = '<strong style="color: #f5576c;">⏱️ Response Time: ' + responseTime + ' ms (Error)</strong>\n\n' +
                                      'Error: ' + error.message;
            }
        }

        function drawChart(result, metricName, start, end, step) {
            const canvas = document.getElementById('metricsChart');
            const ctx = canvas.getContext('2d');
            
            if (chart) {
                chart.destroy();
            }
            
            // Create a map of existing data points
            const dataMap = new Map();
            let firstDataTs = null;
            let lastDataTs = null;
            
            if (result.values && result.values.length > 0) {
                result.values.forEach(point => {
                    const ts = point[0];
                    dataMap.set(ts, parseFloat(point[1]));
                    if (firstDataTs === null || ts < firstDataTs) firstDataTs = ts;
                    if (lastDataTs === null || ts > lastDataTs) lastDataTs = ts;
                });
            }
            
            // If no data, return empty chart
            if (firstDataTs === null) {
                return;
            }
            
            // Generate timestamps from first data point to last data point
            const labels = [];
            const values = [];
            
            for (let ts = firstDataTs; ts <= lastDataTs; ts += step) {
                const date = new Date(ts * 1000);
                labels.push(date.toLocaleString());
                
                // Use the actual value if it exists, otherwise null (will show as gap)
                if (dataMap.has(ts)) {
                    values.push(dataMap.get(ts));
                } else {
                    values.push(null);
                }
            }
            
            chart = new Chart(ctx, {
                type: 'line',
                data: {
                    labels: labels,
                    datasets: [{
                        label: metricName,
                        data: values,
                        borderColor: 'rgb(102, 126, 234)',
                        backgroundColor: 'rgba(102, 126, 234, 0.1)',
                        tension: 0.4,
                        fill: true,
                        spanGaps: false  // Don't connect across null values
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: {
                        legend: {
                            display: true,
                            position: 'top'
                        },
                        tooltip: {
                            callbacks: {
                                label: function(context) {
                                    if (context.parsed.y === null) {
                                        return metricName + ': No data';
                                    }
                                    return metricName + ': ' + context.parsed.y;
                                }
                            }
                        }
                    },
                    scales: {
                        y: {
                            beginAtZero: false
                        },
                        x: {
                            ticks: {
                                maxRotation: 45,
                                minRotation: 45
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
        
        async function loadAllMetrics() {
            const startTime = performance.now();
            
            try {
                const filter = document.getElementById('metricsFilter').value.trim();
                const url = filter ? '/api/v1/metrics?filter=' + encodeURIComponent(filter) : '/api/v1/metrics';
                
                const response = await fetch(url);
                const data = await response.json();
                
                const endTime = performance.now();
                const responseTime = (endTime - startTime).toFixed(2);
                
                if (data.status === 'success') {
                    cachedMetrics = data.data || [];
                    displayMetrics(cachedMetrics, responseTime);
                    
                    // Update stats
                    const now = new Date().toLocaleTimeString();
                    document.getElementById('lastUpdate').textContent = now;
                } else {
                    document.getElementById('metricsResult').innerHTML = 
                        '<div class="alert alert-error">Error: ' + data.error + '</div>';
                }
            } catch (error) {
                const endTime = performance.now();
                const responseTime = (endTime - startTime).toFixed(2);
                
                document.getElementById('metricsResult').innerHTML = 
                    '<div class="alert alert-error">Error loading metrics (' + responseTime + ' ms): ' + error.message + '</div>';
            }
        }

        function displayMetrics(metrics, responseTime) {
            const resultDiv = document.getElementById('metricsResult');
            
            if (!metrics || metrics.length === 0) {
                resultDiv.innerHTML = '<div style="text-align: center; padding: 40px; color: #999;">No metrics found</div>';
                document.getElementById('filteredMetricCount').textContent = '0';
                return;
            }

            let html = '<div style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 15px; border-radius: 8px; margin-bottom: 15px; color: white;">';
            html += '<strong>⏱️ Response Time: ' + responseTime + ' ms</strong> | ';
            html += '<strong>Total: ' + metrics.length + ' metrics</strong>';
            html += '</div>';
            html += '<div style="max-height: 500px; overflow-y: auto; background: white; border: 1px solid #ddd; border-radius: 8px; padding: 15px;">';
            
            metrics.forEach((metric, index) => {
                html += '<div style="padding: 8px; border-bottom: 1px solid #eee; font-family: monospace; font-size: 0.95em;">';
                html += (index + 1) + '. ' + metric;
                html += '</div>';
            });
            
            html += '</div>';
            resultDiv.innerHTML = html;
            
            // Update stats
            document.getElementById('metricCount3').textContent = allMetrics.length || metrics.length;
            document.getElementById('filteredMetricCount').textContent = metrics.length;
        }

        function filterMetrics() {
            loadAllMetrics();
        }
    </script>
</body>
</html>
`
