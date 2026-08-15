package bench

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/mayberuk/recall/internal/schema"
)

// Machine is what a reader needs to know before comparing one run's wall clock
// with another's. Allocation counts survive a change of machine; nanoseconds do
// not, and a table without this header invites the comparison anyway.
type Machine struct {
	CPU   string
	Cores int
	OS    string
	Arch  string
	Go    string
}

// Facts reads the machine this run happened on.
func Facts() Machine {
	return Machine{
		CPU:   cpuModel(),
		Cores: runtime.NumCPU(),
		OS:    runtime.GOOS,
		Arch:  runtime.GOARCH,
		Go:    runtime.Version(),
	}
}

func (m Machine) String() string {
	return fmt.Sprintf("%s · %d cores · %s/%s · %s", m.CPU, m.Cores, m.OS, m.Arch, m.Go)
}

// cpuModel names the processor. There is no portable way to ask, so each
// platform is asked in its own words and an unknown one says so rather than
// guessing: a wrong CPU in the header is worse than a missing one.
func cpuModel() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		if model, ok := procCPUModel("/proc/cpuinfo"); ok {
			return model
		}
	}
	return "unknown CPU"
}

func procCPUModel(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "model name", "Model":
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// TierFacts is one tier's share of a corpus after stripping.
type TierFacts struct {
	Tier  schema.Tier
	Turns int
	Bytes int64
}

// CorpusFacts describes what a size actually contains. It is in the report
// because the tier split is what the all-tier numbers mean: a corpus that is
// almost all conversation makes an all-tier search look like a conversation
// one, and a reader comparing these figures with a real session store needs to
// see that rather than infer it.
type CorpusFacts struct {
	Size      Size
	Files     int
	DiskBytes int64
	Turns     int
	Tiers     []TierFacts
}

// GateResult is one threshold and what this run measured against it.
type GateResult struct {
	Gate     Gate
	Size     Size
	Measured time.Duration
	Detail   string
}

// Breached reports whether the measurement is over the limit. A breach is a
// failure: the architecture has no index because scanning measured fast enough,
// so the measurement is what the design rests on.
func (r GateResult) Breached() bool { return r.Measured > r.Gate.Limit }

// Report is a whole measurement run, in the order RESULTS.md prints it.
type Report struct {
	Machine   Machine
	Measured  time.Time
	Corpora   []CorpusFacts
	Micro     []Sample
	Scenarios []Measurement
	Gates     []GateResult
}

// Validate rejects a report with a hole in it. A blank cell in a benchmark
// table reads as "this is not worth knowing" when what it means is that a
// measurement did not run, so the report refuses to render rather than print
// one.
func (r Report) Validate() error {
	var holes []string
	if r.Machine.CPU == "" || r.Machine.Go == "" {
		holes = append(holes, "the machine header is incomplete")
	}
	if len(r.Corpora) == 0 {
		holes = append(holes, "no corpus was described")
	}
	if len(r.Micro) == 0 {
		holes = append(holes, "no micro benchmarks were measured")
	}
	if len(r.Scenarios) == 0 {
		holes = append(holes, "no scenarios were measured")
	}
	if len(r.Gates) == 0 {
		holes = append(holes, "no gates were measured")
	}
	for _, c := range r.Corpora {
		// Every tier gets a column whether or not the generator filled it, so a
		// corpus described with fewer would print a short row against a full
		// header — which reads as a missing measurement rather than an empty tier.
		if c.Files == 0 || c.Turns == 0 || c.DiskBytes == 0 || len(c.Tiers) != 3 {
			holes = append(holes, "the "+string(c.Size)+" corpus is not described")
		}
	}
	for _, s := range r.Micro {
		if s.Size == "" {
			holes = append(holes, "micro benchmark "+s.Name+" names no corpus size")
		}
	}
	for _, m := range r.Scenarios {
		switch {
		case m.Size == "":
			holes = append(holes, "scenario "+m.Name+" names no corpus size")
		case m.Wall == 0:
			holes = append(holes, "scenario "+m.Name+" has no wall clock")
		case m.PeakBytes == 0:
			holes = append(holes, "scenario "+m.Name+" has no peak memory")
		}
	}
	for _, g := range r.Gates {
		if g.Measured == 0 {
			holes = append(holes, "gate "+g.Gate.Name+" was not measured")
		}
	}
	if len(holes) > 0 {
		return fmt.Errorf("bench: the report has empty cells: %s", strings.Join(holes, "; "))
	}
	return nil
}

// Markdown renders RESULTS.md. Every cell is filled: a scenario that could not
// run is an error the caller raised before reaching here, not a blank to print.
func (r Report) Markdown() string {
	var b strings.Builder
	b.WriteString("# recall benchmark results\n\n")
	fmt.Fprintf(&b, "%s  \nmeasured %s\n\n", r.Machine, r.Measured.UTC().Format(time.RFC3339))
	b.WriteString("Every number here comes from a corpus generated from a seed (`internal/corpusgen`),\n")
	b.WriteString("never from a session store, so a run on another machine measures the same bytes.\n")
	b.WriteString("Small is about 5 MB and Medium about 50 MB of transcript.\n\n")

	b.WriteString("## The corpus these numbers came from\n\n")
	b.WriteString("| corpus | files | on disk | turns | conversation | invocation | result |\n")
	b.WriteString("| --- | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, c := range r.Corpora {
		fmt.Fprintf(&b, "| %s | %d | %s | %s |", c.Size, c.Files, mib(c.DiskBytes), commas(float64(c.Turns)))
		for _, t := range c.Tiers {
			fmt.Fprintf(&b, " %s turns / %s |", commas(float64(t.Turns)), mib(t.Bytes))
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\nThe generator reproduces the tier shape of a working session store: %s.\n",
		CorpusShape(r.Corpora))
	b.WriteString("Tool output is most of a real store and none of what recall searches by default,\n")
	b.WriteString("so an all-tier number below costs several times its conversation-tier neighbour,\n")
	b.WriteString("as it does on a real machine.\n\n")
	b.WriteString("The generator is denser than a real store, though: about 72% of this corpus's\n")
	b.WriteString("on-disk bytes strip into text, where a real store measured at 1,402 MB of JSONL\n")
	b.WriteString("yielded only 244 MB, roughly 17%. The tier ratio above still holds, so the\n")
	b.WriteString("all-tier-versus-conversation-tier comparison is sound — but an absolute figure\n")
	b.WriteString("like a cold-strip time does not transfer directly to a real machine, because\n")
	b.WriteString("50 MB of this corpus holds several times the searchable text that 50 MB of a\n")
	b.WriteString("real store does.\n\n")

	b.WriteString("## Micro benchmarks\n\n")
	b.WriteString("| benchmark | corpus | ns/op | B/op | allocs/op |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: |\n")
	for _, s := range r.Micro {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
			s.Name, s.Size, commas(s.Ns), commas(s.Bytes), commas(s.Allocs))
	}

	b.WriteString("\n## Scenarios\n\n")
	b.WriteString("The built binary, invoked end to end. Allocation figures belong to the micro\n")
	b.WriteString("table above: a scenario measures another process, where the comparable costs\n")
	b.WriteString("are wall clock, the size of the answer, and the memory the process reached.\n\n")
	b.WriteString("| scenario | corpus | wall clock | output bytes | peak RSS |\n")
	b.WriteString("| --- | --- | ---: | ---: | ---: |\n")
	for _, m := range r.Scenarios {
		fmt.Fprintf(&b, "| `recall %s` | %s | %s | %s | %s |\n",
			m.Name, m.Size, millis(m.Wall), commas(float64(m.OutBytes)), mib(m.PeakBytes))
	}

	b.WriteString("\n## Gates\n\n")
	b.WriteString("| gate | corpus | limit | measured | verdict |\n")
	b.WriteString("| --- | --- | ---: | ---: | --- |\n")
	for _, g := range r.Gates {
		verdict := "within"
		if g.Breached() {
			verdict = "**BREACHED**"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			g.Gate.Name, g.Size, millis(g.Gate.Limit), millis(g.Measured), verdict)
	}
	return b.String()
}

func millis(d time.Duration) string {
	return fmt.Sprintf("%.1f ms", float64(d.Microseconds())/1000)
}

func mib(bytes int64) string {
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
}

// commas groups a figure in threes. A benchmark table is read by eye, and
// 1250518 and 125051 are indistinguishable at a glance where 1,250,518 and
// 125,051 are not.
func commas(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}
