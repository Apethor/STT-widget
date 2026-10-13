package video

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"time"
)

type segmentTiming struct {
	start, end float64
	activeAt   time.Time
	firstFrame time.Time
}

func validPosition(position float64) bool {
	return position >= 0 && position <= 24*60*60 &&
		!math.IsNaN(position) && !math.IsInf(position, 0)
}

func audioDuration(tracks []AudioTrack) float64 {
	var duration float64
	for _, track := range tracks {
		duration = max(duration, track.Offset+track.Duration)
	}
	return duration
}

func (r *Recorder) currentPositionLocked(at time.Time) float64 {
	if r.active == nil || len(r.timings) == 0 {
		return r.nextPosition
	}
	timing := r.timings[len(r.timings)-1]
	return timing.start + max(0, at.Sub(timing.activeAt).Seconds())
}

func (r *Recorder) closeAudioWindowLocked(position float64) {
	if len(r.timings) == 0 {
		return
	}
	timing := &r.timings[len(r.timings)-1]
	timing.end = max(timing.start, position)
	r.nextPosition = timing.end
}

func (r *Recorder) alignSegmentsLocked(ctx context.Context, duration float64) error {
	// Historical/synthetic callers can provide already aligned segments.
	if len(r.timings) == 0 {
		return nil
	}
	if len(r.timings) != len(r.segments) || duration <= 0 {
		return errors.New("os intervalos de áudio e vídeo não puderam ser alinhados")
	}
	r.timings[len(r.timings)-1].end = duration
	aligned := make([]string, 0, len(r.segments))
	fps := r.preset.frameRate()
	for index, path := range r.segments {
		timing := r.timings[index]
		startFrame := int64(math.Round(timing.start * float64(fps)))
		endFrame := int64(math.Round(timing.end * float64(fps)))
		if index == len(r.segments)-1 {
			endFrame = int64(math.Ceil(duration * float64(fps)))
		}
		count := endFrame - startFrame
		if count <= 0 {
			continue
		}
		leading := int64(math.Round(max(0, timing.firstFrame.Sub(timing.activeAt).Seconds()) * float64(fps)))
		leading = min(leading, count)
		filter := fmt.Sprintf(
			"fps=%d,setpts=PTS-STARTPTS,tpad=start_mode=clone:start=%d:stop_mode=clone:stop=%d,trim=end_frame=%d,setpts=PTS-STARTPTS",
			fps, leading, count, count,
		)
		output := filepath.Join(r.tempDir, fmt.Sprintf("aligned-%04d.mp4", index+1))
		args := []string{
			"-hide_banner", "-loglevel", "error", "-nostdin", "-y",
			"-protocol_whitelist", "file,pipe", "-i", path,
			"-map", "0:v:0", "-vf", filter, "-an",
		}
		args = append(args, r.preset.encoding(r.region).codecArguments()...)
		args = append(args, output)
		if err := r.runFFmpeg(ctx, args); err != nil {
			return fmt.Errorf("alinhar o trecho %d do vídeo ao áudio: %w", index+1, err)
		}
		aligned = append(aligned, output)
	}
	if len(aligned) == 0 {
		return errors.New("a gravação não contém um intervalo de vídeo com áudio")
	}
	r.segments = aligned
	return nil
}
