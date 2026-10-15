package trust

import (
	"bytes"
	"context"
	"testing"
)

// FuzzTrustGraph builds random small flow graphs and checks the bounds RFC
// 0021 §15 claims of the flow: each node keeps min(world, cap) of its own
// and never holds more than its cap; the total never exceeds the world's;
// a region no world reaches, and no edge enters, holds nothing; and the
// flow is deterministic.
func FuzzTrustGraph(f *testing.F) {
	f.Add([]byte{5, 2, 1, 0, 1, 2, 3, 4, 1, 3, 2, 4, 9, 9, 9})
	f.Add([]byte("world flow region bound determinism"))
	f.Add(bytes.Repeat([]byte{7, 1, 250, 3}, 40))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 3 {
			return
		}
		n := 3 + int(data[0])%28
		isolated := int(data[1]) % (n - 1) // the last `isolated` nodes have no world and no in-edges
		data = data[2:]
		next := func() int {
			if len(data) == 0 {
				return 0
			}
			v := int(data[0])
			data = data[1:]
			return v
		}
		const U, nodeCap = 200 * mc, 750 * mc
		g := &Graph{RootOf: map[string]string{}, MaxWork: 1 << 40, Levels: flowLevels(U, nodeCap)}
		for i := 0; i < n; i++ {
			g.Labels = append(g.Labels, string(rune('a'+i)))
			w := int64(next()%40) * 50 * mc
			if i >= n-isolated {
				w = 0
			}
			g.World = append(g.World, w)
			g.Cap = append(g.Cap, nodeCap*int64(4+next()%5)/8)
		}
		type key struct{ s, d int32 }
		seen := map[key]bool{}
		for k := next() % 60; k > 0; k-- {
			s, d := int32(next()%n), int32(next()%n)
			if s == d || seen[key{s, d}] || int(d) >= n-isolated {
				continue
			}
			seen[key{s, d}] = true
			g.Src, g.Dst, g.EdgeCap = append(g.Src, s), append(g.Dst, d), append(g.EdgeCap, int64(1+next()%250)*mc)
		}
		// edges must be in (src, dst) order
		for i := 1; i < len(g.Src); i++ {
			for j := i; j > 0 && (g.Src[j] < g.Src[j-1] || g.Src[j] == g.Src[j-1] && g.Dst[j] < g.Dst[j-1]); j-- {
				g.Src[j], g.Src[j-1] = g.Src[j-1], g.Src[j]
				g.Dst[j], g.Dst[j-1] = g.Dst[j-1], g.Dst[j]
				g.EdgeCap[j], g.EdgeCap[j-1] = g.EdgeCap[j-1], g.EdgeCap[j]
			}
		}
		g.Reindex()
		res, err := g.flow(context.Background(), nil, g.MaxWork, 0)
		if err != nil {
			t.Fatal(err)
		}
		var total, world int64
		for i := 0; i < n; i++ {
			world += g.World[i]
			total += res.Standing[i]
			if res.Standing[i] > g.Cap[i] || res.Own[i] != min(g.World[i], g.Cap[i]) || res.Standing[i] < res.Own[i] {
				t.Fatalf("node %d: standing %d, own %d, world %d, cap %d", i, res.Standing[i], res.Own[i], g.World[i], g.Cap[i])
			}
			if i >= n-isolated && res.Standing[i] != 0 {
				t.Fatalf("isolated node %d holds %d", i, res.Standing[i])
			}
		}
		if total > world {
			t.Fatalf("total %d > world %d", total, world)
		}
		again, _ := g.flow(context.Background(), nil, g.MaxWork, 0)
		for i := range res.Standing {
			if again.Standing[i] != res.Standing[i] {
				t.Fatal("the flow is not deterministic")
			}
		}
	})
}
