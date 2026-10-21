package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

const outputFPS = 30

// stallTimeout is how long the encoder can go without outputting a frame
// before it's killed and restarted, e.g. when the RTMP server stops responding.
const stallTimeout = 15 * time.Second

func runPlayout(output, encoder, previewDir string, playlist *Playlist) {
	for {
		err := stream(output, encoder, previewDir, playlist)
		log.Printf("encoder stopped: %v, restarting in 5s", err)
		time.Sleep(5 * time.Second)
	}
}

func stream(output, encoder, previewDir string, playlist *Playlist) error {
	preview := "[f=hls:onfail=ignore:hls_time=2:hls_list_size=5:hls_flags=delete_segments:hls_start_number_source=epoch]" +
		filepath.Join(previewDir, "live.m3u8")

	video := []string{"-c:v", encoder, "-pix_fmt", "yuv420p", "-g", "60", "-b:v", "4500k"}
	if encoder == "libx264" {
		video = append(video, "-preset", "veryfast", "-maxrate", "4500k", "-bufsize", "9000k")
	}
	if encoder == "h264_v4l2m2m" {
		video = append(video, "-bsf:v", "extract_extradata")
	} else {
		video = append(video, "-flags:v", "+global_header")
	}

	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-progress", "pipe:1",
		"-re",
		"-f", "mpegts", "-i", "pipe:0",
		"-map", "0:v", "-map", "0:a",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=" + strconv.Itoa(outputFPS),
	}
	args = append(args, video...)
	args = append(args,
		"-af", "aresample=async=1", "-ar", "48000",
		"-c:a", "aac", "-b:a", "128k", "-flags:a", "+global_header",
		"-f", "tee", "[f=flv]"+output+"|"+preview,
	)
	ffmpeg := exec.Command("ffmpeg", args...)
	ffmpeg.Stderr = os.Stderr

	// An os.Pipe rather than StdinPipe, since Wait closes a StdinPipe while
	// feed may be handing it to the next clip's ffmpeg.
	input, stdin, err := os.Pipe()
	if err != nil {
		return err
	}
	defer stdin.Close()
	ffmpeg.Stdin = input
	progress, err := ffmpeg.StdoutPipe()
	if err != nil {
		input.Close()
		return err
	}
	err = ffmpeg.Start()
	input.Close()
	if err != nil {
		return err
	}
	cues := make(chan cue, 100)
	go followProgress(progress, cues, playlist, func() { ffmpeg.Process.Kill() })
	exited := make(chan error, 1)
	go func() { exited <- ffmpeg.Wait() }()

	var offset time.Duration
	for {
		select {
		case err := <-exited:
			return err
		default:
		}

		// A clip that can't be played is replaced by a second of filler,
		// so the encoder never runs out of input.
		path := playlist.Next()
		var duration time.Duration
		if path != "" {
			duration, err = probeDuration(path)
			if err != nil {
				log.Printf("%s: %v, playing filler instead", path, err)
				path = ""
			}
		}

		next := cue{start: offset}
		if path != "" {
			log.Printf("queueing %s", path)
			next.name = filepath.Base(path)
		}
		select {
		case cues <- next:
		case err := <-exited:
			return err
		}

		if path == "" {
			err = filler(stdin, offset)
			offset += time.Second
		} else {
			err = remux(path, duration, stdin, offset)
			offset += duration
		}
		if err != nil {
			log.Printf("%s: %v", path, err)
		}
	}
}

type cue struct {
	start time.Duration
	name  string
}

// followProgress updates what's playing from ffmpeg's progress output, and
// calls stalled if no new frames are output for stallTimeout.
func followProgress(progress io.Reader, cues <-chan cue, playlist *Playlist, stalled func()) {
	counts := make(chan int64)
	go func() {
		defer close(counts)
		scanner := bufio.NewScanner(progress)
		for scanner.Scan() {
			value, ok := strings.CutPrefix(scanner.Text(), "frame=")
			if !ok {
				continue
			}
			if frames, err := strconv.ParseInt(value, 10, 64); err == nil {
				counts <- frames
			}
		}
	}()

	var pending []cue
	var last int64
	watchdog := time.NewTimer(stallTimeout)
	defer watchdog.Stop()
	for {
		var frames int64
		select {
		case n, ok := <-counts:
			if !ok {
				return
			}
			frames = n
		case <-watchdog.C:
			log.Printf("no frames output for %s, killing encoder", stallTimeout)
			stalled()
			continue
		}
		if frames > last {
			last = frames
			watchdog.Reset(stallTimeout)
		}

		for len(cues) > 0 {
			pending = append(pending, <-cues)
		}
		for len(pending) > 0 && pending[0].start <= time.Duration(frames)*time.Second/outputFPS {
			if pending[0].name != "" {
				log.Printf("on air: %s", pending[0].name)
			}
			playlist.SetPlaying(pending[0].name)
			pending = pending[1:]
		}
	}
}

// remux feeds the first duration of the clip at path. Cutting it with -t
// rather than -shortest keeps the video from running past the end of shorter
// audio, which leaves the RTMP output waiting for audio.
func remux(path string, duration time.Duration, dst io.Writer, offset time.Duration) error {
	return feed(dst, offset,
		"-i", path,
		"-map", "0:v:0", "-map", "0:a:0",
		"-t", strconv.FormatFloat(duration.Seconds(), 'f', 6, 64),
		"-c:v", "copy",
		"-af", "aresample=48000,asetnsamples=n=1920:p=0", "-ac", "2",
	)
}

func filler(dst io.Writer, offset time.Duration) error {
	return feed(dst, offset,
		"-f", "lavfi", "-i", "color=black:size=640x360:rate="+strconv.Itoa(outputFPS),
		"-f", "lavfi", "-i", "anullsrc=sample_rate=48000:channel_layout=stereo:nb_samples=1920",
		"-t", "1",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
	)
}

func feed(dst io.Writer, offset time.Duration, args ...string) error {
	args = append([]string{"-hide_banner", "-loglevel", "error"}, args...)
	args = append(args,
		"-c:a", "s302m", "-strict", "-2", "-sample_fmt", "s32",
		"-output_ts_offset", strconv.FormatFloat(offset.Seconds(), 'f', 6, 64),
		"-mpegts_flags", "initial_discontinuity",
		"-f", "mpegts", "pipe:1",
	)
	ffmpeg := exec.Command("ffmpeg", args...)
	ffmpeg.Stdout = dst
	ffmpeg.Stderr = os.Stderr
	return ffmpeg.Run()
}

func probe(path, entries string) ([]string, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", entries, "-of", "csv=p=0", path).Output()
	return strings.Fields(string(out)), err
}

func probeDuration(path string) (time.Duration, error) {
	video, err := streamSeconds(path, "v:0")
	if err != nil {
		return 0, err
	}
	audio, err := streamSeconds(path, "a:0")
	if err != nil {
		return 0, err
	}
	return time.Duration(min(video, audio) * float64(time.Second)), nil
}

func streamSeconds(path, stream string) (float64, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", stream, "-show_entries", "stream=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, err
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("could not read %s duration", stream)
	}
	return seconds, nil
}

func checkVideo(path string) error {
	format, err := probe(path, "format=format_name")
	if err != nil || len(format) == 0 {
		return errors.New("not a readable video file")
	}
	formats := strings.Trim(format[0], `"`)
	if !slices.Contains(strings.Split(formats, ","), "mp4") {
		return fmt.Errorf("needs to be an MP4 file, but it's %s", formats)
	}
	codecs, err := probe(path, "stream=codec_name")
	if err != nil {
		return errors.New("not a readable video file")
	}
	if !slices.Contains(codecs, "h264") || !slices.Contains(codecs, "aac") {
		return fmt.Errorf("needs H.264 video and AAC audio, but it has %s", strings.Join(codecs, ", "))
	}
	if _, err := probeDuration(path); err != nil {
		return fmt.Errorf("its length can't be read: %v", err)
	}
	// Reading every packet without decoding is quick, and catches files
	// that are cut short, which would otherwise freeze the stream.
	read := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-xerror", "-i", path,
		"-map", "0:v:0", "-map", "0:a:0", "-c", "copy", "-f", "null", "-")
	if err := read.Run(); err != nil {
		return errors.New("the file is damaged or incomplete")
	}
	return nil
}
