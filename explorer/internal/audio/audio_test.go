package audio

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"owcontent/content"
	"owexplorer/internal/names"
	"owexplorer/internal/refindex"
)

func install(t *testing.T) (*content.Store, string) {
	t.Helper()
	dir := os.Getenv("OW_INSTALL")
	if dir == "" {
		dir = filepath.Join(names.FindRepo("."), "game", "owbetaclient")
	}
	if _, err := os.Stat(filepath.Join(dir, ".build.info")); err != nil {
		t.Skip("no install at", dir)
	}
	s, err := content.Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, dir
}

var (
	libOnce sync.Once
	libVal  *Codebooks
	libErr  error
)

func library(t *testing.T, dir string) *Codebooks {
	libOnce.Do(func() { libVal, libErr = LoadCodebooks(filepath.Join(dir, "GameClientApp.exe")) })
	if libErr != nil {
		t.Fatal(libErr)
	}
	return libVal
}

func TestWwiseID(t *testing.T) {
	if got := WwiseID("WEAPON_TAIL"); got != 0xd237b686 {
		t.Errorf("WwiseID = %08x", got)
	}
}

// Every WEM of the install (0x03F, 0x0B2) converts and decodes to exactly its header's sample count, with finite
// samples. (The comparison with ffmpeg is cmd/audioscan -all -ffmpeg: every WEM matched to 5.4e-7.)
func TestAllWEMs(t *testing.T) {
	s, dir := install(t)
	lib := library(t, dir)
	if len(lib.books) != libraryCount {
		t.Fatalf("%d codebooks", len(lib.books))
	}
	var rs []content.Resource
	rs = append(rs, s.ResourcesOfType(TypeWEM)...)
	rs = append(rs, s.ResourcesOfType(TypeWEMStream)...)
	if len(rs) != 11703+6904 {
		t.Errorf("%d WEMs", len(rs))
	}
	ch := make(chan content.Resource)
	var mu sync.Mutex
	var failed []string
	var wg sync.WaitGroup
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range ch {
				err := func() error {
					b, err := s.Read(r.GUID)
					if err != nil {
						return err
					}
					v, err := Convert(b, lib)
					if err != nil {
						return err
					}
					pcm, err := v.Decode()
					if err != nil {
						return err
					}
					if len(pcm) != v.Header.Channels || len(pcm[0]) != v.Header.Samples {
						return errorf("decoded %d × %d, header %d × %d", len(pcm), len(pcm[0]), v.Header.Channels, v.Header.Samples)
					}
					for _, c := range pcm {
						for _, x := range c {
							if x != x || x > 4 || x < -4 {
								return errorf("sample %g", x)
							}
						}
					}
					return nil
				}()
				if err != nil {
					mu.Lock()
					failed = append(failed, fmt.Sprintf("%016x: %v", r.GUID, err))
					mu.Unlock()
				}
			}
		}()
	}
	for _, r := range rs {
		ch <- r
	}
	close(ch)
	wg.Wait()
	for i, f := range failed {
		if i < 20 {
			t.Error(f)
		}
	}
	if len(failed) > 0 {
		t.Errorf("%d of %d WEMs failed", len(failed), len(rs))
	}
}

// Every bank parses (every HIRC object inside its chunk), every audio node's parent resolves in its bank, every
// event's actions are Actions of the bank; the index source matches every Vorbis Sound object to a WEM of its STUSound.
func TestBanksAndSource(t *testing.T) {
	s, _ := install(t)
	banks := s.ResourcesOfType(TypeBank)
	if len(banks) != 3821 {
		t.Errorf("%d banks", len(banks))
	}
	parents := 0
	for _, r := range banks {
		b, err := s.Read(r.GUID)
		if err != nil {
			t.Fatal(err)
		}
		bk, err := ParseBank(b)
		if err != nil {
			t.Fatalf("%016x: %v", r.GUID, err)
		}
		ids := map[uint32]uint8{}
		for _, o := range bk.Objects {
			ids[o.ID] = o.Type
		}
		for i := range bk.Objects {
			o := &bk.Objects[i]
			if p, ok := o.Parent(); ok {
				parents++
				if _, in := ids[p]; p != 0 && !in {
					t.Errorf("%016x object %08x (type %d): parent %08x not in the bank", r.GUID, o.ID, o.Type, p)
				}
			} else if o.Type == 2 || o.Type == 5 || o.Type == 6 || o.Type == 7 || o.Type == 9 {
				t.Errorf("%016x object %08x: no parent", r.GUID, o.ID)
			}
			if o.Type == 4 {
				acts, ok := o.Actions()
				if !ok {
					t.Errorf("%016x event %08x: bad action list", r.GUID, o.ID)
				}
				for _, a := range acts {
					if ids[a] != 3 {
						t.Errorf("%016x event %08x: action %08x not in the bank", r.GUID, o.ID, a)
					}
				}
			}
		}
	}
	t.Logf("%d banks, %d parents checked", len(banks), parents)

	src := &Source{}
	ctx := &refindex.Ctx{Store: s}
	if err := src.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range s.Resources() {
		if src.Select(ctx, r) {
			if err := src.Scan(ctx, r, &refindex.Emitter{}); err != nil {
				t.Errorf("%016x: %v", r.GUID, err)
			}
		}
	}
	st := src.Stats
	t.Logf("%+v", st)
	// 3,821 sounds, one without a bank (0x0D40000000000F49); 3,821 banks: the other one is the init bank (STMG).
	if st.Sounds != 3821 || st.Banks != 3820 || st.Unmatched != 0 || st.Matched != 52030 {
		t.Errorf("source stats %+v", st)
	}
}
