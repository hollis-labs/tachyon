package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type torqueHTTPError struct {
	method, path, body string
	status             int
}

func (e *torqueHTTPError) Error() string {
	return fmt.Sprintf("torque %s %s: HTTP %d: %s", e.method, e.path, e.status, e.body)
}

// One retry handles either deploy direction. Other failures remain provider
// errors; an unsupported parameter must never look like an empty task list.
func (a *TorqueAdapter) requestWorkList(ctx context.Context, path string, q url.Values, search bool) (*WorkList, error) {
	wantTotal := a.pagedLists.Load()
	for attempt := 0; attempt < 2; attempt++ {
		if wantTotal {
			q.Set("include_total", "true")
		} else {
			q.Del("include_total")
		}
		var raw json.RawMessage
		if err := a.request(ctx, http.MethodGet, path+"?"+q.Encode(), nil, &raw); err != nil {
			var failure *torqueHTTPError
			if wantTotal && errors.As(err, &failure) && failure.status == http.StatusBadRequest && strings.Contains(failure.body, "include_total") {
				a.pagedLists.Store(false)
				if attempt == 0 {
					wantTotal = false
					continue
				}
			}
			return nil, err
		}
		offset, err := strconv.Atoi(q.Get("offset"))
		if err != nil {
			return nil, fmt.Errorf("invalid work offset: %w", err)
		}
		list, paged, totalPresent, err := decodeTorqueList(raw, offset, search)
		if err != nil {
			return nil, fmt.Errorf("decode torque list: %w", err)
		}
		a.pagedLists.Store(paged)
		if paged && !totalPresent {
			if attempt == 0 && !wantTotal {
				wantTotal = true
				continue
			}
			return nil, fmt.Errorf("torque paged list omitted requested meta.total")
		}
		return list, nil
	}
	panic("unreachable work-list retry")
}

type torqueListMeta struct {
	Total      *int  `json:"total"`
	Returned   *int  `json:"returned"`
	Limit      *int  `json:"limit"`
	Offset     *int  `json:"offset"`
	HasMore    *bool `json:"has_more"`
	NextOffset *int  `json:"next_offset"`
}

func decodeTorqueList(raw json.RawMessage, offset int, search bool) (*WorkList, bool, bool, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, false, false, err
	}
	tasks, legacy := body["tasks"]
	items, paged := body["items"]
	if legacy == paged {
		return nil, false, false, fmt.Errorf("expected exactly one of tasks or items")
	}
	var meta torqueListMeta
	if paged {
		tasks = items
		if string(body["meta"]) == "null" || len(body["meta"]) == 0 {
			return nil, true, false, fmt.Errorf("missing meta object")
		}
		if err := json.Unmarshal(body["meta"], &meta); err != nil {
			return nil, true, false, err
		}
	} else if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, false, false, err
	}
	var list WorkList
	if string(tasks) == "null" {
		return nil, paged, false, fmt.Errorf("task items must be an array")
	}
	if err := json.Unmarshal(tasks, &list.Tasks); err != nil {
		return nil, paged, false, err
	}
	if paged && (meta.Returned == nil || meta.Limit == nil || meta.Offset == nil || meta.HasMore == nil) {
		return nil, true, false, fmt.Errorf("offset page requires returned, limit, offset and has_more metadata")
	}
	if !paged && !search && (meta.Total == nil || meta.HasMore == nil) {
		return nil, false, false, fmt.Errorf("legacy list requires total and has_more")
	}
	if meta.Total != nil && *meta.Total < 0 {
		return nil, paged, false, fmt.Errorf("total must be nonnegative")
	}
	if meta.Returned != nil && *meta.Returned != len(list.Tasks) {
		return nil, paged, false, fmt.Errorf("returned does not match task items")
	}
	if meta.Limit != nil && (*meta.Limit <= 0 || (paged && (*meta.Limit > 200 || len(list.Tasks) > *meta.Limit))) {
		return nil, paged, false, fmt.Errorf("invalid page limit")
	}
	if meta.Offset != nil && *meta.Offset != offset {
		return nil, paged, false, fmt.Errorf("page offset does not match request")
	}
	if meta.HasMore != nil {
		list.HasMore = *meta.HasMore
	}
	if list.HasMore {
		if len(list.Tasks) == 0 || meta.NextOffset == nil || *meta.NextOffset <= offset {
			return nil, paged, false, fmt.Errorf("has_more requires a forward next_offset")
		}
		if paged && *meta.NextOffset != offset+len(list.Tasks) {
			return nil, true, false, fmt.Errorf("next_offset must equal offset plus returned")
		}
		list.NextOffset = meta.NextOffset
	} else if meta.NextOffset != nil {
		return nil, paged, false, fmt.Errorf("final page must not have next_offset")
	}
	if search && len(list.Tasks) > 200 {
		// Old search is unpaged even when sent limit/offset. Bound the verb's
		// output without walking provider pages or inventing a continuation.
		list.Tasks = list.Tasks[:200]
		list.HasMore = true
	}
	list.Total = len(list.Tasks)
	if meta.Total != nil {
		list.Total = *meta.Total
	}
	return &list, paged, meta.Total != nil, nil
}
