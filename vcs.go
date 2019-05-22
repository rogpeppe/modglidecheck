package main

import (
	"strings"

	"gopkg.in/errgo.v2/fmt/errors"
)

var kindToVCS = map[string]VCS{
	"bzr": bzrVCS{},
	"hg":  hgVCS{},
	"git": gitVCS{},
}

type VCS interface {
	Kind() string
	ResolveTag(repo string, tag string) (string, error)
}

type VCSInfo struct {
	revid string
	revno string // optional
	clean bool
}

type gitVCS struct{}

func (gitVCS) Kind() string {
	return "git"
}

func (gitVCS) ResolveTag(repo string, tag string) (string, error) {
	if tag == "" {
		panic("empty tag in " + repo)
	}
	// Note: technically this might be wrong because we
	// need to know the submodule too so we know which
	// tag to choose.
	out, err := runCmd("git", "ls-remote", "-q", repo, tag)
	if err != nil {
		return "", errors.Wrap(err)
	}
	out = strings.TrimSuffix(out, "\n")
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return "", errors.Newf("unexpected ls-remote output %q from repo %q, tag %q", out, repo, tag)
		}
		if f[1] == "refs/tags/"+tag {
			return f[0], nil
		}
	}
	// Full match wasn't found. It might be a sub-module.
	if len(lines) == 0 {
		return "", errors.Newf("no tag ref for %q found in %q", tag, out)
	}
	if len(lines) > 1 {
		return "", errors.Newf("ambiguous tag for %q found in %q", tag, out)
	}
	return strings.Fields(lines[0])[0], nil
}

type bzrVCS struct{}

func (bzrVCS) Kind() string {
	return "bzr"
}

func (bzrVCS) ResolveTag(repo string, tag string) (string, error) {
	return "", errors.New("bzr unimplemented")
}

type hgVCS struct{}

func (hgVCS) Kind() string {
	return "hg"
}

func (hgVCS) ResolveTag(repo string, tag string) (string, error) {
	return "", errors.New("hg unimplemented")
}
