package adapter

import (
	"encoding/json"
	"strconv"
	"strings"
)

func decodePricingModels(body []byte) ([]pricingModel, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	items := findArray(value, 0)
	// Some embedded NewAPI probes expose the selected catalog as a plain
	// string array rather than model objects.
	if len(items) == 0 {
		if names := findStringArray(value, 0); len(names) > 0 {
			items = make([]any, 0, len(names))
			for _, name := range names {
				items = append(items, map[string]any{"model": name})
			}
		}
	}
	models := make([]pricingModel, 0, len(items))
	for _, item := range items {
		if name, ok := item.(string); ok {
			if name = strings.TrimSpace(name); name != "" {
				models = append(models, pricingModel{Model: name})
			}
			continue
		}
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		model := pricingModel{
			Model:    firstString(object, "model", "model_name", "name", "id"),
			Provider: firstString(object, "provider", "supplier", "vendor"),
			Group:    firstString(object, "group", "group_name", "channel_name", "channel"),
			Channel:  firstString(object, "channel"),
			Status:   firstString(object, "status", "state"),
		}
		if channel, ok := object["channel"].(map[string]any); ok {
			if model.Group == "" {
				model.Group = firstString(channel, "name", "group", "slug")
			}
			if model.Provider == "" {
				model.Provider = firstString(channel, "provider", "type")
			}
		}
		if health, ok := object["health"].(map[string]any); ok {
			if current, ok := health["current"].(map[string]any); ok {
				model.SuccessRate = numberPointer(current, "success_rate", "successRate")
				model.Latency = numberPointer(current, "latency_ms", "avg_latency_ms", "latency")
			}
			_, model.HistoryPresent = health["buckets"]
			model.Buckets = decodeEmbeddedHealthBuckets(health, model.Group)
		}
		model.Groups = stringSlice(object, "enable_groups", "groups", "group_names")
		if value := numberPointer(object, "success_rate", "successRate", "success_ratio", "successRatio"); value != nil {
			model.SuccessRate = value
		}
		if value := numberPointer(object, "latency", "avg_latency", "avg_latency_ms", "average_latency", "average_latency_ms"); value != nil {
			model.Latency = value
		}
		model.TPS = numberPointer(object, "tps", "avg_tps", "tokens_per_second", "tokensPerSecond")
		if strings.TrimSpace(model.Model) != "" {
			models = append(models, model)
		}
	}
	return models, nil
}

func decodeEmbeddedHealthBuckets(health map[string]any, groupName string) []detailBucket {
	rawBuckets, ok := health["buckets"].([]any)
	if !ok {
		return nil
	}
	buckets := make([]detailBucket, 0, len(rawBuckets))
	for _, rawBucket := range rawBuckets {
		object, ok := rawBucket.(map[string]any)
		if !ok {
			continue
		}
		bucket := detailBucket{
			Complete:    boolPointer(object, "is_complete", "complete"),
			Timestamp:   int64Value(object, "timestamp", "ts", "time_unix"),
			Time:        firstString(object, "bucket_start", "start", "time", "timestamp_iso"),
			EndTime:     firstString(object, "bucket_end", "end", "end_time", "end_time_iso"),
			Group:       groupName,
			SuccessRate: numberPointer(object, "success_rate", "successRate", "success_ratio", "successRatio"),
			Latency:     numberPointer(object, "latency_ms", "avg_latency_ms", "latency"),
		}
		bucket.Requests = intPointer(object, "sample_count", "total_requests", "requests", "request_count")
		bucket.Success = intPointer(object, "success_count", "success", "successCount")
		bucket.Failure = intPointer(object, "failure_count", "failure", "failureCount")
		bucket.Empty = intPointer(object, "empty_count", "empty", "emptyCount")
		if bucket.Requests == nil && (bucket.Success != nil || bucket.Failure != nil) {
			requests := int64(0)
			if bucket.Success != nil {
				requests += *bucket.Success
			}
			if bucket.Failure != nil {
				requests += *bucket.Failure
			}
			bucket.Requests = &requests
		}
		if bucket.Timestamp != 0 || bucket.Time != "" {
			buckets = append(buckets, bucket)
		}
	}
	return buckets
}

func findStringArray(value any, depth int) []string {
	if depth > 4 {
		return nil
	}
	if items, ok := value.([]any); ok {
		result := make([]string, 0, len(items))
		for _, item := range items {
			name, ok := item.(string)
			if !ok || strings.TrimSpace(name) == "" {
				return nil
			}
			result = append(result, strings.TrimSpace(name))
		}
		return result
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"data", "models", "items", "records", "result", "payload"} {
		if child, exists := object[key]; exists {
			if result := findStringArray(child, depth+1); len(result) > 0 {
				return result
			}
		}
	}
	return nil
}

func stringSlice(object map[string]any, keys ...string) []string {
	for _, key := range keys {
		items, ok := object[key].([]any)
		if !ok {
			continue
		}
		result := make([]string, 0, len(items))
		for _, item := range items {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" {
				result = append(result, strings.TrimSpace(value))
			}
		}
		return result
	}
	return nil
}

func decodeDetailBuckets(body []byte) ([]detailBucket, error) {
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	if probeBuckets := decodeProbeStatus(value); len(probeBuckets) > 0 {
		return probeBuckets, nil
	}
	if groups := findGroups(value, 0); len(groups) > 0 {
		return decodeNewAPIGroups(groups), nil
	}
	items := findArray(value, 0)
	buckets := make([]detailBucket, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		bucket := detailBucket{
			Timestamp:    int64Value(object, "timestamp", "ts", "time_unix"),
			EndTimestamp: int64Value(object, "end_timestamp", "end_ts", "end_time"),
			Time:         firstString(object, "time", "timestamp_iso", "start", "bucket_start"),
			EndTime:      firstString(object, "end", "bucket_end", "end_time", "end_time_iso"),
			Group:        firstString(object, "group", "group_name", "channel", "channel_name", "token_group", "tokenGroup"),
			SuccessRate:  numberPointer(object, "success_rate", "successRate", "success_ratio", "successRatio"),
			Latency:      numberPointer(object, "latency", "avg_latency", "avg_latency_ms", "average_latency", "average_latency_ms"),
			TTFT:         numberPointer(object, "ttft", "avg_ttft_ms", "first_token_ms", "firstTokenMs"),
			TPS:          numberPointer(object, "tps", "avg_tps", "tokens_per_second", "tokensPerSecond"),
		}
		bucket.Requests = intPointer(object, "requests", "request_count", "requestCount")
		bucket.Success = intPointer(object, "success", "success_count", "successCount")
		bucket.Failure = intPointer(object, "failure", "failure_count", "failureCount")
		bucket.Empty = intPointer(object, "empty", "empty_count", "emptyCount")
		if bucket.Timestamp != 0 || bucket.Time != "" || bucket.SuccessRate != nil || bucket.Latency != nil {
			buckets = append(buckets, bucket)
		}
	}
	return buckets, nil
}

func decodeProbeStatus(value any) []detailBucket {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if nested, ok := object["data"].(map[string]any); ok {
		object = nested
	}
	slots, ok := object["slot_data"].([]any)
	if !ok {
		return nil
	}
	result := []detailBucket{{
		Aggregate:   true,
		SuccessRate: numberPointer(object, "success_rate"),
		Requests:    intPointer(object, "total_requests"),
		Success:     intPointer(object, "success_count"),
		Failure:     intPointer(object, "failure_count", "error_count"),
		Empty:       intPointer(object, "empty_count"),
	}}
	for _, rawSlot := range slots {
		slot, ok := rawSlot.(map[string]any)
		if !ok {
			continue
		}
		result = append(result, detailBucket{
			Timestamp:    int64Value(slot, "start_time"),
			EndTimestamp: int64Value(slot, "end_time", "end_timestamp"),
			SuccessRate:  numberPointer(slot, "success_rate"),
			Requests:     intPointer(slot, "total_requests"),
			Success:      intPointer(slot, "success_count"),
			Failure:      intPointer(slot, "failure_count", "error_count"),
			Empty:        intPointer(slot, "empty_count"),
		})
	}
	return result
}

func findGroups(value any, depth int) []any {
	if depth > 5 {
		return nil
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if groups, ok := object["groups"].([]any); ok {
		return groups
	}
	for _, key := range []string{"data", "result", "payload"} {
		if child, exists := object[key]; exists {
			if groups := findGroups(child, depth+1); len(groups) > 0 {
				return groups
			}
		}
	}
	return nil
}

func decodeNewAPIGroups(groups []any) []detailBucket {
	result := make([]detailBucket, 0)
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}
		name := firstString(group, "group", "group_name", "name")
		series, _ := group["series"].([]any)
		result = append(result, detailBucket{
			Aggregate:   true,
			Group:       name,
			SuccessRate: numberPointer(group, "success_rate", "successRatio"),
			Latency:     numberPointer(group, "avg_latency_ms", "average_latency_ms", "latency"),
			TTFT:        numberPointer(group, "avg_ttft_ms", "first_token_ms", "ttft"),
			TPS:         numberPointer(group, "avg_tps", "tokens_per_second", "tps"),
		})
		for _, rawPoint := range series {
			point, ok := rawPoint.(map[string]any)
			if !ok {
				continue
			}
			result = append(result, detailBucket{
				Timestamp:    int64Value(point, "ts", "timestamp"),
				EndTimestamp: int64Value(point, "end_ts", "end_timestamp", "end_time"),
				Group:        name,
				SuccessRate:  numberPointer(point, "success_rate", "successRatio"),
				Latency:      numberPointer(point, "avg_latency_ms", "average_latency_ms", "latency"),
				TTFT:         numberPointer(point, "avg_ttft_ms", "first_token_ms", "ttft"),
				TPS:          numberPointer(point, "avg_tps", "tokens_per_second", "tps"),
			})
		}
	}
	return result
}

func findArray(value any, depth int) []any {
	if depth > 4 {
		return nil
	}
	if items, ok := value.([]any); ok {
		return items
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"data", "models", "items", "records", "result", "payload"} {
		if child, exists := object[key]; exists {
			if childObject, ok := child.(map[string]any); ok {
				for _, arrayKey := range []string{"models", "items", "records"} {
					if items, ok := childObject[arrayKey].([]any); ok {
						return items
					}
				}
			}
			if items := findArray(child, depth+1); len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

func firstString(object map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := object[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	for _, key := range []string{"model", "model_info", "modelInfo"} {
		if nested, ok := object[key].(map[string]any); ok {
			if value := firstString(nested, "name", "model", "model_name", "id", "slug"); value != "" {
				return value
			}
		}
	}
	return ""
}

func numberPointer(object map[string]any, keys ...string) *float64 {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			switch typed := value.(type) {
			case float64:
				return &typed
			case json.Number:
				if parsed, err := typed.Float64(); err == nil {
					return &parsed
				}
			case string:
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(typed, "%")), 64); err == nil {
					if strings.HasSuffix(typed, "%") {
						parsed /= 100
					}
					return &parsed
				}
			}
		}
	}
	return nil
}

func boolPointer(object map[string]any, keys ...string) *bool {
	for _, key := range keys {
		if value, ok := object[key].(bool); ok {
			return &value
		}
	}
	return nil
}

func intPointer(object map[string]any, keys ...string) *int64 {
	for _, key := range keys {
		if value, ok := object[key]; ok {
			var parsed int64
			switch typed := value.(type) {
			case float64:
				parsed = int64(typed)
			case string:
				parsedValue, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
				if err != nil {
					continue
				}
				parsed = parsedValue
			default:
				continue
			}
			return &parsed
		}
	}
	return nil
}

func int64Value(object map[string]any, keys ...string) int64 {
	value := intPointer(object, keys...)
	if value == nil {
		return 0
	}
	return *value
}
