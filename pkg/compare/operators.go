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
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// operatorDescriptor describes one alternative Kubernetes database operator: the
// custom resource to detect (by group/version/kind) and how to read the CPU,
// memory and replica count out of its spec. The CRDs are matched by GVK only,
// via the controller-runtime client and unstructured objects, so this binary
// takes no build dependency on any of these projects.
type operatorDescriptor struct {
	Operator  string // display name, e.g. "CloudNativePG"
	Engine    string // database engine, e.g. "postgres"
	Licensing string // licensing model shown in the inventory

	Group   string
	Version string
	Kind    string

	// Extract reduces one CR (its unstructured content) to one or more billable
	// components (replicas x resources-per-node). Multiple components cover CRs
	// that declare several pools (Strimzi kafka+zookeeper, Percona replsets, ...).
	Extract func(obj map[string]any) []componentSpec
}

// componentSpec is one pool within a database CR.
type componentSpec struct {
	Component string  // sub-component label, e.g. "kafka", "rs0" ("" for single)
	Replicas  int     // node count for this pool
	CPU       float64 // CPU limit/request per node in cores (0 if not set)
	MemGiB    float64 // memory limit/request per node in GiB (0 if not set)
	Note      string  // warning, e.g. unknown memory
}

// operatorDescriptors is the catalog of alternative operators that `inspect
// operators` looks for. CPU and memory are read from the pod resource limit
// (falling back to the request).
func operatorDescriptors() []operatorDescriptor {
	return []operatorDescriptor{
		// PostgreSQL
		{Operator: "CloudNativePG", Engine: "postgres", Licensing: "Open source (Apache-2.0)", Group: "postgresql.cnpg.io", Version: "v1", Kind: "Cluster", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "resources")
			return []componentSpec{comp("", intAt(o, "spec", "instances"), cpu, mem, ok)}
		}},
		{Operator: "Zalando Postgres Operator", Engine: "postgres", Licensing: "Open source (MIT)", Group: "acid.zalan.do", Version: "v1", Kind: "postgresql", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "resources")
			return []componentSpec{comp("", intAt(o, "spec", "numberOfInstances"), cpu, mem, ok)}
		}},
		{Operator: "StackGres", Engine: "postgres", Licensing: "Open source (AGPL-3.0)", Group: "stackgres.io", Version: "v1", Kind: "SGCluster", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "pods", "resources")
			cs := comp("", intAt(o, "spec", "instances"), cpu, mem, ok)
			if !ok {
				cs.Note = "resources set via SGInstanceProfile (not resolved)"
			}
			return []componentSpec{cs}
		}},
		{Operator: "Percona Operator for PostgreSQL", Engine: "postgres", Licensing: "Open source (Apache-2.0)", Group: "pgv2.percona.com", Version: "v2", Kind: "PerconaPGCluster", Extract: func(o map[string]any) []componentSpec {
			return eachItem(sliceAt(o, "spec", "instances"), "replicas", "name")
		}},
		// MySQL / MariaDB
		{Operator: "Percona XtraDB Cluster Operator", Engine: "mysql", Licensing: "Open source (Apache-2.0)", Group: "pxc.percona.com", Version: "v1", Kind: "PerconaXtraDBCluster", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "pxc", "resources")
			return []componentSpec{comp("pxc", intAt(o, "spec", "pxc", "size"), cpu, mem, ok)}
		}},
		{Operator: "Oracle MySQL Operator", Engine: "mysql", Licensing: "Open source (Apache-2.0)", Group: "mysql.oracle.com", Version: "v2", Kind: "InnoDBCluster", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specFromContainers(sliceAt(o, "spec", "podSpec", "containers"), "mysql")
			return []componentSpec{comp("", intAt(o, "spec", "instances"), cpu, mem, ok)}
		}},
		{Operator: "MOCO", Engine: "mysql", Licensing: "Open source (Apache-2.0)", Group: "moco.cybozu.com", Version: "v1beta2", Kind: "MySQLCluster", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specFromContainers(sliceAt(o, "spec", "podTemplate", "spec", "containers"), "mysqld")
			return []componentSpec{comp("", intAt(o, "spec", "replicas"), cpu, mem, ok)}
		}},
		{Operator: "Bitpoke MySQL Operator", Engine: "mysql", Licensing: "Open source (Apache-2.0)", Group: "mysql.presslabs.org", Version: "v1alpha1", Kind: "MysqlCluster", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "podSpec", "resources")
			if !ok {
				cpu, mem, ok = specAt(o, "spec", "resources")
			}
			return []componentSpec{comp("", intAt(o, "spec", "replicas"), cpu, mem, ok)}
		}},
		{Operator: "mariadb-operator", Engine: "mariadb", Licensing: "Open source (MIT)", Group: "k8s.mariadb.com", Version: "v1alpha1", Kind: "MariaDB", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "resources")
			return []componentSpec{comp("", intAt(o, "spec", "replicas"), cpu, mem, ok)}
		}},
		// MongoDB
		{Operator: "Percona Server for MongoDB Operator", Engine: "mongodb", Licensing: "Open source (Apache-2.0)", Group: "psmdb.percona.com", Version: "v1", Kind: "PerconaServerMongoDB", Extract: func(o map[string]any) []componentSpec {
			return eachItem(sliceAt(o, "spec", "replsets"), "size", "name")
		}},
		{Operator: "MongoDB Community Operator", Engine: "mongodb", Licensing: "Open source (Apache-2.0)", Group: "mongodbcommunity.mongodb.com", Version: "v1", Kind: "MongoDBCommunity", Extract: func(o map[string]any) []componentSpec {
			members := intAt(o, "spec", "members")
			if members == 0 {
				members = 3
			}
			cpu, mem, ok := specFromContainers(sliceAt(o, "spec", "statefulSet", "spec", "template", "spec", "containers"), "mongod")
			return []componentSpec{comp("", members, cpu, mem, ok)}
		}},
		// Redis / Valkey
		{Operator: "Spotahome Redis Operator", Engine: "redis", Licensing: "Open source (Apache-2.0)", Group: "databases.spotahome.com", Version: "v1", Kind: "RedisFailover", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "redis", "resources")
			return []componentSpec{comp("", intAt(o, "spec", "redis", "replicas"), cpu, mem, ok)}
		}},
		{Operator: "OpsTree Redis Operator", Engine: "redis", Licensing: "Open source (Apache-2.0)", Group: "redis.redis.opstreelabs.in", Version: "v1beta2", Kind: "RedisReplication", Extract: func(o map[string]any) []componentSpec {
			size := intAt(o, "spec", "clusterSize")
			if size == 0 {
				size = 3
			}
			cpu, mem, ok := specAt(o, "spec", "kubernetesConfig", "resources")
			return []componentSpec{comp("", size, cpu, mem, ok)}
		}},
		{Operator: "OpsTree Redis Operator", Engine: "redis", Licensing: "Open source (Apache-2.0)", Group: "redis.redis.opstreelabs.in", Version: "v1beta2", Kind: "RedisCluster", Extract: func(o map[string]any) []componentSpec {
			shards := atLeastOne(intAt(o, "spec", "clusterSize"))
			cpu, mem, ok := specAt(o, "spec", "kubernetesConfig", "resources")
			return []componentSpec{comp("leaders+followers", shards*2, cpu, mem, ok)}
		}},
		{Operator: "DragonflyDB Operator", Engine: "redis", Licensing: "Source-available (BSL-1.1)", Group: "dragonflydb.io", Version: "v1alpha1", Kind: "Dragonfly", Extract: func(o map[string]any) []componentSpec {
			r := intAt(o, "spec", "replicas")
			if r == 0 {
				r = 1
			}
			cpu, mem, ok := specAt(o, "spec", "resources")
			return []componentSpec{comp("", r, cpu, mem, ok)}
		}},
		{Operator: "Redis Enterprise Operator", Engine: "redis", Licensing: "Commercial", Group: "app.redislabs.com", Version: "v1", Kind: "RedisEnterpriseCluster", Extract: func(o map[string]any) []componentSpec {
			n := intAt(o, "spec", "nodes")
			if n == 0 {
				n = 3
			}
			cpu, mem, ok := specAt(o, "spec", "redisEnterpriseNodeResources")
			return []componentSpec{comp("", n, cpu, mem, ok)}
		}},
		// Search
		{Operator: "Elastic Cloud on Kubernetes (ECK)", Engine: "elasticsearch", Licensing: "Source-available (Elastic License)", Group: "elasticsearch.k8s.elastic.co", Version: "v1", Kind: "Elasticsearch", Extract: func(o map[string]any) []componentSpec {
			var out []componentSpec
			for _, ns := range sliceAt(o, "spec", "nodeSets") {
				nm, _ := ns.(map[string]any)
				if nm == nil {
					continue
				}
				cpu, mem, ok := specFromContainers(sliceAt(nm, "podTemplate", "spec", "containers"), "elasticsearch")
				out = append(out, comp(strAt(nm, "name"), intAt(nm, "count"), cpu, mem, ok))
			}
			return out
		}},
		{Operator: "OpenSearch Operator", Engine: "opensearch", Licensing: "Open source (Apache-2.0)", Group: "opensearch.opster.io", Version: "v1", Kind: "OpenSearchCluster", Extract: func(o map[string]any) []componentSpec {
			return eachItem(sliceAt(o, "spec", "nodePools"), "replicas", "component")
		}},
		{Operator: "Apache Solr Operator", Engine: "solr", Licensing: "Open source (Apache-2.0)", Group: "solr.apache.org", Version: "v1beta1", Kind: "SolrCloud", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "customSolrKubeOptions", "podOptions", "resources")
			return []componentSpec{comp("", intAt(o, "spec", "replicas"), cpu, mem, ok)}
		}},
		// Streaming / messaging
		{Operator: "Strimzi", Engine: "kafka", Licensing: "Open source (Apache-2.0)", Group: "kafka.strimzi.io", Version: "v1beta2", Kind: "Kafka", Extract: func(o map[string]any) []componentSpec {
			var out []componentSpec
			if kc, km, kok := specAt(o, "spec", "kafka", "resources"); kok || intAt(o, "spec", "kafka", "replicas") > 0 {
				out = append(out, comp("kafka", intAt(o, "spec", "kafka", "replicas"), kc, km, kok))
			}
			if zr := intAt(o, "spec", "zookeeper", "replicas"); zr > 0 {
				zc, zm, zok := specAt(o, "spec", "zookeeper", "resources")
				out = append(out, comp("zookeeper", zr, zc, zm, zok))
			}
			if len(out) == 0 {
				out = append(out, componentSpec{Note: "replicas defined via KafkaNodePool; not counted"})
			}
			return out
		}},
		{Operator: "RabbitMQ Cluster Operator", Engine: "rabbitmq", Licensing: "Open source (MPL-2.0)", Group: "rabbitmq.com", Version: "v1beta1", Kind: "RabbitmqCluster", Extract: func(o map[string]any) []componentSpec {
			r := intAt(o, "spec", "replicas")
			if r == 0 {
				r = 1
			}
			cpu, mem, ok := specAt(o, "spec", "resources")
			return []componentSpec{comp("", r, cpu, mem, ok)}
		}},
		// Analytics
		{Operator: "Altinity ClickHouse Operator", Engine: "clickhouse", Licensing: "Open source (Apache-2.0)", Group: "clickhouse.altinity.com", Version: "v1", Kind: "ClickHouseInstallation", Extract: func(o map[string]any) []componentSpec {
			nodes := 0
			for _, c := range sliceAt(o, "spec", "configuration", "clusters") {
				cm, _ := c.(map[string]any)
				if cm == nil {
					continue
				}
				nodes += atLeastOne(intAt(cm, "layout", "shardsCount")) * atLeastOne(intAt(cm, "layout", "replicasCount"))
			}
			var cpu, mem float64
			ok := false
			for _, pt := range sliceAt(o, "spec", "templates", "podTemplates") {
				ptm, _ := pt.(map[string]any)
				if ptm == nil {
					continue
				}
				if pc, pm, found := specFromContainers(sliceAt(ptm, "spec", "containers"), "clickhouse"); found {
					cpu, mem, ok = pc, pm, true
					break
				}
			}
			return []componentSpec{comp("", atLeastOne(nodes), cpu, mem, ok)}
		}},
		// Cassandra
		{Operator: "cass-operator (K8ssandra)", Engine: "cassandra", Licensing: "Open source (Apache-2.0)", Group: "cassandra.datastax.com", Version: "v1beta1", Kind: "CassandraDatacenter", Extract: func(o map[string]any) []componentSpec {
			cpu, mem, ok := specAt(o, "spec", "resources")
			return []componentSpec{comp("", intAt(o, "spec", "size"), cpu, mem, ok)}
		}},
		{Operator: "Scylla Operator", Engine: "cassandra", Licensing: "Open source (AGPL-3.0)", Group: "scylla.scylladb.com", Version: "v1", Kind: "ScyllaCluster", Extract: func(o map[string]any) []componentSpec {
			return eachItem(sliceAt(o, "spec", "datacenter", "racks"), "members", "name")
		}},
		// Other
		{Operator: "Hazelcast Platform Operator", Engine: "hazelcast", Licensing: "Open source (Apache-2.0)", Group: "hazelcast.com", Version: "v1alpha1", Kind: "Hazelcast", Extract: func(o map[string]any) []componentSpec {
			n := intAt(o, "spec", "clusterSize")
			if n == 0 {
				n = 3
			}
			cpu, mem, ok := specAt(o, "spec", "resources")
			return []componentSpec{comp("", n, cpu, mem, ok)}
		}},
	}
}

// operatorLicensing returns the licensing model for an operator or image-vendor
// display name (the "Service" used in self-hosted reports).
func operatorLicensing(operator string) string {
	for _, d := range operatorDescriptors() {
		if d.Operator == operator {
			return d.Licensing
		}
	}
	if lic, ok := imageVendorLicensing[operator]; ok {
		return lic
	}
	return ""
}

// eachItem extracts one component per item of a slice of CR sub-objects (each a
// map with a replicas-style int field and a resources map), e.g. Percona
// instances/replsets, OpenSearch nodePools, Scylla racks.
func eachItem(items []any, replicaKey, labelKey string) []componentSpec {
	var out []componentSpec
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		cpu, mem, ok := specAt(m, "resources")
		out = append(out, comp(strAt(m, labelKey), intAt(m, replicaKey), cpu, mem, ok))
	}
	return out
}

func comp(label string, replicas int, cpu, memGiB float64, ok bool) componentSpec {
	cs := componentSpec{Component: label, Replicas: atLeastOne(replicas), CPU: cpu, MemGiB: memGiB}
	if !ok {
		cs.Note = "memory limit/request not set on the CR"
	}
	return cs
}

// --- unstructured helpers ---

func intAt(obj map[string]any, path ...string) int {
	v, found, err := unstructured.NestedInt64(obj, path...)
	if err != nil || !found {
		return 0
	}
	return int(v)
}

func strAt(obj map[string]any, path ...string) string {
	v, _, _ := unstructured.NestedString(obj, path...)
	return v
}

func sliceAt(obj map[string]any, path ...string) []any {
	s, found, err := unstructured.NestedSlice(obj, path...)
	if err != nil || !found {
		return nil
	}
	return s
}

// specAt reads a Kubernetes ResourceRequirements map at the given path and
// returns the CPU (cores) and memory (GiB) limit, falling back to the request.
// ok reports whether memory was found.
func specAt(obj map[string]any, path ...string) (cpu, mem float64, ok bool) {
	res, found, err := unstructured.NestedMap(obj, path...)
	if err != nil || !found {
		return 0, 0, false
	}
	cpu = cpuFromRes(res)
	mem, ok = memFromRes(res)
	return cpu, mem, ok
}

func memFromRes(res map[string]any) (float64, bool) {
	for _, kind := range []string{"limits", "requests"} {
		if s, ok, _ := unstructured.NestedString(res, kind, "memory"); ok && s != "" {
			if g, ok := parseMemGiB(s); ok {
				return g, true
			}
		}
	}
	return 0, false
}

func cpuFromRes(res map[string]any) float64 {
	for _, kind := range []string{"limits", "requests"} {
		if s, ok, _ := unstructured.NestedString(res, kind, "cpu"); ok && s != "" {
			if c, ok := parseCPU(s); ok {
				return c
			}
		}
	}
	return 0
}

// specFromContainers returns the CPU and memory of the named container, or of
// the first container that declares a memory limit/request.
func specFromContainers(containers []any, prefer string) (cpu, mem float64, ok bool) {
	var first componentSpec
	haveFirst := false
	for _, c := range containers {
		cm, isMap := c.(map[string]any)
		if !isMap {
			continue
		}
		cc, mm, found := specAt(cm, "resources")
		if !found {
			continue
		}
		if prefer != "" && strAt(cm, "name") == prefer {
			return cc, mm, true
		}
		if !haveFirst {
			first, haveFirst = componentSpec{CPU: cc, MemGiB: mm}, true
		}
	}
	return first.CPU, first.MemGiB, haveFirst
}

func parseMemGiB(s string) (float64, bool) {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, false
	}
	return float64(q.Value()) / (1 << 30), true
}

func parseCPU(s string) (float64, bool) {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		return 0, false
	}
	return float64(q.MilliValue()) / 1000, true
}
