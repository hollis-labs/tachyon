// Package catalogindex is the Builder's read model of the Tether catalog (design B1): a poller
// that reads the catalog files, hashes and parses them, keeps masked snapshots and publishes
// change events. Tether stays the system of record and the index is rebuildable. Environment
// values never enter the index, a snapshot, an event or a log line.
package catalogindex

import (
	"bytes"
	"errors"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const masked = "***"

var (
	secretKey   = regexp.MustCompile(`(?i)(key|secret|token|password|passwd|credential|auth)`)
	secretShape = regexp.MustCompile(`(?i)(sk-[a-z0-9]{12,}|ghp_[a-z0-9]{12,}|github_pat_[a-z0-9_]{12,}|bearer\s+[a-z0-9._-]{12,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|AKIA[0-9A-Z]{12,})`)
	envKeys     = map[string]bool{"env": true, "provider_overrides": true, "environment": true}
)

// MaskYAML returns the content with every environment value, secret-named value and secret-shaped
// scalar replaced by "***", keeping comments, key order and unknown keys, and the number of
// secret-like environment variable names it found. Content that does not parse as YAML is refused
// (it is never stored raw).
func MaskYAML(content string) (string, int, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", 0, errors.New("unparsable yaml")
	}
	n := 0
	maskNode(&doc, false, &n)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", 0, err
	}
	return buf.String(), n, nil
}

func maskNode(nd *yaml.Node, inEnv bool, secrets *int) {
	switch nd.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range nd.Content {
			maskNode(c, inEnv, secrets)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(nd.Content); i += 2 {
			k, v := nd.Content[i], nd.Content[i+1]
			env := inEnv || envKeys[strings.ToLower(k.Value)]
			if env && inEnv && secretKey.MatchString(k.Value) {
				*secrets++
			}
			maskNode(v, env, secrets)
			if v.Kind == yaml.ScalarNode && v.Value != "" && (env || secretKey.MatchString(k.Value)) {
				v.Value, v.Tag, v.Style = masked, "!!str", 0
			}
		}
	case yaml.ScalarNode:
		if secretShape.MatchString(nd.Value) {
			nd.Value, nd.Tag, nd.Style = masked, "!!str", 0
		} else if inEnv && nd.Value != "" {
			nd.Value, nd.Tag, nd.Style = masked, "!!str", 0
		}
	}
}

// MaskText scrubs secret-shaped strings from a markdown or free-text file (skills).
func MaskText(s string) string { return secretShape.ReplaceAllString(s, masked) }
