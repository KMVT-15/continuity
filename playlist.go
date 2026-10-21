package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
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

func (p *Playlist) save() error {
	data, err := json.MarshalIndent(p.names, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.file(), data, 0644)
}

func (p *Playlist) Snapshot() ([]string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.names), p.playing
}

func (p *Playlist) Add(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.names = append(p.names, name)
	return p.save()
}

func (p *Playlist) Reorder(names []string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !sameNames(names, p.names) {
		return errors.New("playlist has changed, reload and try again")
	}
	p.names = names
	return p.save()
}

func (p *Playlist) Remove(name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	i := slices.Index(p.names, name)
	if i < 0 {
		return errors.New("video not found")
	}
	if p.cursor == name {
		p.cursor = ""
		if i > 0 {
			p.cursor = p.names[i-1]
		}
	}
	p.names = slices.Delete(p.names, i, i+1)
	return p.save()
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
