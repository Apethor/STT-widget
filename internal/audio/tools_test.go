package audio

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
)

type zeroReader struct{}

func (zeroReader) Read(payload []byte) (int, error) {
	clear(payload)
	return len(payload), nil
}

func TestSplitPCMExactBoundaryAndThirtyMinuteTail(t *testing.T) {
	tests := []struct {
		name    string
		samples int64
		count   int
	}{
		{name: "exact first boundary", samples: 300 * SampleRate, count: 1},
		{name: "exact second boundary", samples: 598 * SampleRate, count: 2},
		{name: "single sample tail", samples: 598*SampleRate + 1, count: 3},
		{name: "thirty minutes", samples: 1800 * SampleRate, count: 7},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts := make([]Chunk, 0)
			err := SplitPCM(t.Context(), io.LimitReader(zeroReader{}, test.samples*SampleBytes),
				ChunkOptions{ChunkSeconds: 300, OverlapSeconds: 2},
				func(context.Context, []byte) ([]byte, error) {
					t.Fatal("silent parts must not be compressed")
					return nil, nil
				},
				func(chunk Chunk) error {
					parts = append(parts, chunk)
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}
			if len(parts) != test.count {
				t.Fatalf("parts = %d, want %d", len(parts), test.count)
			}
			var end float64
			for index, part := range parts {
				if part.Index != index || part.Start > end+1e-9 {
					t.Fatalf("gap or invalid index at %+v", part)
				}
				end = part.Start + part.Duration
			}
			want := float64(test.samples) / SampleRate
			if math.Abs(end-want) > 1e-9 {
				t.Fatalf("audio ends at %.9f, want %.9f", end, want)
			}
		})
	}
}

func TestSplitPCMCancellationAndMalformedSample(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := SplitPCM(ctx, zeroReader{}, ChunkOptions{ChunkSeconds: 15}, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled decoder = %v", err)
	}
	if err := SplitPCM(t.Context(), io.LimitReader(zeroReader{}, 3), ChunkOptions{ChunkSeconds: 15}, nil, nil); err == nil {
		t.Fatal("partial PCM sample was accepted")
	}
}
