package carddelivery

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hollis-labs/libs/core/util/router"
	"github.com/hollis-labs/libs/mesh/core/hitlrouting"
	"github.com/hollis-labs/libs/mesh/core/messaging"
)

// HoldSink is the sink id meaning "Tachyon keeps it; the asker pulls through
// card.await or card.get". It needs no configuration and is always valid.
const HoldSink = "tachyon"

// File is the host configuration of card delivery: the routing profile (the
// libs hitlrouting rule file) plus what each sink id means on this host. Sink
// options live here, not in the profile, and a card cannot name an address.
type File struct {
	// From is the address pushed messages are sent as.
	From  messaging.Address `json:"from"`
	Sinks map[string]struct {
		Kind string            `json:"kind"` // "tether_message"
		To   messaging.Address `json:"to"`
	} `json:"sinks"`
	Profile json.RawMessage `json:"profile"`
}

// Load reads and validates a delivery file. It fails on a profile that names a
// sink this host does not define, so a typo surfaces at boot and not as a
// card that is silently never delivered.
func Load(path string, n Notifier) (*hitlrouting.Router, map[string]Sink, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, nil, fmt.Errorf("card delivery file: %w", err)
	}
	profile, err := hitlrouting.Parse(f.Profile)
	if err != nil {
		return nil, nil, fmt.Errorf("card delivery profile: %w", err)
	}
	sinks := map[string]Sink{}
	for id, c := range f.Sinks {
		if c.Kind != "tether_message" || c.To.IsZero() || f.From.IsZero() {
			return nil, nil, fmt.Errorf("card delivery sink %q: kind tether_message needs a to address and the file needs from", id)
		}
		if n == nil {
			return nil, nil, fmt.Errorf("card delivery sink %q: no Tether connection configured", id)
		}
		sinks[id] = TetherSink{N: n, From: f.From, To: c.To}
	}
	used := []string{profile.CatchAll.Sink}
	for _, r := range profile.Rules {
		used = append(used, r.Sink)
	}
	for _, id := range used {
		if _, ok := sinks[id]; !ok && id != HoldSink {
			return nil, nil, fmt.Errorf("card delivery profile names sink %q, which the file does not define", id)
		}
	}
	r, err := hitlrouting.Compile(profile, router.Config{})
	if err != nil {
		return nil, nil, err
	}
	return r, sinks, nil
}
