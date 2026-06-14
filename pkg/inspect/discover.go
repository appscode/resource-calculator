/*
Copyright AppsCode Inc. and Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package inspect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// Source selects how a provider's databases are discovered. Discovery is
// layered: the preferred order is sdk, then cli, then rest, then file. SDK
// adapters are an extension point (see DiscovererFor) and are not compiled in
// by default to keep this CLI free of the large cloud SDK dependency trees.
type Source string

const (
	// SourceAuto picks the best available source: a saved file if --from-file
	// is set, otherwise the provider's native live source (cli or rest).
	SourceAuto Source = "auto"
	// SourceSDK uses an official cloud SDK adapter (extension point).
	SourceSDK Source = "sdk"
	// SourceCLI shells out to the provider's installed CLI (aws/az/gcloud/oci).
	SourceCLI Source = "cli"
	// SourceREST calls the provider's REST control-plane API directly.
	SourceREST Source = "rest"
	// SourceFile parses previously exported JSON (no live calls, no creds).
	SourceFile Source = "file"
)

// hoursPerMonth is the convention used to turn hourly cloud rates into monthly
// figures (matches how AWS/Azure/GCP quote "per month").
const hoursPerMonth = 730

// Options controls a discovery run.
type Options struct {
	Source Source
	// FromFile is a path to exported JSON for SourceFile/SourceAuto.
	FromFile string

	// Regions limits the cloud regions scanned (empty means the provider/CLI
	// default, or every enabled region when AllRegions is set).
	Regions    []string
	AllRegions bool
	// Org requests an organization/all-accounts scan where supported.
	Org bool
	// Account scopes the scan to a profile/subscription/project/tenancy.
	Account string

	// CountStandby includes hot standbys / HA mirrors (e.g. RDS Multi-AZ, Azure
	// flexible-server zone-redundant HA) as billable nodes. KubeDB bills every
	// replica, so this defaults to true for an apples-to-apples comparison.
	CountStandby bool
	// IncludeNonData includes non-data nodes (Elasticsearch dedicated masters /
	// ML, OpenSearch masters/warm) in the memory total.
	IncludeNonData bool

	// DBaaS credentials (used by SourceREST).
	Atlas      AtlasCreds
	Elastic    ElasticCreds
	ClickHouse ClickHouseCreds

	// Timeout bounds each external call (CLI invocation or HTTP request).
	Timeout time.Duration
}

func (o Options) timeout() time.Duration {
	if o.Timeout <= 0 {
		return 60 * time.Second
	}
	return o.Timeout
}

// Discoverer inventories the managed databases of a single provider.
type Discoverer interface {
	Provider() Provider
	// Discover returns the normalized databases plus any non-fatal warnings.
	Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error)
}

// DiscovererFor returns the discoverer for a provider.
//
// To add an official-SDK based discoverer, implement Discoverer in a build that
// vendors the relevant SDK and return it here for SourceSDK; the rest of the
// pipeline (sizing, pricing, reporting) is source agnostic.
func DiscovererFor(p Provider) (Discoverer, error) {
	switch p {
	case ProviderAWS:
		return awsDiscoverer{}, nil
	case ProviderAzure:
		return azureDiscoverer{}, nil
	case ProviderGCP:
		return gcpDiscoverer{}, nil
	case ProviderOCI:
		return ociDiscoverer{}, nil
	case ProviderAtlas:
		return atlasDiscoverer{}, nil
	case ProviderElastic:
		return elasticDiscoverer{}, nil
	case ProviderClickHouse:
		return clickhouseDiscoverer{}, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", p)
	}
}

// errSDKNotBuiltIn is returned when SourceSDK is requested. It points the user
// at the supported live sources.
func errSDKNotBuiltIn(p Provider) error {
	return fmt.Errorf("provider %s: --source=sdk is not built into this binary; use --source=cli, --source=rest, or --source=file (see docs/inspect.md)", p)
}

// collector pairs a logical key with the CLI arguments that produce its JSON
// and the parser that turns that JSON into databases. Hyperscaler discoverers
// share this so the CLI and file paths reuse the exact same parsers.
type collector struct {
	key   string
	args  []string
	parse func(raw []byte, region string, opts Options) ([]ManagedDatabase, []string, error)
}

// runCLIJSON executes a CLI command and returns its stdout. stderr is folded
// into the error to make failures actionable.
func runCLIJSON(ctx context.Context, opts Options, bin string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, opts.timeout())
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", bin, args[0], firstLine(msg))
	}
	return stdout.Bytes(), nil
}

// cliAvailable reports whether the named CLI binary is on PATH.
func cliAvailable(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// loadBundle reads a --from-file JSON document. The file is an object mapping a
// collector key to that command's raw JSON output, for example:
//
//	{
//	  "rds-instances": { "DBInstances": [ ... ] },
//	  "elasticache-replication-groups": { "ReplicationGroups": [ ... ] }
//	}
func loadBundle(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var bundle map[string]json.RawMessage
	if err := json.Unmarshal(data, &bundle); err != nil {
		return nil, fmt.Errorf("parse %s: %w (expected a JSON object keyed by collector, see docs/inspect.md)", path, err)
	}
	return bundle, nil
}

// discoverViaFile runs every collector whose key is present in the bundle.
func discoverViaFile(path string, collectors []collector, opts Options) ([]ManagedDatabase, []string, error) {
	bundle, err := loadBundle(path)
	if err != nil {
		return nil, nil, err
	}
	var (
		out      []ManagedDatabase
		warnings []string
	)
	matched := 0
	for _, c := range collectors {
		raw, ok := bundle[c.key]
		if !ok {
			continue
		}
		matched++
		dbs, warns, err := c.parse(raw, opts.regionHint(), opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", c.key, err))
			continue
		}
		out = append(out, dbs...)
		warnings = append(warnings, warns...)
	}
	if matched == 0 {
		keys := make([]string, 0, len(collectors))
		for _, c := range collectors {
			keys = append(keys, c.key)
		}
		return nil, nil, fmt.Errorf("no known collectors found in %s; expected one of: %v", path, keys)
	}
	return out, warnings, nil
}

func (o Options) regionHint() string {
	if len(o.Regions) == 1 {
		return o.Regions[0]
	}
	return ""
}

// httpJSON performs an HTTP request and decodes a JSON response into out.
func httpJSON(ctx context.Context, opts Options, method, url string, headers map[string]string, body io.Reader, out any) error {
	cctx, cancel := context.WithTimeout(ctx, opts.timeout())
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, method, url, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, url, resp.StatusCode, firstLine(string(data)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

func firstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	if len(s) > 400 {
		return s[:400]
	}
	return s
}
