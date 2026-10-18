package catalog

import (
	"log"
	"time"

	"owexplorer/internal/audio"
	"owexplorer/internal/model"
	"owexplorer/internal/names"
	"owexplorer/internal/refindex"
	"owexplorer/internal/texture"
)

// Progress of the background reference index.
type Progress = refindex.Progress

// Sources are the reference index's sources (refindex.Builtin; T063–T065 append theirs here).
func Sources() []refindex.Source {
	return append(refindex.Builtin(), texture.Source{}, model.Source{}, &audio.Source{})
}

// StartIndex loads the reference index from the disk cache, or builds it in the background (then caches it).
// Names found in the content (string assets, name fields) come with it. onChange is called when names or
// references change (after each source, and at the end).
func (c *Catalog) StartIndex(onChange func()) {
	go func() {
		t0 := time.Now()
		srcs := Sources()
		key := refindex.Key(c.Describe, srcs)
		path := c.IndexCache
		if path == "" {
			path = refindex.CachePath(key)
		}
		if x, err := refindex.Load(path, key); err == nil {
			c.setIndex(x, refindex.Progress{Phase: "done", Cached: true, Refs: x.NumEdges(), Named: len(x.Names),
				Seconds: time.Since(t0).Seconds(), Done: 1, Total: 1})
			onChange()
			return
		}
		b := &refindex.Builder{Resources: c.res, Sources: srcs}
		b.Ctx = &refindex.Ctx{
			Store:    c.Store,
			IsSTU:    func(t uint32) bool { i, ok := c.typeIdx[t]; return ok && c.types[i].STU },
			Name:     func(g uint64) string { return c.Name(g).Text },
			TypeName: c.Book.TypeName,
			Exists:   c.Exists,
		}
		c.mu.Lock()
		c.building = b
		c.mu.Unlock()
		x, err := b.Build(key, func() { c.invalidate(); onChange() })
		if err != nil {
			log.Print(err)
			c.mu.Lock()
			c.building = nil
			c.index = refindex.Progress{Phase: "failed"}
			c.mu.Unlock()
			onChange()
			return
		}
		if err := x.Save(path); err != nil {
			log.Printf("refindex: cache not saved: %v", err)
		}
		c.setIndex(x, b.Progress())
		onChange()
	}()
}

func (c *Catalog) setIndex(x *refindex.Index, p refindex.Progress) {
	derived := make(map[uint64]names.Name, len(x.Names))
	for _, n := range x.Names {
		derived[n.GUID] = names.Name{Text: n.Text, Source: n.Source, Guess: n.Guess}
	}
	c.mu.Lock()
	c.idx, c.derived, c.building, c.index = x, derived, nil, p
	c.mu.Unlock()
	c.invalidate()
}

// Index is the reference index (nil until loaded or built).
func (c *Catalog) Index() *refindex.Index {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.idx
}
