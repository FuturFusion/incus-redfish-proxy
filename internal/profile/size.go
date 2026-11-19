package profile

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// Size is a number of bytes. YAML accepts a plain integer or an integer with the binary unit KiB, MiB or GiB.
type Size int64

var sizeUnits = []struct {
	suffix string
	factor int64
}{
	{"KiB", 1 << 10},
	{"MiB", 1 << 20},
	{"GiB", 1 << 30},
}

// UnmarshalYAML parses a size like 4MiB.
func (s *Size) UnmarshalYAML(node *yaml.Node) error {
	text := node.Value
	factor := int64(1)

	for _, unit := range sizeUnits {
		number, found := strings.CutSuffix(text, unit.suffix)
		if found {
			text = number
			factor = unit.factor

			break
		}
	}

	value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil {
		return fmt.Errorf("size %q is not an integer with an optional unit KiB, MiB or GiB", node.Value)
	}

	if value > math.MaxInt64/factor || value < math.MinInt64/factor {
		return fmt.Errorf("size %q is too large", node.Value)
	}

	*s = Size(value * factor)

	return nil
}
