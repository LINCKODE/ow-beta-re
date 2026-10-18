// Command jamgen maintains the JAM message catalogue and generates the Go codecs
// of package owserver/inst/jam from it.
//
//	jamgen seed  [-dump jam_protocols.json] [-catalogue catalogue.json]   (re)build the catalogue from the client dump, keeping curated fields
//	jamgen check [-catalogue catalogue.json]                              validate the catalogue
//	jamgen gen   [-catalogue catalogue.json] [-out messages_gen.go]       generate the codecs (go generate in inst/jam)
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: jamgen seed|check|gen [flags]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fl := flag.NewFlagSet(cmd, flag.ExitOnError)
	cat := fl.String("catalogue", "../systems/data/jam/catalogue.json", "catalogue path")
	dump := fl.String("dump", "../game/owbetaclient/Prometheus/jam_protocols.json", "Prometheus protocol dump (seed)")
	out := fl.String("out", "messages_gen.go", "generated Go file (gen)")
	pkg := fl.String("package", "jam", "Go package name (gen)")
	fl.Parse(os.Args[2:])

	if err := run(cmd, *cat, *dump, *out, *pkg); err != nil {
		fmt.Fprintln(os.Stderr, "jamgen:", err)
		os.Exit(1)
	}
}

func run(cmd, catPath, dumpPath, out, pkg string) error {
	switch cmd {
	case "seed":
		prev, err := loadCatalogue(catPath)
		if errors.Is(err, fs.ErrNotExist) {
			prev = nil
		} else if err != nil {
			return err
		}
		c, err := seed(dumpPath, prev)
		if err != nil {
			return err
		}
		return c.save(catPath)
	case "check":
		c, err := loadCatalogue(catPath)
		if err != nil {
			return err
		}
		n := 0
		for _, p := range c.Protocols {
			n += len(p.Messages)
		}
		fmt.Printf("%d protocols, %d messages: ok\n", len(c.Protocols), n)
		return nil
	case "gen":
		c, err := loadCatalogue(catPath)
		if err != nil {
			return err
		}
		src, err := generate(c, pkg)
		if err != nil {
			return err
		}
		return os.WriteFile(out, src, 0o644)
	}
	return fmt.Errorf("unknown command %q", cmd)
}
