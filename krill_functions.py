"""
Krill Functions Module - Example custom Python functions for Krill TSDB

This module demonstrates how to create custom Python functions
that can be dynamically loaded by PyKrill daemon.

Each function receives the query result data and returns modified data.
"""

def double_values(input_data):
    """
    Double all metric values
    
    Args:
        input_data: Dict with 'resultType' and 'result' (list of series)
    
    Returns:
        Modified input_data with doubled values
    """
    if 'result' in input_data:
        for series in input_data['result']:
            if 'values' in series:
                series['values'] = [
                    [ts, str(float(val) * 2)]
                    for ts, val in series['values']
                ]
    return input_data


def add_label(input_data):
    """
    Add a custom label to all series
    
    Args:
        input_data: Dict with query results
    
    Returns:
        Modified input_data with added label
    """
    if 'result' in input_data:
        for series in input_data['result']:
            if 'metric' in series:
                series['metric']['processed'] = 'true'
                series['metric']['processor'] = 'krill_functions'
    return input_data


def filter_high_values(input_data, threshold=100.0):
    """
    Filter out series where all values are below threshold
    
    Args:
        input_data: Dict with query results
        threshold: Minimum value to keep series (default: 100.0)
    
    Returns:
        Filtered input_data
    """
    if 'result' in input_data:
        filtered_result = []
        for series in input_data['result']:
            if 'values' in series:
                # Check if any value exceeds threshold
                has_high_value = any(
                    float(val) >= threshold
                    for ts, val in series['values']
                )
                if has_high_value:
                    filtered_result.append(series)
        input_data['result'] = filtered_result
    return input_data


def aggregate_sum(input_data):
    """
    Sum all series into a single aggregated series
    
    Args:
        input_data: Dict with query results
    
    Returns:
        Single aggregated series
    """
    if 'result' in input_data:
        aggregated_values = {}
        
        # Sum all values by timestamp
        for series in input_data['result']:
            if 'values' in series:
                for ts, val in series['values']:
                    aggregated_values[ts] = aggregated_values.get(ts, 0.0) + float(val)
        
        # Create new result
        input_data['result'] = [{
            'metric': {'__name__': 'aggregated_sum'},
            'values': [[ts, str(val)] for ts, val in sorted(aggregated_values.items())]
        }]
    
    return input_data


def moving_average(input_data, window=3):
    """
    Calculate moving average for each series
    
    Args:
        input_data: Dict with query results
        window: Window size for moving average (default: 3)
    
    Returns:
        Modified input_data with smoothed values
    """
    if 'result' in input_data:
        for series in input_data['result']:
            if 'values' in series and len(series['values']) >= window:
                values = [float(val) for ts, val in series['values']]
                timestamps = [ts for ts, val in series['values']]
                
                # Calculate moving average
                smoothed = []
                for i in range(len(values)):
                    if i < window - 1:
                        # Not enough data for window, use original value
                        smoothed.append(values[i])
                    else:
                        # Calculate average of window
                        avg = sum(values[i-window+1:i+1]) / window
                        smoothed.append(avg)
                
                # Update values
                series['values'] = [
                    [timestamps[i], str(smoothed[i])]
                    for i in range(len(smoothed))
                ]
    
    return input_data


def rate_of_change(input_data):
    """
    Calculate rate of change (delta/time) between consecutive points
    
    Args:
        input_data: Dict with query results
    
    Returns:
        Modified input_data with rate values
    """
    if 'result' in input_data:
        for series in input_data['result']:
            if 'values' in series and len(series['values']) >= 2:
                values = [(int(ts), float(val)) for ts, val in series['values']]
                
                # Calculate rates
                rates = []
                for i in range(1, len(values)):
                    ts_prev, val_prev = values[i-1]
                    ts_curr, val_curr = values[i]
                    
                    time_delta = ts_curr - ts_prev
                    if time_delta > 0:
                        rate = (val_curr - val_prev) / time_delta
                        rates.append([ts_curr, str(rate)])
                
                # Update with rates (skip first point)
                series['values'] = rates
    
    return input_data


def passthrough(input_data):
    """
    Simple passthrough function for testing
    
    Args:
        input_data: Any data
    
    Returns:
        Same data unchanged
    """
    return input_data
