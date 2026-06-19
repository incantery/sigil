package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ContrastRatio returns the WCAG 2.1 relative-luminance contrast ratio for
// two hex colors. Result is in [1.0, 21.0]. 4.5 is the AA threshold for
// normal text, 7.0 the AAA threshold. The argument order doesn't matter.
//
// Spec: https://www.w3.org/TR/WCAG21/#dfn-relative-luminance
func ContrastRatio(a, b string) (float64, error) {
	la, err := relativeLuminance(a)
	if err != nil {
		return 0, err
	}
	lb, err := relativeLuminance(b)
	if err != nil {
		return 0, err
	}
	lighter, darker := la, lb
	if lb > la {
		lighter, darker = lb, la
	}
	return (lighter + 0.05) / (darker + 0.05), nil
}

// relativeLuminance computes WCAG's L for one hex color. Handles `#rgb`
// and `#rrggbb`; the # is required (defensive — we never see colors
// without it from theme decls).
func relativeLuminance(hex string) (float64, error) {
	r, g, b, err := parseHex(hex)
	if err != nil {
		return 0, err
	}
	rs := channelToLinear(float64(r) / 255.0)
	gs := channelToLinear(float64(g) / 255.0)
	bs := channelToLinear(float64(b) / 255.0)
	return 0.2126*rs + 0.7152*gs + 0.0722*bs, nil
}

func channelToLinear(c float64) float64 {
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func parseHex(s string) (int, int, int, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return 0, 0, 0, fmt.Errorf("color %q must start with #", s)
	}
	s = s[1:]
	switch len(s) {
	case 3:
		r, err := strconv.ParseInt(string(s[0])+string(s[0]), 16, 0)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("color %q: bad hex digit", s)
		}
		g, err := strconv.ParseInt(string(s[1])+string(s[1]), 16, 0)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("color %q: bad hex digit", s)
		}
		b, err := strconv.ParseInt(string(s[2])+string(s[2]), 16, 0)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("color %q: bad hex digit", s)
		}
		return int(r), int(g), int(b), nil
	case 6:
		r, err := strconv.ParseInt(s[0:2], 16, 0)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("color %q: bad hex digit", s)
		}
		g, err := strconv.ParseInt(s[2:4], 16, 0)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("color %q: bad hex digit", s)
		}
		b, err := strconv.ParseInt(s[4:6], 16, 0)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("color %q: bad hex digit", s)
		}
		return int(r), int(g), int(b), nil
	default:
		return 0, 0, 0, fmt.Errorf("color %q: must be #rgb or #rrggbb", "#"+s)
	}
}
