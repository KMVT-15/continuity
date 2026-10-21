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

const clipGap = 100 * time.Millisecond

const outputFPS = 30

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

	stdin, err := ffmpeg.StdinPipe()
	if err != nil {
		return err
	}
	progress, err := ffmpeg.StdoutPipe()
	if err != nil {
		return err
	}
	if err := ffmpeg.Start(); err != nil {
		return err
	}
	cues := make(chan cue, 100)
	go followProgress(progress, cues, playlist)
	exited := make(chan error, 1)
	go func() { exited <- ffmpeg.Wait() }()
	defer stdin.Close()

	var offset time.Duration
	for {
		select {
		case err := <-exited:
			return err
		default:
		}

		path := playlist.Next()
		if path == "" {
			playlist.SetPlaying("")
			time.Sleep(5 * time.Second)
			continue
		}

		log.Printf("queueing %s", path)
		cues <- cue{start: offset, name: filepath.Base(path)}
		offset, err = remux(path, stdin, offset)
		if err != nil {
			log.Printf("%s: %v", path, err)
			time.Sleep(time.Second)
		}
	}
}

type cue struct {
	start time.Duration
	name  string
}

func followProgress(progress io.Reader, cues <-chan cue, playlist *Playlist) {
	var pending []cue
	scanner := bufio.NewScanner(progress)
	for scanner.Scan() {
		value, ok := strings.CutPrefix(scanner.Text(), "frame=")
		if !ok {
			continue
		}
		frames, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			continue
		}
		for len(cues) > 0 {
			pending = append(pending, <-cues)
		}
		for len(pending) > 0 && pending[0].start <= time.Duration(frames)*time.Second/outputFPS {
			log.Printf("on air: %s", pending[0].name)
			playlist.SetPlaying(pending[0].name)
			pending = pending[1:]
		}
	}
}

func remux(path string, dst io.Writer, offset time.Duration) (time.Duration, error) {
	duration, err := probeDuration(path)
	if err != nil {
		return offset, err
	}

	ffmpeg := exec.Command("ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-i", path,
		"-map", "0:v:0", "-map", "0:a:0",
		"-c", "copy",
		"-output_ts_offset", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64),
		"-f", "mpegts", "pipe:1",
	)
	ffmpeg.Stdout = dst
	ffmpeg.Stderr = os.Stderr
	return offset + duration + clipGap, ffmpeg.Run()
}

func probe(path, entries string) ([]string, error) {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", entries, "-of", "csv=p=0", path).Output()
	return strings.Fields(string(out)), err
}

func probeDuration(path string) (time.Duration, error) {
	fields, err := probe(path, "format=duration")
	if err != nil || len(fields) == 0 {
		return 0, errors.New("could not read duration")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	return time.Duration(seconds * float64(time.Second)), err
}

func checkVideo(path string) error {
	codecs, err := probe(path, "stream=codec_name")
	if err != nil {
		return errors.New("not a readable video file")
	}
	if !slices.Contains(codecs, "h264") || !slices.Contains(codecs, "aac") {
		return fmt.Errorf("needs H.264 video and AAC audio, but it has %s", strings.Join(codecs, ", "))
	}
	return nil
}
