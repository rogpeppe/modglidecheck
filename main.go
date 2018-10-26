package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/juju/utils/parallel"
	"golang.org/x/tools/go/vcs"
	"gopkg.in/errgo.v2/fmt/errors"
)

var (
	exitCodeMu sync.Mutex
	exitCode   = 0
)

func main() {
	flag.Parse()
	mods, err := allModules()
	if err != nil {
		log.Fatal(err)
	}
	infoc := make(chan *depInfo)
	go func() {
		run := parallel.NewRun(20)
		for _, m := range mods {
			m := m
			if m.Version == "" {
				continue
			}
			run.Do(func() error {
				info, err := getRev(m)
				if err != nil {
					errorf("%v", err)
				} else {
					infoc <- info
				}
				return nil
			})
		}
		go func() {
			run.Wait()
			close(infoc)
		}()
	}()
	var infos []*depInfo
	for info := range infoc {
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].project < infos[j].project
	})
	for _, info := range infos {
		fmt.Println(info)
	}
	os.Exit(exitCode)
}

func getRev(m *listModule) (*depInfo, error) {
	info, err := getVCSInfoForModule(m)
	if err != nil {
		return nil, errors.Newf("cannot get VCS info for %v", m.Path)
	}
	if info.vcs.Kind() != "git" {
		return nil, errors.Newf("unsupported VCS %q in module %v@%v", info.vcs.Kind(), m.Path, m.Version)
	}
	//log.Printf("version for %v is %v", m.Path, m.Version)
	var rev string
	if IsPseudoVersion(m.Version) {
		rev, err = PseudoVersionRev(m.Version)
		if err != nil {
			return nil, errors.Newf("cannot get rev from %q: %v", m.Version, err)
		}
	} else {
		if m.Version == "" {
			panic("empty version in " + m.Path)
		}
		vers := strings.TrimSuffix(m.Version, "+incompatible")
		if vers == "" {
			panic(fmt.Sprintf("now empty %q, vers %v", m.Path, m.Version))
		}
		rev, err = info.vcs.ResolveTag(info.root.Repo, vers)
		if err != nil {
			return nil, errors.Newf("cannot resolve %q in %v: %v", vers, info.root.Repo, err)
		}
	}
	return &depInfo{
		project: m.Path,
		vcsKind: info.vcs.Kind(),
		rev:     rev,
	}, nil
}

type depInfo struct {
	project string
	vcsKind string
	rev     string
}

func (d *depInfo) String() string {
	return fmt.Sprintf("%s\t%s\t%s\t%s", d.project, d.vcsKind, d.rev, "")
}

type moduleVCSInfo struct {
	// module holds the module information as printed by go list.
	module *listModule
	// root holds information on the VCS root of the module.
	root *vcs.RepoRoot
	// vcs holds the implementation of the VCS used by the module.
	vcs VCS
}

// getVCSInfoForModule returns VCS information about the module
// by inspecting the module path and the module's checked out
// directory.
func getVCSInfoForModule(m *listModule) (*moduleVCSInfo, error) {
	// TODO if module directory already exists, could look in it to see if there's
	// a single VCS directory and use that if so, to avoid hitting the network
	// for vanity imports.
	root, err := vcs.RepoRootForImportPath(m.Path, *printCommands)
	if err != nil {
		return nil, errors.Note(err, nil, "cannot find module root")
	}
	v, ok := kindToVCS[root.VCS.Cmd]
	if !ok {
		return nil, errors.Newf("unknown VCS kind %q", root.VCS.Cmd)
	}
	return &moduleVCSInfo{
		module: m,
		root:   root,
		vcs:    v,
	}, nil
}

func errorf(f string, a ...interface{}) {
	fmt.Fprintln(os.Stderr, fmt.Sprintf(f, a...))
	for _, arg := range a {
		if err, ok := arg.(error); ok {
			fmt.Fprintf(os.Stderr, "error: %s\n", errors.Details(err))
		}
	}
	exitCodeMu.Lock()
	defer exitCodeMu.Unlock()
	exitCode = 1
}
