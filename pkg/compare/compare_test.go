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

package compare

import (
	"testing"
)

func TestAWSInstanceSpec(t *testing.T) {
	cases := []struct {
		in       string
		wantVCPU float64
		wantMem  float64
		wantOK   bool
	}{
		{"db.r6g.xlarge", 4, 32, true},
		{"db.r6g.large", 2, 16, true},
		{"db.r6g.16xlarge", 64, 512, true},
		{"cache.r7g.large", 2, 16, true},
		{"db.m6i.large", 2, 8, true},
		{"db.m5.4xlarge", 16, 64, true},
		{"db.t3.medium", 2, 4, true},
		{"db.t4g.micro", 2, 1, true},
		{"cache.t4g.medium", 2, 4, true},
		{"db.x2g.medium", 1, 16, true},
		{"r6g.large.search", 2, 16, true},
		{"db.c6g.xlarge", 4, 8, true},
		{"db.bogus.size", 0, 0, false},
	}
	for _, c := range cases {
		got, ok := awsInstanceSpec(c.in)
		if ok != c.wantOK {
			t.Errorf("awsInstanceSpec(%q) ok=%v want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && (got.VCPU != c.wantVCPU || got.MemoryGiB != c.wantMem) {
			t.Errorf("awsInstanceSpec(%q)=%+v want {%g %g}", c.in, got, c.wantVCPU, c.wantMem)
		}
	}
}

func TestAzureFlexSpec(t *testing.T) {
	cases := []struct {
		in      string
		wantMem float64
	}{
		{"Standard_D4ds_v5", 16},
		{"Standard_D2ds_v4", 8},
		{"Standard_E8ads_v5", 64},
		{"Standard_E16ds_v5", 128},
		{"Standard_B2ms", 8},
		{"Standard_B1ms", 2},
	}
	for _, c := range cases {
		got, ok := azureFlexSpec(c.in)
		if !ok || got.MemoryGiB != c.wantMem {
			t.Errorf("azureFlexSpec(%q)=%+v,%v want mem %g", c.in, got, ok, c.wantMem)
		}
	}
}

func TestGCPCloudSQLSpec(t *testing.T) {
	cases := []struct {
		in       string
		wantVCPU float64
		wantMem  float64
	}{
		{"db-custom-4-16384", 4, 16},
		{"db-custom-8-32768", 8, 32},
		{"db-n1-standard-2", 2, 7.5},
		{"db-n1-highmem-4", 4, 26},
		{"db-f1-micro", 1, 0.6},
	}
	for _, c := range cases {
		got, ok := gcpCloudSQLSpec(c.in)
		if !ok || got.VCPU != c.wantVCPU || got.MemoryGiB != c.wantMem {
			t.Errorf("gcpCloudSQLSpec(%q)=%+v,%v want {%g %g}", c.in, got, ok, c.wantVCPU, c.wantMem)
		}
	}
}

func TestAtlasSpec(t *testing.T) {
	for _, c := range []struct {
		in      string
		wantMem float64
	}{{"M30", 8}, {"M50", 32}, {"M10", 2}, {"R80", 122}, {"m40", 16}} {
		got, ok := atlasSpec(c.in)
		if !ok || got.MemoryGiB != c.wantMem {
			t.Errorf("atlasSpec(%q)=%+v,%v want mem %g", c.in, got, ok, c.wantMem)
		}
	}
}

func TestOCIMySQLSpec(t *testing.T) {
	for _, c := range []struct {
		in      string
		wantMem float64
	}{{"MySQL.8", 64}, {"MySQL.2", 16}, {"MySQL.VM.Standard.E4.4.64GB", 64}} {
		got, ok := ociMySQLSpec(c.in)
		if !ok || got.MemoryGiB != c.wantMem {
			t.Errorf("ociMySQLSpec(%q)=%+v,%v want mem %g", c.in, got, ok, c.wantMem)
		}
	}
}

func TestBuildReport(t *testing.T) {
	dbs := []ManagedDatabase{
		{Provider: ProviderAWS, Service: "RDS", Name: "a", VCPUPerNode: 4, MemoryGiBPerNode: 16, NodeCount: 2, MonthlyCostUSD: 400},
		{Provider: ProviderAWS, Service: "RDS", Name: "b", VCPUPerNode: 2, MemoryGiBPerNode: 8, NodeCount: 1, MonthlyCostUSD: 100},
	}
	r := BuildReport("aws", dbs, nil)

	if r.TotalMemoryGiB != 40 { // 16*2 + 8*1
		t.Errorf("total mem = %g want 40", r.TotalMemoryGiB)
	}
	if r.TotalVCPU != 10 { // 4*2 + 2*1
		t.Errorf("total vCPU = %g want 10", r.TotalVCPU)
	}
	if r.NodeCount != 3 {
		t.Errorf("nodes = %d want 3", r.NodeCount)
	}
	if r.CurrentMonthlyUSD != 500 {
		t.Errorf("current = %g want 500", r.CurrentMonthlyUSD)
	}
	if !r.CostKnown {
		t.Errorf("CostKnown should be true")
	}
	// largest footprint should sort first
	if r.Databases[0].Name != "a" {
		t.Errorf("sort: first = %s want a", r.Databases[0].Name)
	}
}

func TestParseAWSRDSInstances(t *testing.T) {
	raw := []byte(`{"DBInstances":[
		{"DBInstanceIdentifier":"pg","DBInstanceClass":"db.r6g.large","Engine":"postgres","MultiAZ":true,"AvailabilityZone":"us-east-1a"},
		{"DBInstanceIdentifier":"sl","DBInstanceClass":"db.serverless","Engine":"aurora-postgresql","DBClusterIdentifier":"c1"},
		{"DBInstanceIdentifier":"my","DBInstanceClass":"db.m6i.large","Engine":"mysql","MultiAZ":false}
	]}`)
	dbs, _, err := parseAWSRDSInstances(raw, "us-east-1", Options{CountStandby: true})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]ManagedDatabase{}
	for _, d := range dbs {
		byName[d.Name] = d
	}
	if d := byName["pg"]; d.NodeCount != 2 || d.TotalMemoryGiB() != 32 { // Multi-AZ standby doubled
		t.Errorf("pg: nodes=%d mem=%g want 2/32", d.NodeCount, d.TotalMemoryGiB())
	}
	if d := byName["pg"]; d.Region != "us-east-1" {
		t.Errorf("pg region=%q want us-east-1", d.Region)
	}
	if d := byName["sl"]; d.MemoryGiBPerNode != 0 || d.Notes == "" {
		t.Errorf("serverless should be excluded from memory: %+v", d)
	}
	if d := byName["my"]; d.NodeCount != 1 || d.TotalMemoryGiB() != 8 {
		t.Errorf("my: nodes=%d mem=%g want 1/8", d.NodeCount, d.TotalMemoryGiB())
	}
}

func TestParseAWSElastiCacheReplicationGroups(t *testing.T) {
	raw := []byte(`{"ReplicationGroups":[{"ReplicationGroupId":"r","CacheNodeType":"cache.r7g.large","MemberClusters":["a","b","c"],"Engine":"redis"}]}`)
	dbs, _, err := parseAWSElastiCacheReplicationGroups(raw, "us-east-1", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].NodeCount != 3 || dbs[0].TotalMemoryGiB() != 48 {
		t.Errorf("elasticache: %+v want 3 nodes / 48 GiB", dbs)
	}
}

func TestParseAzureFlex(t *testing.T) {
	raw := []byte(`[{"name":"pg1","location":"eastus","sku":{"name":"Standard_D4ds_v5","tier":"GeneralPurpose"},"highAvailability":{"mode":"ZoneRedundant"}}]`)
	dbs, _, err := parseAzurePostgres(raw, "", Options{CountStandby: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].NodeCount != 2 || dbs[0].TotalMemoryGiB() != 32 { // 16 GiB * 2 (HA)
		t.Errorf("azure pg: %+v want 2 nodes / 32 GiB", dbs)
	}
}

func TestParseGCPCloudSQL(t *testing.T) {
	raw := []byte(`[{"name":"db1","region":"us-central1","databaseVersion":"POSTGRES_15","settings":{"tier":"db-custom-4-16384","availabilityType":"REGIONAL"}}]`)
	dbs, _, err := parseGCPCloudSQL(raw, "", Options{CountStandby: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].NodeCount != 2 || dbs[0].TotalMemoryGiB() != 32 { // 16 * 2 (REGIONAL HA)
		t.Errorf("gcp cloudsql: %+v want 2 nodes / 32 GiB", dbs)
	}
	if dbs[0].Engine != "postgres" {
		t.Errorf("engine=%q want postgres", dbs[0].Engine)
	}
}

func TestParseOCIMySQL(t *testing.T) {
	raw := []byte(`{"data":[{"display-name":"m1","shape-name":"MySQL.8","is-highly-available":true,"region":"us-ashburn-1"}]}`)
	dbs, _, err := parseOCIMySQL(raw, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].NodeCount != 3 || dbs[0].TotalMemoryGiB() != 192 { // 64 GiB * 3 (HA)
		t.Errorf("oci mysql: %+v want 3 nodes / 192 GiB", dbs)
	}
}

func TestParseAtlasClusters(t *testing.T) {
	raw := []byte(`{"results":[{"name":"c1","replicationSpecs":[{"regionConfigs":[
		{"regionName":"US_EAST_1","providerName":"AWS","electableSpecs":{"instanceSize":"M30","nodeCount":3},"analyticsSpecs":{"instanceSize":"M30","nodeCount":1}}
	]}]}]}`)
	dbs, _, err := parseAtlasClusters(raw, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	// 3 electable + 1 analytics, each M30 = 8 GiB -> 32 GiB across 4 nodes
	if len(dbs) != 1 || dbs[0].NodeCount != 4 || dbs[0].TotalMemoryGiB() != 32 {
		t.Errorf("atlas: %+v want 4 nodes / 32 GiB", dbs)
	}
}

func TestParseElasticDeployment(t *testing.T) {
	raw := []byte(`{"name":"d1","resources":{"elasticsearch":[{"region":"gcp-us","info":{"plan_info":{"current":{"plan":{"cluster_topology":[
		{"zone_count":2,"node_roles":["data_hot"],"size":{"value":4096,"resource":"memory"}},
		{"zone_count":3,"node_roles":["master"],"size":{"value":1024,"resource":"memory"}}
	]}}}}}]}}`)
	// data only
	dbs, err := parseElasticDeployment(raw, Options{IncludeNonData: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 || dbs[0].TotalMemoryGiB() != 8 || dbs[0].NodeCount != 2 { // 4096*2/1024
		t.Errorf("elastic data-only: %+v want 2 nodes / 8 GiB", dbs)
	}
	// include masters
	dbs2, _ := parseElasticDeployment(raw, Options{IncludeNonData: true})
	if dbs2[0].TotalMemoryGiB() != 11 || dbs2[0].NodeCount != 5 { // (4096*2 + 1024*3)/1024
		t.Errorf("elastic incl-nondata: %+v want 5 nodes / 11 GiB", dbs2)
	}
}

func TestParseClickHouseServices(t *testing.T) {
	raw := []byte(`{"result":[
		{"name":"svc1","provider":"aws","region":"us-east-1","state":"running","numReplicas":3,"minReplicaMemoryGb":8,"maxReplicaMemoryGb":16},
		{"name":"dead","state":"stopped","numReplicas":3,"maxReplicaMemoryGb":32}
	]}`)
	dbs, _, err := parseClickHouseServices(raw, "", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 1 { // stopped service skipped
		t.Fatalf("clickhouse: got %d dbs want 1", len(dbs))
	}
	if dbs[0].NodeCount != 3 || dbs[0].TotalMemoryGiB() != 48 { // 16 * 3
		t.Errorf("clickhouse: %+v want 3 nodes / 48 GiB", dbs[0])
	}
}

func descByKind(t *testing.T, operator, kind string) operatorDescriptor {
	t.Helper()
	for _, d := range operatorDescriptors() {
		if d.Operator == operator && d.Kind == kind {
			return d
		}
	}
	t.Fatalf("descriptor not found: %s/%s", operator, kind)
	return operatorDescriptor{}
}

func TestOperatorExtractCNPG(t *testing.T) {
	obj := map[string]any{"spec": map[string]any{
		"instances": int64(3),
		"resources": map[string]any{"limits": map[string]any{"memory": "8Gi"}},
	}}
	cs := descByKind(t, "CloudNativePG", "Cluster").Extract(obj)
	if len(cs) != 1 || cs[0].Replicas != 3 || cs[0].MemGiB != 8 {
		t.Errorf("cnpg: %+v want 1 comp 3x8GiB", cs)
	}
}

func TestOperatorExtractStrimzi(t *testing.T) {
	obj := map[string]any{"spec": map[string]any{
		"kafka": map[string]any{
			"replicas":  int64(3),
			"resources": map[string]any{"limits": map[string]any{"memory": "16Gi"}},
		},
		"zookeeper": map[string]any{
			"replicas":  int64(3),
			"resources": map[string]any{"requests": map[string]any{"memory": "4Gi"}},
		},
	}}
	cs := descByKind(t, "Strimzi", "Kafka").Extract(obj)
	if len(cs) != 2 {
		t.Fatalf("strimzi: got %d comps want 2: %+v", len(cs), cs)
	}
	byComp := map[string]componentSpec{}
	for _, c := range cs {
		byComp[c.Component] = c
	}
	if k := byComp["kafka"]; k.Replicas != 3 || k.MemGiB != 16 {
		t.Errorf("strimzi kafka: %+v want 3x16", k)
	}
	if z := byComp["zookeeper"]; z.Replicas != 3 || z.MemGiB != 4 { // falls back to requests
		t.Errorf("strimzi zookeeper: %+v want 3x4", z)
	}
}

func TestOperatorExtractPSMDB(t *testing.T) {
	obj := map[string]any{"spec": map[string]any{
		"replsets": []any{map[string]any{
			"name": "rs0", "size": int64(3),
			"resources": map[string]any{"limits": map[string]any{"memory": "8Gi"}},
		}},
	}}
	cs := descByKind(t, "Percona Server for MongoDB Operator", "PerconaServerMongoDB").Extract(obj)
	if len(cs) != 1 || cs[0].Component != "rs0" || cs[0].Replicas != 3 || cs[0].MemGiB != 8 {
		t.Errorf("psmdb: %+v want rs0 3x8", cs)
	}
}

func TestOperatorExtractUnknownMemory(t *testing.T) {
	// CR with no resources set -> replica counted, memory unknown with a note.
	obj := map[string]any{"spec": map[string]any{"instances": int64(2)}}
	cs := descByKind(t, "CloudNativePG", "Cluster").Extract(obj)
	if len(cs) != 1 || cs[0].Replicas != 2 || cs[0].MemGiB != 0 || cs[0].Note == "" {
		t.Errorf("cnpg no-resources: %+v want 2 nodes, 0 mem, note", cs)
	}
}

func TestClassifyDBImage(t *testing.T) {
	cases := []struct {
		image, vendor, engine string
		ok                    bool
	}{
		{"docker.io/bitnami/postgresql:15.4.0", "Bitnami", "postgres", true},
		{"bitnami/redis:7.2", "Bitnami", "redis", true},
		{"bitnami/mariadb-galera:11", "Bitnami", "mariadb", true},
		{"bitnamilegacy/mongodb:6.0", "Bitnami (legacy)", "mongodb", true},
		{"cgr.dev/chainguard/postgres:latest", "Chainguard", "postgres", true},
		{"cgr.dev/acme.example/redis:7", "Chainguard", "redis", true},
		{"dhi.io/mysql:8.4-debian13", "Docker Hardened Image", "mysql", true},
		{"docker.io/library/postgres:16", "", "", false},        // upstream official, not targeted
		{"quay.io/cloudnative-pg/postgresql:16", "", "", false}, // operator image, not targeted
		{"bitnami/redis-exporter:1.50.0", "", "", false},        // metrics sidecar excluded
		{"bitnami/os-shell:12", "", "", false},                  // not a database image
	}
	for _, c := range cases {
		v, e, ok := classifyDBImage(c.image)
		if ok != c.ok || (ok && (v != c.vendor || e != c.engine)) {
			t.Errorf("classifyDBImage(%q) = %q,%q,%v want %q,%q,%v", c.image, v, e, ok, c.vendor, c.engine, c.ok)
		}
	}
}
