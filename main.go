package main

import (
	"embed"
	"flag"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed web
var web embed.FS

func main() {
	output := flag.String("rtmp", "", "RTMP URL to stream to")
	dir := flag.String("media", "media", "directory to store videos in")
	addr := flag.String("addr", ":8080", "address for the web interface")
	encoder := flag.String("encoder", "libx264", "ffmpeg H.264 encoder, e.g. h264_v4l2m2m for the Raspberry Pi's hardware encoder")
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
		path := filepath.Join(*dir, name)
		if _, err := os.Stat(path); err == nil {
			c.String(http.StatusConflict, "a video with that name already exists")
			return
		}
		if err := saveUpload(c.Request.Body, path); err != nil {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		if err := playlist.Add(name); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusOK)
	})

	router.DELETE("/api/videos/:name", func(c *gin.Context) {
		name := c.Param("name")
		if err := playlist.Remove(name); err != nil {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		if err := os.Remove(filepath.Join(*dir, name)); err != nil {
			c.String(http.StatusInternalServerError, err.Error())
			return
		}
		c.Status(http.StatusOK)
	})

	files, _ := fs.Sub(web, "web")
	router.NoRoute(gin.WrapH(http.FileServer(http.FS(files))))

	log.Printf("web interface on %s", *addr)
	log.Fatal(router.Run(*addr))
}

func saveUpload(body io.Reader, path string) error {
	temp := path + ".upload"
	defer os.Remove(temp)

	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, body)
	file.Close()
	if err != nil {
		return err
	}
	if err := checkVideo(temp); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
