package storage

import (
	"bytes"
	"hash/fnv"
	"sort"
)

// Label represents a single label name-value pair
type Label struct {
	Name  string
	Value string
}

// Labels is a sorted set of labels
type Labels []Label

// Len returns the number of labels
func (ls Labels) Len() int { return len(ls) }

// Swap swaps the position of two labels
func (ls Labels) Swap(i, j int) { ls[i], ls[j] = ls[j], ls[i] }

// Less compares two labels
func (ls Labels) Less(i, j int) bool {
	if ls[i].Name != ls[j].Name {
		return ls[i].Name < ls[j].Name
	}
	return ls[i].Value < ls[j].Value
}

// Hash returns a hash of the labels
func (ls Labels) Hash() uint64 {
	h := fnv.New64a()
	for _, l := range ls {
		h.Write([]byte(l.Name))
		h.Write([]byte{0})
		h.Write([]byte(l.Value))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// String returns a string representation of the labels
func (ls Labels) String() string {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, l := range ls {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(l.Name)
		buf.WriteString("=\"")
		buf.WriteString(l.Value)
		buf.WriteByte('"')
	}
	buf.WriteByte('}')
	return buf.String()
}

// Get returns the value for the given label name
func (ls Labels) Get(name string) string {
	for _, l := range ls {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// Has checks if a label with the given name exists
func (ls Labels) Has(name string) bool {
	for _, l := range ls {
		if l.Name == name {
			return true
		}
	}
	return false
}

// WithoutName returns labels without the __name__ label
func (ls Labels) WithoutName() Labels {
	result := make(Labels, 0, len(ls))
	for _, l := range ls {
		if l.Name != "__name__" {
			result = append(result, l)
		}
	}
	return result
}

// Copy creates a deep copy of the labels
func (ls Labels) Copy() Labels {
	result := make(Labels, len(ls))
	copy(result, ls)
	return result
}

// Equals checks if two label sets are equal
func (ls Labels) Equals(other Labels) bool {
	if len(ls) != len(other) {
		return false
	}
	for i := range ls {
		if ls[i].Name != other[i].Name || ls[i].Value != other[i].Value {
			return false
		}
	}
	return true
}

// LabelsFromMap creates Labels from a map, adding __name__ if provided
func LabelsFromMap(name string, tags map[string]string) Labels {
	labels := make(Labels, 0, len(tags)+1)
	
	if name != "" {
		labels = append(labels, Label{Name: "__name__", Value: name})
	}
	
	for k, v := range tags {
		labels = append(labels, Label{Name: k, Value: v})
	}
	
	sort.Sort(labels)
	return labels
}

// LabelsToMap converts Labels to a map
func LabelsToMap(ls Labels) (string, map[string]string) {
	name := ""
	tags := make(map[string]string, len(ls))
	
	for _, l := range ls {
		if l.Name == "__name__" {
			name = l.Value
		} else {
			tags[l.Name] = l.Value
		}
	}
	
	return name, tags
}

// MatchLabels checks if labels match all the given matchers
func MatchLabels(ls Labels, matchers map[string]string) bool {
	for name, value := range matchers {
		if ls.Get(name) != value {
			return false
		}
	}
	return true
}
