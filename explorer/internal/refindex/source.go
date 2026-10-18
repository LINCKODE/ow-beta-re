package refindex

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"owcontent/content"
)

// Source adds one format's references (and names) to the index. To plug in a format (T063 textures, T064
// models/materials, T065 audio): implement Source, add it to catalog.Sources(), bump its Version when its
// output changes (that invalidates the cache). Scan runs on Workers() goroutines; it must not keep state
// between calls except through the Emitter (or its own locks).
type Source interface {
	ID() string        // short and stable: "stu", "maps", "packages", …
	Version() int      // part of the cache key
	Kinds() []KindInfo // the kinds it emits; Emitter.Ref takes an index into this slice
	Select(c *Ctx, r content.Resource) bool
	Workers() int // concurrency (memory-heavy formats: 1–2)
	Scan(c *Ctx, r content.Resource, e *Emitter) error
}

// Preparer is an optional Source hook, run once before its scans (e.g. to take the package list).
type Preparer interface {
	Prepare(c *Ctx) error
}

// Ctx is what a source can use while scanning.
type Ctx struct {
	Store *content.Store
	// IsSTU tells whether a resource type holds STUD files (from the catalog's type table).
	IsSTU func(t uint32) bool
	// Name is the best name known so far (repository names + names emitted by earlier sources).
	Name func(g uint64) string
	// TypeName names an STU hash.
	TypeName func(h uint32) (string, bool)
	// Exists: the GUID is a resource of the install.
	Exists func(g uint64) bool
}

// Emitter collects one scan's output.
type Emitter struct {
	base  Kind
	n     int
	edges []rawEdge
	names []NameRec
}

// Ref records "from uses to" with the source's kind k (index into its Kinds()) and an aux value.
func (e *Emitter) Ref(from, to uint64, k int, aux uint32) {
	if from == 0 || to == 0 || from == to || k < 0 || k >= e.n {
		return
	}
	e.edges = append(e.edges, rawEdge{from, to, 1 << (e.base + Kind(k)), aux})
}

// Name records a name found in the content.
func (e *Emitter) Name(g uint64, text, source string, guess bool) {
	if g == 0 || strings.TrimSpace(text) == "" {
		return
	}
	e.names = append(e.names, NameRec{g, text, source, guess})
}

// Progress of a build.
type Progress struct {
	Phase   string  `json:"phase"` // source id, "done", "cached", ""
	Done    int     `json:"done"`
	Total   int     `json:"total"`
	Seconds float64 `json:"seconds"`
	Refs    int     `json:"refs"`
	Named   int     `json:"named"`
	Failed  int     `json:"failed"`
	Cached  bool    `json:"cached"`
}

// Builder runs the sources over an install.
type Builder struct {
	Ctx       *Ctx
	Resources []content.Resource
	Sources   []Source

	mu    sync.Mutex
	prog  Progress
	names map[uint64]NameRec // first name wins (sources run in order)
}

// Key is the cache key: the install's content description, the index format and every source's version.
func Key(describe string, sources []Source) string {
	parts := []string{fmt.Sprintf("refindex v%d", formatVersion), describe}
	for _, s := range sources {
		parts = append(parts, fmt.Sprintf("%s v%d", s.ID(), s.Version()))
	}
	return strings.Join(parts, " | ")
}

func (b *Builder) Progress() Progress {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.prog
}

// Name is a name found so far (for Ctx.Name and the catalog while building).
func (b *Builder) Name(g uint64) (NameRec, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.names[g]
	return n, ok
}

// Build runs every source in order; onPhase is called after each source (names found so far are visible
// through Name).
func (b *Builder) Build(key string, onPhase func()) (*Index, error) {
	t0 := time.Now()
	b.names = map[uint64]NameRec{}
	var kinds []KindInfo
	var edges []rawEdge
	var stats []SourceStat
	for _, s := range b.Sources {
		base := Kind(len(kinds))
		ks := s.Kinds()
		if len(kinds)+len(ks) > 32 {
			return nil, fmt.Errorf("refindex: more than 32 edge kinds (source %s)", s.ID())
		}
		for _, k := range ks {
			k.Source = s.ID()
			kinds = append(kinds, k)
		}
		var sel []content.Resource
		for _, r := range b.Resources {
			if s.Select(b.Ctx, r) {
				sel = append(sel, r)
			}
		}
		b.mu.Lock()
		b.prog.Phase, b.prog.Done, b.prog.Total = s.ID(), 0, len(sel)
		b.mu.Unlock()
		st := SourceStat{ID: s.ID(), Scanned: len(sel)}
		ts := time.Now()
		if p, ok := s.(Preparer); ok {
			if err := p.Prepare(b.Ctx); err != nil {
				return nil, fmt.Errorf("refindex: %s: %w", s.ID(), err)
			}
		}
		ch := make(chan content.Resource, 64)
		var wg sync.WaitGroup
		for range max(1, s.Workers()) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for r := range ch {
					e := &Emitter{base: base, n: len(ks)}
					err := s.Scan(b.Ctx, r, e)
					b.mu.Lock()
					edges = append(edges, e.edges...)
					st.Edges += len(e.edges)
					for _, n := range e.names {
						if _, ok := b.names[n.GUID]; !ok {
							b.names[n.GUID] = n
							st.Names++
							b.prog.Named++
						}
					}
					b.prog.Refs += len(e.edges)
					b.prog.Done++
					if err != nil {
						st.Failed++
						b.prog.Failed++
					}
					b.mu.Unlock()
				}
			}()
		}
		for _, r := range sel {
			ch <- r
		}
		close(ch)
		wg.Wait()
		st.Seconds = time.Since(ts).Seconds()
		stats = append(stats, st)
		b.mu.Lock()
		b.prog.Seconds = time.Since(t0).Seconds()
		b.mu.Unlock()
		if onPhase != nil {
			onPhase()
		}
	}
	names := make([]NameRec, 0, len(b.names))
	b.mu.Lock()
	for _, n := range b.names {
		names = append(names, n)
	}
	b.mu.Unlock()
	sort.Slice(names, func(i, j int) bool { return names[i].GUID < names[j].GUID })
	x := assemble(key, kinds, edges, names, stats)
	b.mu.Lock()
	b.prog.Phase, b.prog.Seconds, b.prog.Refs = "done", time.Since(t0).Seconds(), x.NumEdges()
	b.mu.Unlock()
	return x, nil
}
