package model

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
)

// ExportMaterial is a material slot as exported: a name and its colour texture (PNG, optional).
type ExportMaterial struct {
	Name      string
	ColorName string // the texture GUID, for file names
	ColorPNG  []byte
}

// GLB writes a binary glTF 2.0: one mesh with a primitive per group (positions, normals, uv 0, indices), a material
// per slot with the colour texture embedded. The game's axes are kept (Y up).
func GLB(me *Mesh, mats []ExportMaterial) ([]byte, error) {
	var bin bytes.Buffer
	type view struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
		Target     int `json:"target,omitempty"`
	}
	type accessor struct {
		BufferView    int       `json:"bufferView"`
		ComponentType int       `json:"componentType"`
		Count         int       `json:"count"`
		Type          string    `json:"type"`
		Min           []float32 `json:"min,omitempty"`
		Max           []float32 `json:"max,omitempty"`
	}
	var views []view
	var accs []accessor
	add := func(data any, target int) int {
		for bin.Len()%4 != 0 {
			bin.WriteByte(0)
		}
		off := bin.Len()
		binary.Write(&bin, binary.LittleEndian, data)
		views = append(views, view{0, off, bin.Len() - off, target})
		return len(views) - 1
	}
	nv := len(me.Positions) / 3
	if nv == 0 {
		return nil, fmt.Errorf("empty mesh")
	}
	pos := add(me.Positions, 34962)
	accs = append(accs, accessor{pos, 5126, nv, "VEC3", me.Min[:], me.Max[:]})
	nrm := add(me.Normals, 34962)
	accs = append(accs, accessor{BufferView: nrm, ComponentType: 5126, Count: nv, Type: "VEC3"})
	uv := add(me.UVs, 34962)
	accs = append(accs, accessor{BufferView: uv, ComponentType: 5126, Count: nv, Type: "VEC2"})
	type prim struct {
		Attributes map[string]int `json:"attributes"`
		Indices    int            `json:"indices"`
		Material   *int           `json:"material,omitempty"`
	}
	var prims []prim
	used := map[int]bool{}
	for _, g := range me.Groups {
		iv := add(me.Indices[g.Index:g.Index+g.Count], 34963)
		accs = append(accs, accessor{BufferView: iv, ComponentType: 5125, Count: g.Count, Type: "SCALAR"})
		p := prim{Attributes: map[string]int{"POSITION": 0, "NORMAL": 1, "TEXCOORD_0": 2}, Indices: len(accs) - 1}
		if g.Material < len(mats) {
			m := g.Material
			p.Material = &m
			used[m] = true
		}
		prims = append(prims, p)
	}
	type texInfo struct {
		Index int `json:"index"`
	}
	type pbr struct {
		BaseColorTexture *texInfo  `json:"baseColorTexture,omitempty"`
		BaseColorFactor  []float32 `json:"baseColorFactor,omitempty"`
		MetallicFactor   float32   `json:"metallicFactor"`
		RoughnessFactor  float32   `json:"roughnessFactor"`
	}
	type material struct {
		Name string `json:"name"`
		PBR  pbr    `json:"pbrMetallicRoughness"`
	}
	type image struct {
		BufferView int    `json:"bufferView"`
		MimeType   string `json:"mimeType"`
		Name       string `json:"name,omitempty"`
	}
	type tex struct {
		Source  int `json:"source"`
		Sampler int `json:"sampler"`
	}
	var gm []material
	var images []image
	var texs []tex
	imgOf := map[string]int{}
	for _, m := range mats {
		mt := material{Name: m.Name, PBR: pbr{MetallicFactor: 0, RoughnessFactor: 0.8}}
		if len(m.ColorPNG) > 0 {
			i, ok := imgOf[m.ColorName]
			if !ok {
				v := add(m.ColorPNG, 0)
				images = append(images, image{v, "image/png", m.ColorName})
				texs = append(texs, tex{len(images) - 1, 0})
				i = len(texs) - 1
				imgOf[m.ColorName] = i
			}
			mt.PBR.BaseColorTexture = &texInfo{i}
		} else {
			mt.PBR.BaseColorFactor = []float32{0.7, 0.7, 0.7, 1}
		}
		gm = append(gm, mt)
	}
	for bin.Len()%4 != 0 {
		bin.WriteByte(0)
	}
	doc := map[string]any{
		"asset":       map[string]any{"version": "2.0", "generator": "owexplorer (T064)"},
		"scene":       0,
		"scenes":      []any{map[string]any{"nodes": []int{0}}},
		"nodes":       []any{map[string]any{"mesh": 0}},
		"meshes":      []any{map[string]any{"primitives": prims}},
		"buffers":     []any{map[string]any{"byteLength": bin.Len()}},
		"bufferViews": views,
		"accessors":   accs,
	}
	if len(gm) > 0 {
		doc["materials"] = gm
	}
	if len(images) > 0 {
		doc["images"] = images
		doc["textures"] = texs
		doc["samplers"] = []any{map[string]any{"wrapS": 10497, "wrapT": 10497}}
	}
	js, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	for len(js)%4 != 0 {
		js = append(js, ' ')
	}
	var out bytes.Buffer
	le := binary.LittleEndian
	binary.Write(&out, le, [3]uint32{0x46546C67, 2, uint32(12 + 8 + len(js) + 8 + bin.Len())})
	binary.Write(&out, le, [2]uint32{uint32(len(js)), 0x4E4F534A})
	out.Write(js)
	binary.Write(&out, le, [2]uint32{uint32(bin.Len()), 0x004E4942})
	out.Write(bin.Bytes())
	return out.Bytes(), nil
}

// OBJ writes a Wavefront OBJ + MTL; files are the PNGs the MTL names (name → bytes). V is flipped for OBJ's
// bottom-left origin.
func OBJ(me *Mesh, mats []ExportMaterial, base string) (obj, mtl string, files map[string][]byte) {
	var o, m strings.Builder
	files = map[string][]byte{}
	fmt.Fprintf(&o, "# owexplorer (T064)\nmtllib %s.mtl\n", base)
	f := func(x float32) string { return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", x), "0"), ".") }
	for i := 0; i < len(me.Positions); i += 3 {
		fmt.Fprintf(&o, "v %s %s %s\n", f(me.Positions[i]), f(me.Positions[i+1]), f(me.Positions[i+2]))
	}
	for i := 0; i < len(me.Normals); i += 3 {
		fmt.Fprintf(&o, "vn %s %s %s\n", f(me.Normals[i]), f(me.Normals[i+1]), f(me.Normals[i+2]))
	}
	for i := 0; i < len(me.UVs); i += 2 {
		fmt.Fprintf(&o, "vt %s %s\n", f(me.UVs[i]), f(1-me.UVs[i+1]))
	}
	for _, mt := range mats {
		fmt.Fprintf(&m, "newmtl %s\nKd 0.8 0.8 0.8\n", mt.Name)
		if len(mt.ColorPNG) > 0 {
			n := fmt.Sprintf("%s_%s.png", base, mt.ColorName)
			files[n] = mt.ColorPNG
			fmt.Fprintf(&m, "map_Kd %s\n", n)
		}
		m.WriteString("\n")
	}
	for _, g := range me.Groups {
		fmt.Fprintf(&o, "g submesh%d\n", g.Submesh)
		if g.Material < len(mats) {
			fmt.Fprintf(&o, "usemtl %s\n", mats[g.Material].Name)
		}
		for k := g.Index; k+2 < g.Index+g.Count; k += 3 {
			a, b, c := me.Indices[k]+1, me.Indices[k+1]+1, me.Indices[k+2]+1
			fmt.Fprintf(&o, "f %d/%d/%d %d/%d/%d %d/%d/%d\n", a, a, a, b, b, b, c, c, c)
		}
	}
	return o.String(), m.String(), files
}
