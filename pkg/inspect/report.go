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
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"text/tabwriter"

	"sigs.k8s.io/yaml"
)

// Render writes the report in the requested format: text (default), json or yaml.
func Render(w io.Writer, r *Report, format string) error {
	switch format {
	case "json":
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(data))
		return err
	case "yaml", "yml":
		data, err := yaml.Marshal(r)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(w, string(data))
		return err
	case "", "text":
		return renderText(w, r)
	default:
		return fmt.Errorf("unknown output format %q (want text, json or yaml)", format)
	}
}

func fprintf(w io.Writer, format string, a ...any) { _, _ = fmt.Fprintf(w, format, a...) }

func fprintln(w io.Writer, a ...any) { _, _ = fmt.Fprintln(w, a...) }

func renderText(w io.Writer, r *Report) error {
	fprintln(w)
	fprintf(w, "Database inventory: %s\n", r.Scope)
	fprintf(w, "Generated: %s\n", r.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fprintln(w)

	if len(r.Databases) == 0 {
		fprintln(w, "No databases discovered.")
		renderWarnings(w, r)
		return nil
	}

	const padding = 3
	tw := tabwriter.NewWriter(w, 0, 0, padding, ' ', tabwriter.TabIndent)
	fprintln(tw, "PROVIDER\tSERVICE\tENGINE\tNAME\tREGION\tNODE TYPE\tCPU/NODE\tMEM/NODE\tNODES\tTOTAL CPU\tTOTAL MEM\tEST. $/MO\t")
	for _, d := range r.Databases {
		fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s\t\n",
			d.Provider, d.Service, dash(d.Engine), d.Name, dash(d.Region), dash(d.NodeType),
			vcpu(d.VCPUPerNode), gib(d.MemoryGiBPerNode), d.NodeCount,
			vcpu(d.TotalVCPU()), gib(d.TotalMemoryGiB()), money(d.MonthlyCostUSD))
	}
	fprintf(tw, "TOTAL\t%d dbs\t\t\t\t\t\t\t%d\t%s\t%s\t%s\t\n",
		r.DatabaseCount, r.NodeCount, vcpu(r.TotalVCPU), gib(r.TotalMemoryGiB), money(r.CurrentMonthlyUSD))
	if err := tw.Flush(); err != nil {
		return err
	}

	fprintln(w)
	fprintf(w, "Total: %d databases, %d nodes, %s vCPU, %s GiB memory allocated\n",
		r.DatabaseCount, r.NodeCount, trimFloat(r.TotalVCPU), trimFloat(r.TotalMemoryGiB))

	if r.SelfHosted {
		renderSelfHosted(w, r)
		renderWarnings(w, r)
		return nil
	}

	if r.CostKnown {
		fprintf(w, "Estimated managed spend: %s/mo  (%s/yr)\n",
			money(r.CurrentMonthlyUSD), money(r.CurrentMonthlyUSD*12))
		if r.CostPartial {
			fprintln(w, "  note: some databases had no price and are excluded from the spend total")
		}
	}

	renderWarnings(w, r)
	return nil
}

func renderWarnings(w io.Writer, r *Report) {
	if len(r.Warnings) == 0 {
		return
	}
	fprintln(w)
	fprintf(w, "Warnings (%d):\n", len(r.Warnings))
	for _, msg := range r.Warnings {
		fprintf(w, "  - %s\n", msg)
	}
}

// renderSelfHosted prints the per-operator / per-vendor breakdown for an
// in-cluster inventory.
func renderSelfHosted(w io.Writer, r *Report) {
	type opAgg struct {
		dbs, nodes int
		cpu, mem   float64
	}
	aggs := map[string]*opAgg{}
	var order []string
	for _, d := range r.Databases {
		a := aggs[d.Service]
		if a == nil {
			a = &opAgg{}
			aggs[d.Service] = a
			order = append(order, d.Service)
		}
		a.dbs++
		a.nodes += d.NodeCount
		a.cpu += d.TotalVCPU()
		a.mem += d.TotalMemoryGiB()
	}
	sort.SliceStable(order, func(i, j int) bool { return aggs[order[i]].mem > aggs[order[j]].mem })

	fprintln(w)
	fprintln(w, "By operator / image vendor:")
	const padding = 3
	tw := tabwriter.NewWriter(w, 0, 0, padding, ' ', tabwriter.TabIndent)
	fprintln(tw, "OPERATOR / VENDOR\tDATABASES\tNODES\tTOTAL CPU\tTOTAL MEM\tLICENSING\t")
	for _, op := range order {
		a := aggs[op]
		fprintf(tw, "%s\t%d\t%d\t%s\t%s\t%s\t\n", op, a.dbs, a.nodes, vcpu(a.cpu), gib(a.mem), dash(operatorLicensing(op)))
	}
	_ = tw.Flush()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func gib(v float64) string {
	if v <= 0 {
		return "-"
	}
	return trimFloat(v) + " GiB"
}

func vcpu(v float64) string {
	if v <= 0 {
		return "-"
	}
	return trimFloat(v)
}

func money(v float64) string {
	if v <= 0 {
		return "-"
	}
	return "$" + humanizeUSD(v)
}

// humanizeUSD formats a dollar amount with thousands separators and no cents.
func humanizeUSD(v float64) string {
	s := strconv.FormatFloat(v, 'f', 0, 64)
	n := len(s)
	if n <= 3 {
		return s
	}
	var b []byte
	for i, c := range []byte(s) {
		if i > 0 && (n-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, c)
	}
	return string(b)
}

func trimFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
