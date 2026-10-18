// stutree prints STU documents as indented trees (debug helper).
package main

import (
	"flag"
	"fmt"
	"strings"

	"owexplorer/internal/catalog"
	"owexplorer/internal/view"
	"owstu/stu"
)

func main() {
	install := flag.String("install", "../game/owbetaclient", "install")
	repo := flag.String("repo", "..", "repository root")
	flag.Parse()
	c, err := catalog.Open(*install, *repo)
	if err != nil {
		panic(err)
	}
	defer c.Close()
	for _, a := range flag.Args() {
		g, ok := catalog.ParseGUID(a)
		if !ok {
			panic(a)
		}
		fmt.Printf("== %016x %s\n", g, c.GUIDName(g))
		err := c.WithDoc(g, func(d *stu.Document) error {
			for _, in := range d.Instances() {
				pr(view.Tree(d, in.Index, c), 0)
			}
			return nil
		})
		if err != nil {
			fmt.Println("error:", err)
		}
	}
}

func pr(n view.Node, depth int) {
	s := strings.Repeat("  ", depth) + n.Label
	if n.Type != "" {
		s += " <" + n.Type + ">"
	}
	if n.Value != "" {
		s += " = " + n.Value
	}
	if n.Alt != "" {
		s += " (" + n.Alt + ")"
	}
	if n.GUID != "" {
		s += " -> " + n.GUID + " " + n.RefName
	}
	fmt.Println(s)
	for _, ch := range n.Children {
		pr(ch, depth+1)
	}
}
