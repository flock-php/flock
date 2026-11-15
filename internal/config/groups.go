package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxGroupLength is the longest group name, in characters.
const MaxGroupLength = 40

// NormalizeGroup turns what the user typed into a group name: surrounding space is dropped and
// runs of whitespace become one space. A name that matches an existing group apart from its case
// takes that group's spelling, so "work" joins "Work" instead of starting a second group. An
// empty name means no group.
func (c *Config) NormalizeGroup(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "", nil
	}
	if utf8.RuneCountInString(name) > MaxGroupLength {
		return "", fmt.Errorf("a group name is at most %d characters", MaxGroupLength)
	}
	if strings.ContainsFunc(name, unicode.IsControl) {
		return "", fmt.Errorf("a group name can't contain control characters")
	}
	for _, g := range c.Groups() {
		if strings.EqualFold(g, name) {
			return g, nil
		}
	}
	return name, nil
}

// SetGroup puts site in the named group, or takes it out of its group when name is empty. The
// caller saves it with Upsert.
func (c *Config) SetGroup(site *Site, name string) error {
	group, err := c.NormalizeGroup(name)
	if err != nil {
		return err
	}
	site.Group = group
	return nil
}

// Groups returns the names of the groups that have a linked site, sorted. A group exists as long
// as a site is in it.
func (c *Config) Groups() []string {
	groups := []string{}
	for _, s := range c.Sites {
		if s.Group != "" && !slices.Contains(groups, s.Group) {
			groups = append(groups, s.Group)
		}
	}
	sort.Strings(groups)
	return groups
}

// SitesInGroup returns the sites in the named group (matched without regard to case), sorted by
// name. An empty name returns the sites that are in no group.
func (c *Config) SitesInGroup(name string) []Site {
	var sites []Site
	for _, s := range c.AllSites() {
		if strings.EqualFold(s.Group, name) {
			sites = append(sites, s)
		}
	}
	return sites
}
