package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

var (
	errExists   = errors.New("a video with that name already exists")
	errNotFound = errors.New("video not found")
)

type Playlist struct {
	mu      sync.Mutex
	dir     string
	names   []string
	playing string
	cursor  string
}

func loadPlaylist(dir string) (*Playlist, error) {
	p := &Playlist{dir: dir}
	data, err := os.ReadFile(p.file())
	if errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &p.names); err != nil {
		// e.g. cut short by a power cut before saves were atomic
		log.Printf("%s is damaged (%v), rebuilding it from the videos in %s", p.file(), err, dir)
		if p.names, err = videosIn(dir); err != nil {
			return nil, err
		}
		return p, p.save(p.names)
	}
	p.names = unique(p.names)
	return p, nil
}

func videosIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.ToLower(filepath.Ext(entry.Name())) == ".mp4" {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}

// unique removes repeated names, which would stop Next from reaching the
// videos after the second copy.
func unique(names []string) []string {
	var out []string
	for _, name := range names {
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func (p *Playlist) file() string {
	return filepath.Join(p.dir, "playlist.json")
}

// save writes names to a temporary file and renames it into place, so the
// playlist file is never left half written.
func (p *Playlist) save(names []string) error {
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(p.dir, "playlist-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, err = temp.Write(data)
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(temp.Name(), 0644)
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), p.file())
}

func (p *Playlist) Snapshot() ([]string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.names), p.playing
}

// Add moves the uploaded file at temp into place as name and appends it to the
// playlist. Doing both under the lock stops two uploads of the same name from
// overwriting each other.
func (p *Playlist) Add(temp, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	path := filepath.Join(p.dir, name)
	if slices.Contains(p.names, name) {
		return errExists
	}
	if _, err := os.Stat(path); err == nil {
		return errExists
	}
	if err := os.Rename(temp, path); err != nil {
		return err
	}
	names := append(slices.Clone(p.names), name)
	if err := p.save(names); err != nil {
		os.Remove(path)
		return err
	}
	p.names = names
	return nil
}

func (p *Playlist) Reorder(names []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !sameNames(names, p.names) {
		return errors.New("playlist has changed, reload and try again")
	}
	if err := p.save(names); err != nil {
		return err
	}
	p.names = names
	return nil
}

func (p *Playlist) Remove(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.Index(p.names, name)
	if i < 0 {
		return errNotFound
	}
	names := slices.Delete(slices.Clone(p.names), i, i+1)
	if err := p.save(names); err != nil {
		return err
	}
	if p.cursor == name {
		p.cursor = ""
		if i > 0 {
			p.cursor = p.names[i-1]
		}
	}
	p.names = names
	return nil
}

func (p *Playlist) Next() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.names) == 0 {
		return ""
	}
	i := slices.Index(p.names, p.cursor)
	p.cursor = p.names[(i+1)%len(p.names)]
	return filepath.Join(p.dir, p.cursor)
}

func (p *Playlist) SetPlaying(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.playing = name
}

func sameNames(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}
