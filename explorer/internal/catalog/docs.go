package catalog

import (
	"container/list"
	"sync"

	"owstu/stu"
)

// docCache keeps the last n parsed documents. A Document is not safe for concurrent use: each entry
// has its own lock.
type docCache struct {
	mu    sync.Mutex
	n     int
	order *list.List // front = most recent; values *docEntry
	byID  map[uint64]*list.Element
}

type docEntry struct {
	guid uint64
	mu   sync.Mutex
	doc  *stu.Document
}

func newDocCache(n int) *docCache {
	return &docCache{n: n, order: list.New(), byID: map[uint64]*list.Element{}}
}

func (c *docCache) get(guid uint64, load func() (*stu.Document, error)) (*docEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byID[guid]; ok {
		c.order.MoveToFront(el)
		return el.Value.(*docEntry), nil
	}
	d, err := load()
	if err != nil {
		return nil, err
	}
	e := &docEntry{guid: guid, doc: d}
	c.byID[guid] = c.order.PushFront(e)
	for c.order.Len() > c.n {
		el := c.order.Back()
		old := el.Value.(*docEntry)
		c.order.Remove(el)
		delete(c.byID, old.guid)
		go func() { // close once no caller holds it
			old.mu.Lock()
			old.doc.Close()
			old.mu.Unlock()
		}()
	}
	return e, nil
}
