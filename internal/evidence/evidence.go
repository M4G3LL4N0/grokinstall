// Package evidence records conclusions as findings backed by deterministic
// evidence strings. Evidence is the currency of GrokInstall: nothing in a plan
// should rest on an unsupported claim.
package evidence

import (
	"encoding/json"
	"fmt"
	"sort"
)

// MaxEvidencePerFinding bounds how much supporting detail one finding carries.
// A repository can match a finding in hundreds of files; a plan that quoted all
// of them would defeat the purpose of evidence.
const MaxEvidencePerFinding = 8

// Confidence expresses how strongly a finding is supported.
type Confidence string

const (
	// ConfidenceHigh is backed by direct deterministic signals (fields, files).
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium is supported by indirect signals.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow is a plausible reading from weak signals.
	ConfidenceLow Confidence = "low"
)

// confidenceRank orders confidence for escalation (max wins).
var confidenceRank = map[Confidence]int{
	ConfidenceLow:    1,
	ConfidenceMedium: 2,
	ConfidenceHigh:   3,
}

// Item is a single finding.
type Item struct {
	Finding    string     `json:"finding"`
	Confidence Confidence `json:"confidence"`
	Evidence   []string   `json:"evidence"`
}

// Set is an ordered, deduplicated collection of findings. Order is stable so
// that JSON output is deterministic.
type Set struct {
	items []Item
	byKey map[string]int
}

// New returns an empty evidence set.
func New() *Set {
	return &Set{byKey: map[string]int{}}
}

func key(finding string) string {
	return finding
}

// Add records a finding, merging evidence and escalating confidence.
func (s *Set) Add(finding string, conf Confidence, evidence ...string) {
	k := key(finding)
	if i, ok := s.byKey[k]; ok {
		item := &s.items[i]
		if confidenceRank[conf] > confidenceRank[item.Confidence] {
			item.Confidence = conf
		}
		item.Evidence = mergeStrings(item.Evidence, evidence)
		return
	}
	s.byKey[k] = len(s.items)
	s.items = append(s.items, Item{
		Finding:    finding,
		Confidence: conf,
		Evidence:   mergeStrings(nil, evidence),
	})
}

// AddItem records a fully-formed item.
func (s *Set) AddItem(it Item) {
	s.Add(it.Finding, it.Confidence, it.Evidence...)
}

func mergeStrings(dst, src []string) []string {
	seen := map[string]bool{}
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range src {
		if !seen[v] {
			dst = append(dst, v)
			seen[v] = true
		}
	}
	return boundEvidence(dst)
}

// boundEvidence keeps the list readable and states plainly how much was
// omitted, so a truncated finding is never mistaken for a complete one.
func boundEvidence(list []string) []string {
	if len(list) <= MaxEvidencePerFinding {
		return list
	}
	kept := make([]string, MaxEvidencePerFinding, MaxEvidencePerFinding+1)
	copy(kept, list[:MaxEvidencePerFinding])
	omitted := len(list) - MaxEvidencePerFinding
	kept = append(kept, fmt.Sprintf("(and %d more)", omitted))
	return kept
}

// Has reports whether a finding exists.
func (s *Set) Has(finding string) bool {
	_, ok := s.byKey[key(finding)]
	return ok
}

// Get returns the item for a finding, or the zero value.
func (s *Set) Get(finding string) Item {
	if i, ok := s.byKey[key(finding)]; ok {
		return s.items[i]
	}
	return Item{}
}

// List returns a copy of all findings in stable order.
func (s *Set) List() []Item {
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

// Len returns the number of findings.
func (s *Set) Len() int {
	return len(s.items)
}

// Merge folds another set into s, preserving order of new items by sorting
// them at the end for determinism.
func (s *Set) Merge(o *Set) {
	if o == nil {
		return
	}
	names := make([]string, 0, len(o.items))
	for _, it := range o.items {
		names = append(names, it.Finding)
	}
	sort.Strings(names)
	for _, name := range names {
		s.AddItem(o.Get(name))
	}
}

// JSON serializes the set.
func (s *Set) JSON() ([]byte, error) {
	return json.Marshal(s.items)
}

// FromJSON deserializes a set.
func FromJSON(data []byte) (*Set, error) {
	var items []Item
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	s := New()
	for _, it := range items {
		s.AddItem(it)
	}
	return s, nil
}
