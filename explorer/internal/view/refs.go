package view

import (
	"fmt"

	"owstu/stu"
)

// RefPaths lists where a document references target: "instance 3 STUFoo.m_bar[2].m_baz" (at most limit).
func RefPaths(d *stu.Document, target uint64, r Resolver, limit int) []string {
	var out []string
	var walk func(v stu.Value, path string, depth int)
	walk = func(v stu.Value, path string, depth int) {
		if len(out) >= limit || depth > 24 {
			return
		}
		info := v.Info()
		switch info.Type {
		case stu.Ref:
			if info.GUID == target {
				out = append(out, path)
			}
		case stu.Inline:
			for _, f := range v.Items() {
				walk(f, path+"."+fieldLabel(r, f.Info().FieldHash), depth+1)
			}
		case stu.List, stu.Map:
			if info.Kind == stu.KindPrimitiveList || info.Kind == stu.KindEnumList {
				return
			}
			for k := range info.Count {
				walk(v.Index(k), fmt.Sprintf("%s[%d]", path, k), depth+1)
			}
		}
	}
	for i := range d.NumInstances() {
		name, _ := r.TypeName(instType(d, i))
		walk(d.Decode(i), fmt.Sprintf("instance %d %s", i, name), 0)
	}
	return out
}
