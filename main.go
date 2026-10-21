package main

import (
	"embed"
	"errors"
	"flag"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
)

//go:embed web
var web embed.FS

func main() {
	output := flag.String("rtmp", "", "RTMP URL to stream to")
	dir := flag.String("media", "media", "directory to store videos in")
	addr := flag.String("addr", ":8080", "address for the web interface")
	encoder := flag.String("encoder", "libx264", "ffmpeg H.264 encoder, e.g. h264_v4l2m2m for the Raspberry Pi's hardware encoder")
	title := flag.String("title", "Continuity", "title shown on the web interface")
	flag.Parse()

	if *output == "" {
		log.Fatal("-rtmp is required")
	}
	if err := os.MkdirAll(*dir, 0755); err != nil {
		log.Fatal(err)
	}
	playlist, err := loadPlaylist(*dir)
	if err != nil {
		log.Fatal(err)
	}

	previewDir, err := os.MkdirTemp("", "continuity-preview")
	if err != nil {
		log.Fatal(err)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		os.RemoveAll(previewDir)
		os.Exit(0)
	}()

	go runPlayout(*output, *encoder, previewDir, playlist)

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	preview := router.Group("/preview", func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
	})
	preview.Static("/", previewDir)

	router.GET("/api/playlist", func(c *gin.Context) {
		names, playing := playlist.Snapshot()
		c.JSON(http.StatusOK, gin.H{"videos": names, "playing": playing})
	})

	router.PUT("/api/playlist", func(c *gin.Context) {
		var names []string
		if err := c.BindJSON(&names); err != nil {
			return
		}
		if err := playlist.Reorder(names); err != nil {
			c.String(http.StatusConflict, err.Error())
			return
		}
		c.Status(http.StatusOK)
	})

	router.POST("/api/videos/:name", func(c *gin.Context) {
		name := c.Param("name")
		if strings.ToLower(filepath.Ext(name)) != ".mp4" {
			c.String(http.StatusBadRequest, "only .mp4 files are accepted")
			return
		}
		if _, err := os.Stat(filepath.Join(*dir, name)); err == nil {
			c.String(http.StatusConflict, errExists.Error())
			return
		}
		if c.Request.ContentLength > freeSpace(*dir)-minFreeSpace {
			c.String(http.StatusInsufficientStorage, "there isn't enough disk space for this video")
			return
		}
		temp, err := saveUpload(c.Request.Body, *dir)
		if err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		defer os.Remove(temp)
		if err := playlist.Add(temp, name); errors.Is(err, errExists) {
			c.String(http.StatusConflict, err.Error())
			return
		} else if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusOK)
	})

	router.DELETE("/api/videos/:name", func(c *gin.Context) {
		name := c.Param("name")
		if err := playlist.Remove(name); errors.Is(err, errNotFound) {
			c.String(http.StatusNotFound, err.Error())
			return
		} else if err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		if err := os.Remove(filepath.Join(*dir, name)); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusOK)
	})

	page := template.Must(template.ParseFS(web, "web/index.html"))
	router.GET("/", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		page.Execute(c.Writer, *title)
	})

	files, _ := fs.Sub(web, "web")
	router.NoRoute(gin.WrapH(http.FileServer(http.FS(files))))

	log.Printf("web interface on %s", *addr)
	log.Fatal(router.Run(*addr))
}

const minFreeSpace = 1 << 30

func freeSpace(dir string) int64 {
	var stat syscall.Statfs_t
	syscall.Statfs(dir, &stat)
	return int64(stat.Bavail) * int64(stat.Bsize)
}

func saveUpload(body io.Reader, dir string) (string, error) {
	file, err := os.CreateTemp(dir, "*.upload")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(file, body)
	file.Close()
	if err == nil {
		err = checkVideo(file.Name())
	}
	if err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return file.Name(), nil
}
