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
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// ClickHouseCreds authenticates to the ClickHouse Cloud API (HTTP Basic with a
// key id / secret pair).
type ClickHouseCreds struct {
	KeyID     string
	KeySecret string
	OrgID     string
	BaseURL   string // defaults to https://api.clickhouse.cloud
}

type clickhouseDiscoverer struct{}

func (clickhouseDiscoverer) Provider() Provider { return ProviderClickHouse }

func (d clickhouseDiscoverer) Discover(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	switch opts.Source {
	case SourceSDK:
		return nil, nil, errSDKNotBuiltIn(ProviderClickHouse)
	case SourceCLI:
		return nil, nil, fmt.Errorf("clickhouse: no CLI source; use --source=rest or --source=file")
	case SourceFile:
		return discoverViaFile(opts.FromFile, clickhouseCollectors(), opts)
	case SourceAuto:
		if opts.FromFile != "" {
			return discoverViaFile(opts.FromFile, clickhouseCollectors(), opts)
		}
		fallthrough
	case SourceREST:
		return d.discoverViaREST(ctx, opts)
	default:
		return nil, nil, fmt.Errorf("clickhouse: unsupported source %q", opts.Source)
	}
}

func clickhouseCollectors() []collector {
	return []collector{{key: "services", parse: parseClickHouseServices}}
}

func (d clickhouseDiscoverer) discoverViaREST(ctx context.Context, opts Options) ([]ManagedDatabase, []string, error) {
	c := opts.ClickHouse
	if c.KeyID == "" || c.KeySecret == "" {
		return nil, nil, fmt.Errorf("clickhouse: --clickhouse-key-id and --clickhouse-key-secret are required for --source=rest (or use --source=file)")
	}
	base := c.BaseURL
	if base == "" {
		base = "https://api.clickhouse.cloud"
	}
	headers := map[string]string{"Authorization": "Basic " + basicAuth(c.KeyID, c.KeySecret)}

	orgIDs := []string{c.OrgID}
	if c.OrgID == "" {
		var orgs struct {
			Result []struct {
				ID string `json:"id"`
			} `json:"result"`
		}
		if err := httpJSON(ctx, opts, "GET", base+"/v1/organizations", headers, nil, &orgs); err != nil {
			return nil, nil, err
		}
		orgIDs = orgIDs[:0]
		for _, o := range orgs.Result {
			orgIDs = append(orgIDs, o.ID)
		}
	}

	var out []ManagedDatabase
	var warnings []string
	for _, org := range orgIDs {
		var resp json.RawMessage
		if err := httpJSON(ctx, opts, "GET", base+"/v1/organizations/"+org+"/services", headers, nil, &resp); err != nil {
			warnings = append(warnings, fmt.Sprintf("clickhouse org %s: %v", org, err))
			continue
		}
		dbs, warns, err := parseClickHouseServices(resp, "", opts)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("clickhouse org %s: %v", org, err))
			continue
		}
		out = append(out, dbs...)
		warnings = append(warnings, warns...)
	}
	return out, warnings, nil
}

type clickhouseServiceList struct {
	Result []clickhouseService `json:"result"`
}

type clickhouseService struct {
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	Region             string `json:"region"`
	State              string `json:"state"`
	NumReplicas        int    `json:"numReplicas"`
	MinReplicaMemoryGb int    `json:"minReplicaMemoryGb"`
	MaxReplicaMemoryGb int    `json:"maxReplicaMemoryGb"`
	ReplicaMemoryGb    int    `json:"replicaMemoryGb"`
	MinReplicas        int    `json:"minReplicas"`
	MaxReplicas        int    `json:"maxReplicas"`
}

// parseClickHouseServices accepts {result:[...]} or a bare array of services.
func parseClickHouseServices(raw []byte, _ string, opts Options) ([]ManagedDatabase, []string, error) {
	var list clickhouseServiceList
	if err := json.Unmarshal(raw, &list); err != nil || list.Result == nil {
		var bare []clickhouseService
		if err2 := json.Unmarshal(raw, &bare); err2 != nil {
			if err != nil {
				return nil, nil, err
			}
			return nil, nil, err2
		}
		list.Result = bare
	}
	var out []ManagedDatabase
	var warnings []string
	for _, s := range list.Result {
		if strings.EqualFold(s.State, "stopped") || strings.EqualFold(s.State, "terminated") {
			continue
		}
		perReplica := float64(s.MaxReplicaMemoryGb)
		replicas := s.NumReplicas
		if perReplica == 0 { // horizontal autoscaling shape
			perReplica = float64(s.ReplicaMemoryGb)
			if replicas == 0 {
				replicas = s.MaxReplicas
			}
		}
		if replicas == 0 {
			replicas = 1
		}
		db := ManagedDatabase{
			Provider:         ProviderClickHouse,
			Service:          "ClickHouse Cloud",
			Engine:           "clickhouse",
			Name:             s.Name,
			Account:          opts.ClickHouse.OrgID,
			Region:           s.Region,
			NodeType:         fmt.Sprintf("%dGiB/replica", int(perReplica)),
			MemoryGiBPerNode: perReplica,
			VCPUPerNode:      perReplica / 4, // ClickHouse Cloud allocates ~1 vCPU per 4 GiB
			NodeCount:        replicas,
		}
		if perReplica <= 0 {
			db.Notes = "memory not reported"
			warnings = append(warnings, fmt.Sprintf("clickhouse %s: memory not reported", s.Name))
		}
		db.MonthlyCostUSD, db.CostEstimated = clickhouseEstimateCost(db)
		out = append(out, db)
	}
	return out, warnings, nil
}

// clickhouseRatePerGiBHour anchors ClickHouse Cloud compute (~$0.20 per 8 GiB
// unit/hour). Estimate; override with actual spend.
const clickhouseRatePerGiBHour = 0.025

func clickhouseEstimateCost(db ManagedDatabase) (float64, bool) {
	if db.MemoryGiBPerNode <= 0 {
		return 0, false
	}
	return db.TotalMemoryGiB() * clickhouseRatePerGiBHour * hoursPerMonth, true
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
