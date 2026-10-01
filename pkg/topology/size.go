package topology

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Size is a byte quantity written in YAML as "512MiB", "4GiB", "40G", ...
// Single-letter and IEC suffixes are binary (G == GiB); "GB"-style suffixes
// are decimal. A bare integer is bytes.
type Size uint64

const (
	KiB Size = 1 << (10 * (iota + 1))
	MiB
	GiB
	TiB
)

var sizeSuffixes = []struct {
	suffix string
	mult   uint64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
	{"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12},
	{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30}, {"T", 1 << 40},
	{"B", 1},
}

func ParseSize(s string) (Size, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := uint64(1)
	num := s
	for _, sf := range sizeSuffixes {
		if strings.HasSuffix(s, sf.suffix) {
			mult = sf.mult
			num = strings.TrimSpace(strings.TrimSuffix(s, sf.suffix))
			break
		}
	}
	n, err := strconv.ParseUint(num, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q (examples: 512MiB, 4GiB, 40G)", s)
	}
	if n != 0 && mult > ^uint64(0)/n {
		return 0, fmt.Errorf("size %q overflows", s)
	}
	return Size(n * mult), nil
}

func (s *Size) UnmarshalYAML(node *yaml.Node) error {
	v, err := ParseSize(node.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", node.Line, err)
	}
	*s = v
	return nil
}

func (s Size) MarshalYAML() (any, error) { return s.String(), nil }

func (s Size) String() string {
	for _, u := range []struct {
		name string
		v    Size
	}{{"TiB", TiB}, {"GiB", GiB}, {"MiB", MiB}, {"KiB", KiB}} {
		if s >= u.v && s%u.v == 0 {
			return fmt.Sprintf("%d%s", s/u.v, u.name)
		}
	}
	return fmt.Sprintf("%dB", uint64(s))
}
