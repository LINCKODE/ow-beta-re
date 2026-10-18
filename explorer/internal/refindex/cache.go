package refindex

import (
	"bufio"
	"crypto/sha1"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
)

// formatVersion changes when the Index layout or the cache file changes.
const formatVersion = 1

type cacheFile struct {
	Key     string
	Kinds   []KindInfo
	Nodes   []uint64
	OutOff  []uint32
	OutTo   []uint32
	OutMask []uint32
	OutAux  []uint32
	Names   []NameRec
	Stats   []SourceStat
}

// CachePath is where the index for key is cached (os.UserCacheDir()/owexplorer/refindex-<hash>.gob).
func CachePath(key string) string {
	d, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "owexplorer", fmt.Sprintf("refindex-%x.gob", sha1.Sum([]byte(key))))
}

// Save writes the index to path (atomically: temp file + rename).
func (x *Index) Save(path string) error {
	if path == "" {
		return fmt.Errorf("refindex: no cache directory")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	err = gob.NewEncoder(w).Encode(cacheFile{x.Key, x.Kinds, x.Nodes, x.OutOff, x.OutTo, x.OutMask, x.OutAux, x.Names, x.Stats})
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads a cached index; it fails if the file is missing, damaged or was built for another key.
func Load(path, key string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var c cacheFile
	if err := gob.NewDecoder(bufio.NewReaderSize(f, 1<<20)).Decode(&c); err != nil {
		return nil, err
	}
	if c.Key != key {
		return nil, fmt.Errorf("refindex: cache is for %q", c.Key)
	}
	if len(c.OutOff) != len(c.Nodes)+1 || len(c.OutTo) != len(c.OutMask) || len(c.OutTo) != len(c.OutAux) ||
		int(c.OutOff[len(c.Nodes)]) != len(c.OutTo) {
		return nil, fmt.Errorf("refindex: damaged cache")
	}
	for _, t := range c.OutTo {
		if int(t) >= len(c.Nodes) {
			return nil, fmt.Errorf("refindex: damaged cache")
		}
	}
	x := &Index{Key: c.Key, Kinds: c.Kinds, Nodes: c.Nodes, OutOff: c.OutOff, OutTo: c.OutTo, OutMask: c.OutMask,
		OutAux: c.OutAux, Names: c.Names, Stats: c.Stats}
	x.buildIn()
	return x, nil
}
