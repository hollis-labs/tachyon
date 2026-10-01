package main

import (
	"context"
	"encoding/json"
)

// Service is a Cerberus connector definition. Config is a schema, not stored credentials.
type Service struct {
	ID            string          `json:"id"`
	Version       string          `json:"version"`
	ResourceTypes []string        `json:"resource_types"`
	Capabilities  json.RawMessage `json:"capabilities"`
	Config        json.RawMessage `json:"config"`
	Operations    json.RawMessage `json:"operations"`
}

type ServiceStatus struct {
	ServiceID string `json:"service_id"`
	Live      bool   `json:"live"`
}

type ServiceHealth struct {
	// Runtime retains Cerberus's daemon/services/resources health snapshot.
	Runtime    json.RawMessage `json:"runtime"`
	Connectors []ServiceStatus `json:"connectors"`
}

type ServiceAdapter interface {
	List(context.Context) ([]Service, error)
	Read(context.Context, string) (*Service, error)
	Status(context.Context, string) (*ServiceStatus, error)
	Health(context.Context) (*ServiceHealth, error)
}
