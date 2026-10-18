package mapview

import "owmaps/maps"

// Visual geometry of a map (T064, docs/Explorer.md "3D map mode"): the models the static world draws, with their
// looks and transforms (Maps.md §2: record type 1 model groups, one look per group; type 2 models; type 8 placed
// models), grouped by (model, look).

// VisualBatch is one model + look and every place it is drawn.
type VisualBatch struct {
	Model string `json:"model"`
	Look  string `json:"look,omitempty"`
	// Instances: pos xyz, rot xyzw (quaternion), scale xyz: 10 floats each; Records: the record of each
	Instances []float32 `json:"instances"`
	Records   []int     `json:"records"`
}

// Visual lists the map's model placements. Records whose primary GUID is not a model are skipped.
func (m *Map) Visual() []VisualBatch {
	idx := map[[2]uint64]int{}
	var out []VisualBatch
	add := func(model, look uint64, xf maps.Transform, rec int) {
		k := [2]uint64{model, look}
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			b := VisualBatch{Model: hex(model)}
			if look != 0 {
				b.Look = hex(look)
			}
			out = append(out, b)
		}
		b := &out[i]
		b.Instances = append(b.Instances, xf.Pos[0], xf.Pos[1], xf.Pos[2], xf.Rot[0], xf.Rot[1], xf.Rot[2], xf.Rot[3],
			xf.Scale[0], xf.Scale[1], xf.Scale[2])
		b.Records = append(b.Records, rec)
	}
	for i := range m.M.Records {
		r := &m.M.Records[i]
		if r.Primary.Type() != 0x00C {
			continue
		}
		switch r.Type {
		case 1:
			for _, in := range r.Instances {
				look := uint64(in.Look)
				if in.Look.Type() != 0x01A {
					look = 0
				}
				add(uint64(r.Primary), look, in.Xf, i)
			}
		case 2, 8:
			if r.Xf == nil {
				continue
			}
			look := uint64(r.Secondary)
			if r.Secondary.Type() != 0x01A {
				look = 0
			}
			add(uint64(r.Primary), look, *r.Xf, i)
		}
	}
	return out
}

func hex(g uint64) string {
	const d = "0123456789abcdef"
	var b [16]byte
	for i := 15; i >= 0; i-- {
		b[i] = d[g&15]
		g >>= 4
	}
	return string(b[:])
}
