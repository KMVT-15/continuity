package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
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
	return p, json.Unmarshal(data, &p.names)
}

func (p *Playlist) file() string {
	return filepath.Join(p.dir, "playlist.json")
}

func (p *Playlist) save(names []string) error {
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	temp := p.file() + ".tmp"
	if err := os.WriteFile(temp, data, 0644); err != nil {
		return err
	}
	return os.Rename(temp, p.file())
}

func (p *Playlist) Snapshot() ([]string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.names), p.playing
}

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
