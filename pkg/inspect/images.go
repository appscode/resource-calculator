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

import "strings"

// Many self-hosted databases are deployed not by an operator but by a Helm chart
// or plain workload that pulls a vendor database image. classifyDBImage detects
// the database images distributed by Bitnami, Chainguard and Docker Hardened
// Images, so `compare operators` can size those workloads too. Detection is by
// image reference only (no dependency on any of these projects).

// imageVendorLicensing maps the detected image vendor to its licensing note,
// surfaced in the comparison.
var imageVendorLicensing = map[string]string{
	"Bitnami":               "Free images deprecated Aug 2025; Secure Images are commercial",
	"Bitnami (legacy)":      "Legacy repo, no updates since Aug 2025",
	"Chainguard":            "Commercial (hardened, secure-by-default)",
	"Docker Hardened Image": "Commercial (Docker subscription)",
}

// dbImageNames maps an image's base name to a database engine. Exact match only,
// to avoid counting sidecars such as *-exporter.
var dbImageNames = map[string]string{
	"postgresql": "postgres", "postgres": "postgres", "postgresql-repmgr": "postgres",
	"mysql": "mysql", "mariadb": "mariadb", "mariadb-galera": "mariadb",
	"mongodb": "mongodb", "mongodb-sharded": "mongodb",
	"redis": "redis", "redis-cluster": "redis", "valkey": "redis",
	"memcached": "memcached", "kafka": "kafka", "rabbitmq": "rabbitmq",
	"clickhouse": "clickhouse", "cassandra": "cassandra",
	"elasticsearch": "elasticsearch", "opensearch": "opensearch",
	"zookeeper": "zookeeper", "etcd": "etcd",
	"pgpool": "pgpool", "pgbouncer": "pgbouncer",
}

// classifyDBImage reports the vendor and database engine for a container image
// distributed by Bitnami, Chainguard or Docker Hardened Images. ok is false for
// any other image (including operator and upstream-official images), so the
// workload scan only counts these three image families and never double-counts
// operator-managed pods.
func classifyDBImage(image string) (vendor, engine string, ok bool) {
	ref := strings.ToLower(strings.TrimSpace(image))
	if i := strings.Index(ref, "@"); i >= 0 { // drop digest
		ref = ref[:i]
	}

	switch {
	case strings.Contains(ref, "bitnamilegacy/"):
		vendor = "Bitnami (legacy)"
	case strings.Contains(ref, "bitnami/"):
		vendor = "Bitnami"
	case strings.Contains(ref, "cgr.dev/"):
		vendor = "Chainguard"
	case strings.HasPrefix(ref, "dhi.io/") || strings.Contains(ref, "/dhi.io/"):
		vendor = "Docker Hardened Image"
	default:
		return "", "", false
	}

	eng, ok := imageEngine(ref)
	if !ok {
		return "", "", false
	}
	return vendor, eng, true
}

func imageEngine(ref string) (string, bool) {
	base := imageBase(ref)
	if strings.Contains(base, "exporter") {
		return "", false // metrics sidecar, not the database
	}
	eng, ok := dbImageNames[base]
	return eng, ok
}

// imageBase returns the image's short name (registry, path and tag/digest
// stripped), e.g. "docker.io/bitnami/postgresql:15" -> "postgresql".
func imageBase(image string) string {
	ref := image
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		ref = ref[i+1:]
	}
	if i := strings.Index(ref, ":"); i >= 0 {
		ref = ref[:i]
	}
	return strings.ToLower(ref)
}
