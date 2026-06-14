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
	"regexp"
	"strconv"
	"strings"
)

// InstanceSpec is the compute footprint of a single node.
type InstanceSpec struct {
	VCPU      float64
	MemoryGiB float64
}

// ---------------------------------------------------------------------------
// AWS: db.* (RDS/DocDB/Neptune/MemoryDB), cache.* (ElastiCache), *.search
// (OpenSearch). Memory mirrors the underlying EC2 instance family, so a generic
// "size step x family ratio" rule covers the regular families and a small
// override map handles the burstable (t*) and a few irregular classes.
// ---------------------------------------------------------------------------

// awsSizeVCPU maps an instance size to its vCPU count for the regular families
// (large is the 2 vCPU baseline; each step doubles).
var awsSizeVCPU = map[string]float64{
	"large":    2,
	"xlarge":   4,
	"2xlarge":  8,
	"3xlarge":  12,
	"4xlarge":  16,
	"6xlarge":  24,
	"8xlarge":  32,
	"9xlarge":  36,
	"12xlarge": 48,
	"16xlarge": 64,
	"18xlarge": 72,
	"24xlarge": 96,
	"32xlarge": 128,
	"48xlarge": 192,
}

// awsExplicitSpec overrides the generic rule for burstable and irregular sizes
// (normalized key: family.size, prefixes and the .search suffix stripped).
var awsExplicitSpec = map[string]InstanceSpec{
	"t2.micro": {1, 1}, "t2.small": {1, 2}, "t2.medium": {2, 4}, "t2.large": {2, 8}, "t2.xlarge": {4, 16}, "t2.2xlarge": {8, 32},
	"t3.micro": {2, 1}, "t3.small": {2, 2}, "t3.medium": {2, 4}, "t3.large": {2, 8}, "t3.xlarge": {4, 16}, "t3.2xlarge": {8, 32},
	"t4g.micro": {2, 1}, "t4g.small": {2, 2}, "t4g.medium": {2, 4}, "t4g.large": {2, 8}, "t4g.xlarge": {4, 16}, "t4g.2xlarge": {8, 32},
	// x2g extra-high-memory starts at medium (1 vCPU / 16 GiB).
	"x2g.medium": {1, 16}, "x2gd.medium": {1, 16},
	// OpenSearch OR1 family (memory ~ r6g ratios).
	"or1.medium": {1, 8}, "or1.large": {2, 16}, "or1.xlarge": {4, 32}, "or1.2xlarge": {8, 64},
	"or1.4xlarge": {16, 128}, "or1.8xlarge": {32, 256}, "or1.12xlarge": {48, 384}, "or1.16xlarge": {64, 512},
}

// awsFamilyMemPerVCPU returns the GiB-per-vCPU ratio for a family.
func awsFamilyMemPerVCPU(family string) (float64, bool) {
	switch {
	case strings.HasPrefix(family, "x2"):
		return 16, true // x2g/x2gd: large = 2 vCPU / 32 GiB
	case strings.HasPrefix(family, "z1d"):
		return 8, true
	}
	switch family[0] {
	case 'r': // memory optimized
		return 8, true
	case 'm': // general purpose
		return 4, true
	case 'c': // compute optimized
		return 2, true
	case 'z': // high frequency, memory optimized
		return 8, true
	}
	return 0, false
}

var awsTrimPrefixes = []string{"db.", "cache."}

// awsInstanceSpec resolves an AWS instance class to a spec. The boolean is
// false when the family/size is unknown (the caller should warn and treat the
// memory as unknown rather than guessing).
func awsInstanceSpec(nodeType string) (InstanceSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(nodeType))
	for _, p := range awsTrimPrefixes {
		key = strings.TrimPrefix(key, p)
	}
	key = strings.TrimSuffix(key, ".search")

	if s, ok := awsExplicitSpec[key]; ok {
		return s, true
	}
	family, size, ok := strings.Cut(key, ".")
	if !ok {
		return InstanceSpec{}, false
	}
	vcpu, ok := awsSizeVCPU[size]
	if !ok {
		return InstanceSpec{}, false
	}
	ratio, ok := awsFamilyMemPerVCPU(family)
	if !ok {
		return InstanceSpec{}, false
	}
	return InstanceSpec{VCPU: vcpu, MemoryGiB: vcpu * ratio}, true
}

// ---------------------------------------------------------------------------
// Azure
// ---------------------------------------------------------------------------

var azureSKURe = regexp.MustCompile(`(?i)^Standard_([A-Za-z]+?)(\d+)`)

// azureBurstableSpec covers the B-series used by flexible servers.
var azureBurstableSpec = map[string]InstanceSpec{
	"b1ms": {1, 2}, "b1s": {1, 1}, "b2s": {2, 4}, "b2ms": {2, 8},
	"b4ms": {4, 16}, "b8ms": {8, 32}, "b12ms": {12, 48}, "b16ms": {16, 64}, "b20ms": {20, 80},
}

// azureFlexSpec resolves an Azure flexible-server / VM SKU (e.g.
// "Standard_D4ds_v5", "Standard_E8ads_v5", "Standard_B2ms") to a spec.
// D-series general purpose = 4 GiB/vCPU, E-series memory optimized = 8 GiB/vCPU.
func azureFlexSpec(sku string) (InstanceSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(sku))
	key = strings.TrimPrefix(key, "standard_")
	if s, ok := azureBurstableSpec[key]; ok {
		return s, true
	}
	m := azureSKURe.FindStringSubmatch(sku)
	if m == nil {
		return InstanceSpec{}, false
	}
	series := strings.ToUpper(m[1])
	vcpu, err := strconv.Atoi(m[2])
	if err != nil || vcpu <= 0 {
		return InstanceSpec{}, false
	}
	var ratio float64
	switch series[0] {
	case 'E', 'M': // memory optimized
		ratio = 8
	case 'D': // general purpose
		ratio = 4
	case 'F': // compute optimized
		ratio = 2
	default:
		return InstanceSpec{}, false
	}
	return InstanceSpec{VCPU: float64(vcpu), MemoryGiB: float64(vcpu) * ratio}, true
}

// azureRedisGiB returns the cache memory for an Azure Cache for Redis SKU by
// family (C or P) and capacity index.
var azureRedisCFamily = map[int]float64{0: 0.25, 1: 1, 2: 2.5, 3: 6, 4: 13, 5: 26, 6: 53}

var azureRedisPFamily = map[int]float64{1: 6, 2: 13, 3: 26, 4: 53, 5: 120}

func azureRedisGiB(family string, capacity int) (float64, bool) {
	switch strings.ToUpper(strings.TrimSpace(family)) {
	case "C":
		v, ok := azureRedisCFamily[capacity]
		return v, ok
	case "P":
		v, ok := azureRedisPFamily[capacity]
		return v, ok
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// GCP Cloud SQL
// ---------------------------------------------------------------------------

var gcpCustomTierRe = regexp.MustCompile(`^db-(?:custom|perf-optimized-N)-(\d+)-(\d+)$`)

var gcpLegacyTierSpec = map[string]InstanceSpec{
	"db-f1-micro": {1, 0.6},
	"db-g1-small": {1, 1.7},
}

var gcpStandardTierRe = regexp.MustCompile(`^db-n1-(standard|highmem|highcpu)-(\d+)$`)

// gcpCloudSQLSpec resolves a Cloud SQL tier to a spec. Custom tiers encode
// memory directly: db-custom-{vCPU}-{memoryMB}.
func gcpCloudSQLSpec(tier string) (InstanceSpec, bool) {
	key := strings.ToLower(strings.TrimSpace(tier))
	if s, ok := gcpLegacyTierSpec[key]; ok {
		return s, true
	}
	if m := gcpCustomTierRe.FindStringSubmatch(key); m != nil {
		vcpu, _ := strconv.Atoi(m[1])
		memMB, _ := strconv.Atoi(m[2])
		return InstanceSpec{VCPU: float64(vcpu), MemoryGiB: float64(memMB) / 1024}, true
	}
	if m := gcpStandardTierRe.FindStringSubmatch(key); m != nil {
		vcpu, _ := strconv.Atoi(m[2])
		var ratio float64
		switch m[1] {
		case "standard":
			ratio = 3.75
		case "highmem":
			ratio = 6.5
		case "highcpu":
			ratio = 0.9
		}
		return InstanceSpec{VCPU: float64(vcpu), MemoryGiB: float64(vcpu) * ratio}, true
	}
	return InstanceSpec{}, false
}

// gcpAlloyDBMemoryGiB returns AlloyDB memory: a fixed 8 GiB per vCPU.
func gcpAlloyDBMemoryGiB(cpuCount float64) float64 { return cpuCount * 8 }

// ---------------------------------------------------------------------------
// OCI
// ---------------------------------------------------------------------------

// ociMySQLSpec resolves a MySQL HeatWave shape. ECPU shapes (MySQL.N) provision
// 8 GiB/ECPU; legacy OCPU shapes encode memory in the name (...E4.{ocpu}.{mem}GB).
var ociMySQLECPURe = regexp.MustCompile(`(?i)^MySQL\.(\d+)$`)

var ociMySQLLegacyRe = regexp.MustCompile(`(?i)\.(\d+)\.(\d+)GB$`)

func ociMySQLSpec(shape string) (InstanceSpec, bool) {
	s := strings.TrimSpace(shape)
	if strings.EqualFold(s, "MySQL.Free") {
		return InstanceSpec{VCPU: 1, MemoryGiB: 8}, true
	}
	if m := ociMySQLECPURe.FindStringSubmatch(s); m != nil {
		ecpu, _ := strconv.Atoi(m[1])
		return InstanceSpec{VCPU: float64(ecpu), MemoryGiB: float64(ecpu) * 8}, true
	}
	if m := ociMySQLLegacyRe.FindStringSubmatch(s); m != nil {
		ocpu, _ := strconv.Atoi(m[1])
		mem, _ := strconv.Atoi(m[2])
		return InstanceSpec{VCPU: float64(ocpu), MemoryGiB: float64(mem)}, true
	}
	return InstanceSpec{}, false
}

// ociBaseDBFixedShape covers the common fixed VM DB shapes (Standard2 =
// 15 GiB/OCPU). Flex shapes carry memory explicitly and are read from the data.
var ociBaseDBFixedShape = map[string]InstanceSpec{
	"vm.standard2.1": {1, 15}, "vm.standard2.2": {2, 30}, "vm.standard2.4": {4, 60},
	"vm.standard2.8": {8, 120}, "vm.standard2.16": {16, 240}, "vm.standard2.24": {24, 320},
}

func ociBaseDBSpec(shape string) (InstanceSpec, bool) {
	s, ok := ociBaseDBFixedShape[strings.ToLower(strings.TrimSpace(shape))]
	return s, ok
}

// ociAutonomousMemoryGiB approximates Autonomous Database memory from compute
// units. Oracle provisions roughly 8 GiB per ECPU/OCPU (not separately
// configurable); this is an estimate, flagged as such by callers.
func ociAutonomousMemoryGiB(computeCount float64) float64 { return computeCount * 8 }

// ---------------------------------------------------------------------------
// MongoDB Atlas dedicated tiers (RAM is provider-independent; vCPU is the
// standard-class value). R-series mirrors M-series RAM with fewer vCPUs.
// ---------------------------------------------------------------------------

var atlasTierSpec = map[string]InstanceSpec{
	"M10": {2, 2}, "M20": {2, 4}, "M30": {2, 8}, "M40": {4, 16}, "M50": {8, 32},
	"M60": {16, 64}, "M80": {32, 131}, "M140": {48, 192}, "M200": {64, 256},
	"M300": {96, 384}, "M400": {64, 488}, "M700": {96, 768},
	"R40": {2, 16}, "R50": {4, 32}, "R60": {8, 64}, "R80": {16, 122},
	"R200": {32, 256}, "R300": {48, 384}, "R400": {64, 488}, "R700": {96, 768},
	"M40_NVME": {4, 15.25}, "M50_NVME": {8, 30.5}, "M60_NVME": {16, 61},
	"M80_NVME": {32, 122}, "M200_NVME": {64, 244}, "M400_NVME": {64, 512},
}

func atlasSpec(instanceSize string) (InstanceSpec, bool) {
	s, ok := atlasTierSpec[strings.ToUpper(strings.TrimSpace(instanceSize))]
	return s, ok
}
