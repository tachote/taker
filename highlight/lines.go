package highlight

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const GapSentinel = -1

// ParseLineSpec parses a line spec string like "1,21,39,45-51,53-67"
// into a sorted slice of concrete line numbers with -1 gap sentinels inserted
// between non-adjacent ranges.
func ParseLineSpec(s string) ([]int, error) {
	if s == "" {
		return nil, nil
	}

	var nums []int
	for _, seg := range strings.Split(s, ",") {
		seg = strings.TrimSpace(seg)
		if strings.Contains(seg, "-") {
			parts := strings.SplitN(seg, "-", 2)
			lo, err := strconv.Atoi(strings.TrimSpace(parts[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid range %q: %w", seg, err)
			}
			hi, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid range %q: %w", seg, err)
			}
			if lo > hi {
				return nil, fmt.Errorf("invalid range %q: start > end", seg)
			}
			for i := lo; i <= hi; i++ {
				nums = append(nums, i)
			}
		} else {
			n, err := strconv.Atoi(seg)
			if err != nil {
				return nil, fmt.Errorf("invalid line number %q: %w", seg, err)
			}
			nums = append(nums, n)
		}
	}

	// Deduplicate and sort
	seen := map[int]bool{}
	var deduped []int
	for _, n := range nums {
		if !seen[n] {
			seen[n] = true
			deduped = append(deduped, n)
		}
	}
	sort.Ints(deduped)

	// Insert gap sentinels between non-adjacent lines
	var result []int
	for i, n := range deduped {
		if i > 0 && n > deduped[i-1]+1 {
			result = append(result, GapSentinel)
		}
		result = append(result, n)
	}
	return result, nil
}

// BuildLineSpec takes an arbitrary slice of line numbers (may contain duplicates
// or be unsorted) and returns a sorted, deduplicated slice with GapSentinel
// values inserted between non-adjacent lines. Safe to call with nil/empty input.
func BuildLineSpec(nums []int) []int {
	if len(nums) == 0 {
		return nil
	}
	seen := make(map[int]bool, len(nums))
	var deduped []int
	for _, n := range nums {
		if n > 0 && !seen[n] {
			seen[n] = true
			deduped = append(deduped, n)
		}
	}
	sort.Ints(deduped)

	var result []int
	for i, n := range deduped {
		if i > 0 && n > deduped[i-1]+1 {
			result = append(result, GapSentinel)
		}
		result = append(result, n)
	}
	return result
}

// ParseHighlightSpec parses a highlight spec like "13", "13-15", or "13,15,20-25"
// into a set of line numbers. Returns nil for empty string (no highlight).
func ParseHighlightSpec(s string) (map[int]bool, error) {
	if s == "" {
		return nil, nil
	}
	// Reuse ParseLineSpec but strip gap sentinels — we just want the set of numbers.
	nums, err := ParseLineSpec(s)
	if err != nil {
		return nil, err
	}
	set := make(map[int]bool, len(nums))
	for _, n := range nums {
		if n != GapSentinel {
			set[n] = true
		}
	}
	return set, nil
}
