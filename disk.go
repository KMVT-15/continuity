package main

import "syscall"

// minFreeSpace is kept free on the media disk so uploads can't starve the
// playlist file and the preview of space.
const minFreeSpace = 1 << 30

// uploadSpace returns how many bytes an upload to dir may use.
func uploadSpace(dir string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(dir, &stat); err != nil {
		return 0, err
	}
	return max(int64(stat.Bavail)*int64(stat.Bsize)-minFreeSpace, 0), nil
}
