package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CerberusAdapter struct{ client *http.Client }

func NewCerberusAdapter(socketPath string) (*CerberusAdapter, error) {
	if socketPath == "" {
		socketPath = "~/.cerberus/cerberus.sock"
	}
	if strings.HasPrefix(socketPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve Cerberus socket: %w", err)
		}
		socketPath = filepath.Join(home, socketPath[2:])
	}
	if !filepath.IsAbs(socketPath) {
		return nil, fmt.Errorf("cerberus_socket must be an absolute path or start with ~/")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", socketPath)
	}}
	return &CerberusAdapter{client: &http.Client{Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// Routes and DTOs follow Cerberus internal/cerbapi/socket_{client,server}.go.
// Only GET is used; no connector operation or lifecycle command is invoked.
func (a *CerberusAdapter) get(ctx context.Context, path string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://cerberus"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Cerberus-Api", "v1")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("Cerberus GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Do not propagate backend bodies: they can contain operational details.
		return fmt.Errorf("Cerberus GET %s: status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(dst); err != nil {
		return fmt.Errorf("decode Cerberus %s: %w", path, err)
	}
	return nil
}

func (a *CerberusAdapter) List(ctx context.Context) ([]Service, error) {
	var services []Service
	if err := a.get(ctx, "/connectors", &services); err != nil {
		return nil, err
	}
	if services == nil {
		services = []Service{}
	}
	return services, nil
}

func (a *CerberusAdapter) Read(ctx context.Context, id string) (*Service, error) {
	if id == "" {
		return nil, fmt.Errorf("service_id is required")
	}
	services, err := a.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, service := range services {
		if service.ID == id {
			return &service, nil
		}
	}
	return nil, fmt.Errorf("service %q not found", id)
}

func (a *CerberusAdapter) live(ctx context.Context) (map[string]bool, error) {
	var ids []string
	if err := a.get(ctx, "/connectors/live", &ids); err != nil {
		return nil, err
	}
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		live[id] = true
	}
	return live, nil
}

func (a *CerberusAdapter) Status(ctx context.Context, id string) (*ServiceStatus, error) {
	if _, err := a.Read(ctx, id); err != nil {
		return nil, err
	}
	live, err := a.live(ctx)
	if err != nil {
		return nil, err
	}
	return &ServiceStatus{ServiceID: id, Live: live[id]}, nil
}

func (a *CerberusAdapter) Health(ctx context.Context) (*ServiceHealth, error) {
	var runtime json.RawMessage
	if err := a.get(ctx, "/health", &runtime); err != nil {
		return nil, err
	}
	services, err := a.List(ctx)
	if err != nil {
		return nil, err
	}
	live, err := a.live(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]ServiceStatus, 0, len(services))
	for _, service := range services {
		statuses = append(statuses, ServiceStatus{ServiceID: service.ID, Live: live[service.ID]})
	}
	return &ServiceHealth{Runtime: runtime, Connectors: statuses}, nil
}
