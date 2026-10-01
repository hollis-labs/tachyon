package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const maxPollLimit = 200

var errUnsupportedPolling = errors.New("polling is unsupported")

func pollingDescriptor(req SubscribeRequest) (*SubscriptionHandle, error) {
	var allowed string
	limit := 50
	switch req.Channel {
	case "activity", "events":
		allowed = "source kind limit"
	case "logs":
		allowed = "source level limit since until search"
		limit = 100
	case "metrics", "status":
		return nil, fmt.Errorf("%w for channel %s", errUnsupportedPolling, req.Channel)
	default:
		return nil, errors.New("invalid channel: expected activity, logs or events")
	}
	payload := map[string]any{}
	if req.Filter != "" {
		if len(req.Filter) > 8192 {
			return nil, errors.New("filter exceeds 8192 bytes")
		}
		if err := json.Unmarshal([]byte(req.Filter), &payload); err != nil || payload == nil {
			return nil, errors.New("filter must be a JSON object")
		}
	}
	for key, value := range payload {
		if key == "since_id" {
			return nil, fmt.Errorf("%w: snapshot cursors are unavailable", errUnsupportedPolling)
		}
		if !slices.Contains(strings.Fields(allowed), key) {
			return nil, fmt.Errorf("invalid filter field %q", key)
		}
		if key == "limit" {
			n, ok := value.(float64)
			if !ok || n < 1 || n > maxPollLimit || n != float64(int(n)) {
				return nil, fmt.Errorf("limit must be an integer between 1 and %d", maxPollLimit)
			}
			limit = int(n)
			continue
		}
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a string", key)
		}
		if (key == "since" || key == "until") && text != "" {
			if _, err := time.Parse(time.RFC3339, text); err != nil {
				return nil, fmt.Errorf("%s must be RFC3339", key)
			}
		}
	}
	payload["limit"] = limit
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return &SubscriptionHandle{Channel: req.Channel, Endpoint: "/api/verb/observe_" + req.Channel,
		Method: "POST", Payload: body, Transport: "polling", Supported: true, Mode: "snapshot",
		PollIntervalMS: 2000, MaxLimit: maxPollLimit, CursorSupported: false, Cursor: nil, DurableReplay: false}, nil
}
