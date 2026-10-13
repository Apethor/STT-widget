package soundboard

import "testing"

// Measures prepared request/render work only. Device latency is measured in
// the opt-in native integration, and is not inferred from this CPU benchmark.
func BenchmarkPreparedEffectRequestAndRender(b *testing.B) {
	engine := NewEngine()
	engine.ready = true
	pcm := make([]int16, 960)
	for index := range pcm {
		pcm[index] = 1200
	}
	engine.prepared["effect"] = pcm
	target := make([]int16, 960)
	b.ReportAllocs()
	for b.Loop() {
		_ = engine.Play("effect")
		engine.fill(target)
	}
}
