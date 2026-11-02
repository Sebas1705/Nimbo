package nimbo

import (
	"regexp"
	"strconv"
	"strings"
)

// SemVer is `1.2.3` or `1.2.3-beta.1`, with an optional leading `v`. A release without a suffix is newer than the
// same one with it. Pre is empty when there is no suffix (the pattern never accepts an empty one).
type SemVer struct {
	Major, Minor, Patch int
	Pre                 string
}

var semVerPattern = regexp.MustCompile(`^v?(\d{1,5})\.(\d{1,5})\.(\d{1,5})(?:-([0-9A-Za-z.-]{1,32}))?$`)

// ParseSemVer reads a version; ok is false when the text is not one.
func ParseSemVer(text string) (SemVer, bool) {
	m := semVerPattern.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return SemVer{}, false
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return SemVer{a, b, c, m[4]}, true
}

func (v SemVer) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or 1. Pre-release parts are compared one by one: numbers numerically and before text, text
// alphabetically, and a shorter list is older than a longer one that starts the same.
func (v SemVer) Compare(o SemVer) int {
	for _, p := range [][2]int{{v.Major, o.Major}, {v.Minor, o.Minor}, {v.Patch, o.Patch}} {
		if p[0] != p[1] {
			return semSign(p[0] - p[1])
		}
	}
	if v.Pre == o.Pre {
		return 0
	}
	if v.Pre == "" {
		return 1
	}
	if o.Pre == "" {
		return -1
	}
	a, b := strings.Split(v.Pre, "."), strings.Split(o.Pre, ".")
	for i := 0; i < len(a) || i < len(b); i++ {
		if i >= len(a) {
			return -1
		}
		if i >= len(b) {
			return 1
		}
		xi, xerr := strconv.ParseInt(a[i], 10, 32)
		yi, yerr := strconv.ParseInt(b[i], 10, 32)
		var c int
		switch {
		case xerr == nil && yerr == nil:
			c = semSign(int(xi - yi))
		case xerr == nil:
			c = -1
		case yerr == nil:
			c = 1
		default:
			c = strings.Compare(a[i], b[i])
		}
		if c != 0 {
			return c
		}
	}
	return 0
}

func semSign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
