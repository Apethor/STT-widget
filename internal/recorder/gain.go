package recorder

import (
	"encoding/binary"
	"math"
)

// SetGain adjusts this recording's volume, including during capture. It changes
// samples before saving and measuring their level, leaving device settings alone.
func (r *Recorder) SetGain(gain float64) {
	if math.IsNaN(gain) || math.IsInf(gain, 0) {
		return
	}
	r.mu.Lock()
	r.gain = max(0, min(2, gain))
	r.mu.Unlock()
}

func (t *Team) SetGains(microphone, playback float64) {
	for index, gain := range []float64{microphone, playback} {
		if input, ok := t.inputs[index].(interface{ SetGain(float64) }); ok {
			input.SetGain(gain)
		}
	}
}

func scalePCM(pcm []byte, gain float64) {
	if gain == 1 {
		return
	}
	for offset := 0; offset+1 < len(pcm); offset += 2 {
		sample := int16(binary.LittleEndian.Uint16(pcm[offset:]))
		scaled := int16(max(-32768, min(32767, math.Round(float64(sample)*gain))))
		binary.LittleEndian.PutUint16(pcm[offset:], uint16(scaled))
	}
}
