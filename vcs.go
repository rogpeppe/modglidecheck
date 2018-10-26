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
	out, err := runCmd("git", "ls-remote", "-q", repo, tag)
	if err != nil {
		return "", errors.Wrap(err)
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return "", errors.Newf("unexpected ls-remote output %q from repo %q, tag %q", out, repo, tag)
	}
	return f[0], nil
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
