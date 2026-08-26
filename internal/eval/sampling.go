package eval

import "sort"

// DrawStratified picks n task ids spread across the curation categories,
// for the "quick check" shape: a cheap first signal that still touches every
// kind of turn the set covers.
//
// The draw cycles the categories in their canonical order, taking one random
// unpicked task from each pass and skipping exhausted ones, so category counts
// differ by at most one wherever the set allows. Ranking by anything else — or
// drawing uniformly — would let a set that is 70 % chat produce a subset that
// is all chat, which is the one thing a stratified sample exists to prevent.
//
// n <= 0 or n >= len(tasks) returns nil, meaning "the whole set": the pin is
// only worth recording when it actually narrows something, and a nil pin is
// what every reader treats as unpinned.
//
// Randomness comes from randIndex (crypto/rand), like the blinding coin — the
// package deliberately has one source rather than a security-grade one next to
// a seeded one, and there is no seed knob to make a draw reproducible.
func DrawStratified(tasks []Task, n int) TaskIDList {
	if n <= 0 || n >= len(tasks) {
		return nil
	}
	order, buckets := bucketByCategory(tasks)

	picked := make([]int64, 0, n)
	for len(picked) < n {
		progressed := false
		for _, cat := range order {
			b := buckets[cat]
			if len(b) == 0 {
				continue
			}
			i := randIndex(len(b))
			picked = append(picked, b[i])
			// Swap-remove: order inside a bucket is irrelevant once the pick is
			// random, and the final list is sorted anyway.
			b[i] = b[len(b)-1]
			buckets[cat] = b[:len(b)-1]
			progressed = true
			if len(picked) == n {
				break
			}
		}
		if !progressed {
			break
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i] < picked[j] })
	return picked
}

// bucketByCategory groups task ids by category and returns the cycle order:
// the canonical categories first, then anything unrecognised, sorted. A
// stored category is validated on write, so the tail is defensive — but a task
// the draw cannot classify must still be drawable.
func bucketByCategory(tasks []Task) ([]string, map[string][]int64) {
	buckets := make(map[string][]int64)
	for _, t := range tasks {
		buckets[t.Category] = append(buckets[t.Category], t.ID)
	}
	order := make([]string, 0, len(buckets))
	for _, cat := range Categories() {
		if len(buckets[cat]) > 0 {
			order = append(order, cat)
		}
	}
	var extra []string
	for cat := range buckets {
		if !ValidCategory(cat) {
			extra = append(extra, cat)
		}
	}
	sort.Strings(extra)
	return append(order, extra...), buckets
}
