// Package validate holds the input checks shared by the CI forced command
// and the manual CLI. Error texts are part of the CI protocol (they show up
// in pipeline logs) and match the original bash wrapper.
package validate

import (
	"fmt"
	"regexp"
)

var (
	appRe     = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	releaseRe = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
	domainRe  = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)
)

const (
	Laravel = "laravel"
	React   = "react"
	Static  = "static"
)

var Types = []string{Laravel, React, Static}

// AppName allows only [a-zA-Z0-9_-]: no path separators, no "..".
func AppName(app string) error {
	if !appRe.MatchString(app) {
		return fmt.Errorf("invalid app name: %s", app)
	}
	return nil
}

// ReleaseID allows dots (for ids like 1.2.3), so "." and ".." have to be
// rejected explicitly or releases/<id> would escape to the releases dir or
// the app root.
func ReleaseID(id string) error {
	if !releaseRe.MatchString(id) || id == "." || id == ".." {
		return fmt.Errorf("invalid release id: %s", id)
	}
	return nil
}

// Domain is written into the Apache vhost, so it must be a plain hostname.
func Domain(d string) error {
	if d == "" {
		return fmt.Errorf("domínio não informado (use --domain, ou DOMAIN_SUFFIX em /etc/deploy-tools.conf)")
	}
	if !domainRe.MatchString(d) {
		return fmt.Errorf("domínio inválido: %q", d)
	}
	return nil
}

func Type(t string) error {
	for _, v := range Types {
		if t == v {
			return nil
		}
	}
	return fmt.Errorf("tipo inválido: %q (use laravel, react ou static)", t)
}
