package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juju/utils/parallel"
	"golang.org/x/tools/go/vcs"
	"gopkg.in/errgo.v2/fmt/errors"
	"gopkg.in/yaml.v1"
)

var (
	exitCodeMu sync.Mutex
	exitCode   = 0
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "modglide\n")
		os.Exit(2)
	}
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
	}
	glideMods, err := readGlideLock(filepath.Join(".", "glide.lock"))
	if err != nil {
		log.Fatal(err)
	}
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
	changed := 0
	notChanged := 0
	for _, info := range infos {
		if info.vcsKind != "git" {
			log.Fatalf("ERROR %s uses %s not git", info.project, info.vcsKind)
			continue
		}
		oldVers, ok := glideMods[info.project]
		if !ok {
			continue
		}
		newVers := info.rev
		if len(newVers) > 12 {
			newVers = newVers[:12]
		}
		if len(newVers) < len(oldVers) {
			oldVers = oldVers[0:len(newVers)]
		}
		if oldVers == newVers {
			notChanged++
			continue
		}
		oldDate, oldCommit, err := commitDate(info.project, oldVers)
		if err != nil {
			log.Printf("cannot get commit date for %v %v: %v", info.project, oldVers, err)
		}
		newDate, newCommit, err := commitDate(info.project, newVers)
		if err != nil {
			log.Printf("cannot get commit date for %v %v: %v", info.project, newVers, err)
		}
		if oldCommit == newCommit {
			notChanged++
			continue
		}
		var which string
		if newDate.Before(oldDate) {
			which = " reversion"
		}
		fmt.Printf("%s%s\n\t%s %v\n\t%s %v\n", info.project, which, oldVers, oldDate.Round(time.Second), newVers, newDate.Round(time.Second))
		changed++
	}
	fmt.Printf("%d/%d changed\n", changed, notChanged+changed)
	os.Exit(exitCode)
}

func commitDate(repo string, commit string) (time.Time, string, error) {
	dir := filepath.Join(os.Getenv("GOPATH"), "src", repo)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		if os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "XXXX go get %s\n", repo)
			c := exec.Command("go", "get", repo)
			c.Stderr = os.Stderr
			c.Env = append(os.Environ(), "GO111MODULE=off")
			if err := c.Run(); err != nil {
				return time.Time{}, "", fmt.Errorf("could not fetch %s: %v", repo, err)
			}
		} else {
			return time.Time{}, "", fmt.Errorf("no repo dir for %s", repo)
		}
	}
	c := exec.Command("git", "log", "-1", "--pretty=format:%H %ct", commit)
	c.Dir = dir
	data, err := c.Output()
	if err != nil {
		c = exec.Command("git", "fetch", "origin")
		c.Dir = dir
		if err := c.Run(); err != nil {
			return time.Time{}, "", fmt.Errorf("cannot fetch origin in %s: %v", repo, err)
		}
		c = exec.Command("git", "log", "-1", "--pretty=format:%ct", commit)
		c.Dir = dir
		c.Stderr = os.Stderr
		data, err = c.Output()
		if err != nil {
			return time.Time{}, "", fmt.Errorf("cannot get log for %s: %v", repo, err)
		}
	}
	var timestamp int64
	var actualCommit string
	if n, err := fmt.Sscanf(string(data), "%s %d\n", &actualCommit, &timestamp); n != 2 || err != nil {
		return time.Time{}, "", fmt.Errorf("scan %q failed: %v", err)
	}
	return time.Unix(timestamp, 0).UTC(), actualCommit, nil
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

func readGlideLock(f string) (map[string]string, error) {
	data, err := ioutil.ReadFile(f)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	var gl glideLock
	if err := yaml.Unmarshal(data, &gl); err != nil {
		return nil, errors.Wrap(err)
	}
	deps := make(map[string]string)
	for _, imp := range gl.Imports {
		deps[imp.Name] = imp.Version
	}
	return deps, nil
}

type glideLock struct {
	Imports []glideVersion `yaml:"imports"`
}

type glideVersion struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}
