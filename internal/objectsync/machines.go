package objectsync

import (
	"context"
	"strings"
	"time"

	"github.com/hollis-labs/tachyon/internal/objects"
)

type CerberusMachines struct {
	remote
	scope objects.SourceScope
}

func NewCerberusMachines(addr, tokenFile string) (*CerberusMachines, error) {
	r, err := newRemote(addr, tokenFile, false)
	if err != nil {
		return nil, err
	}
	instance := r.base
	if strings.HasPrefix(addr, "unix:") {
		instance = addr
	}
	return &CerberusMachines{remote: r, scope: objects.SourceScope{Provider: "cerberus", Instance: instance, Collection: "hosts", Kind: objects.MachineKind}}, nil
}
func (*CerberusMachines) Name() string                 { return "cerberus-machines" }
func (s *CerberusMachines) Scope() objects.SourceScope { return s.scope }

// Only declared inventory and typed probe facts cross into the local store.
// Reach credentials, token-file handles and raw probe error text do not.
type cerberusHost struct {
	Name                string            `json:"name"`
	Hostname            string            `json:"hostname,omitempty"`
	TailnetName         string            `json:"tailnet_name,omitempty"`
	TailnetIP           string            `json:"tailnet_ip,omitempty"`
	LANIP               string            `json:"lan_ip,omitempty"`
	Role                string            `json:"role,omitempty"`
	Provider            string            `json:"provider,omitempty"`
	Owner               string            `json:"owner,omitempty"`
	Labels              map[string]string `json:"labels,omitempty"`
	TetherEnvironmentID string            `json:"tether_environment_id,omitempty"`
	Declared            struct {
		OS     string `json:"os,omitempty"`
		Arch   string `json:"arch,omitempty"`
		CPU    int    `json:"cpu,omitempty"`
		Memory int64  `json:"memory,omitempty"`
		Disk   int64  `json:"disk,omitempty"`
		GPU    string `json:"gpu,omitempty"`
	} `json:"declared"`
}
type cerberusProbe struct {
	Source          string          `json:"source"`
	OS              string          `json:"os,omitempty"`
	OSVersion       string          `json:"os_version,omitempty"`
	Arch            string          `json:"arch,omitempty"`
	Kernel          string          `json:"kernel,omitempty"`
	CPU             int             `json:"cpu,omitempty"`
	CPUModel        string          `json:"cpu_model,omitempty"`
	GPUCount        *int            `json:"gpu_count,omitempty"`
	GPUModel        string          `json:"gpu_model,omitempty"`
	DiskMount       string          `json:"disk_mount,omitempty"`
	MemoryTotal     int64           `json:"memory_total,omitempty"`
	MemoryAvailable int64           `json:"memory_available,omitempty"`
	DiskTotal       int64           `json:"disk_total,omitempty"`
	DiskFree        int64           `json:"disk_free,omitempty"`
	Load1           *float64        `json:"load1,omitempty"`
	Load5           *float64        `json:"load5,omitempty"`
	Load15          *float64        `json:"load15,omitempty"`
	UptimeSeconds   int64           `json:"uptime_seconds,omitempty"`
	Tools           map[string]bool `json:"tools,omitempty"`
}
type cerberusHostView struct {
	Host            cerberusHost   `json:"host"`
	State           string         `json:"state"`
	OnboardingStage string         `json:"onboarding_stage,omitempty"`
	LastSeen        *time.Time     `json:"last_seen,omitempty"`
	ProbedAt        *time.Time     `json:"probed_at,omitempty"`
	CheckedAt       *time.Time     `json:"checked_at,omitempty"`
	Stale           bool           `json:"stale"`
	Probed          *cerberusProbe `json:"probed,omitempty"`
}

func (s *CerberusMachines) Scan(ctx context.Context, at time.Time, progress func(Progress) error) (Snapshot, error) {
	// GET /hosts is the daemon's full declared inventory, independent of probe
	// reachability. This endpoint reads cached probes; it does not initiate one.
	var hosts []cerberusHostView
	if _, err := s.get(ctx, "/hosts", "", &hosts); err != nil {
		return Snapshot{}, err
	}
	if hosts == nil {
		return Snapshot{}, scanError("invalid Cerberus host inventory")
	}
	c := collect(s.scope, at)
	for _, view := range hosts {
		switch view.State {
		case "online", "offline", "unknown":
		default:
			return Snapshot{}, scanError("unknown Cerberus host state")
		}
		h := view.Host
		address := h.TailnetIP
		if address == "" {
			address = h.TailnetName
		}
		if address == "" {
			address = h.LANIP
		}
		// A declared name is Cerberus's API key, not a guessed hostname or IP.
		// Probed OS/arch stay separate from desired/declared platform settings.
		d := objects.Machine{Common: objects.Common{Name: h.Name, Properties: map[string]any{"cerberus": view}}, Hostname: h.Hostname, Address: address, Platform: h.Declared.OS, Architecture: h.Declared.Arch}
		if err := c.add(h.Name, "", false, d); err != nil {
			return Snapshot{}, err
		}
	}
	c.snapshot.Authoritative = true
	if err := progress(Progress{Pages: 1, Items: len(hosts), Checkpoint: "inventory_complete"}); err != nil {
		return Snapshot{}, err
	}
	return c.snapshot, nil
}
