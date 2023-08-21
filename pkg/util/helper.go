package util

import (
	"fmt"
	"os"
	"path/filepath"
)

func IsDirectory(path string) bool {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false
	}

	return fileInfo.IsDir()
}

func GetFileNames(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			fmt.Printf("prevent panic by handling failure accessing a path %q: %v\n", path, err)

			return err
		}

		if !info.IsDir() {
			files = append(files, path)
		}

		return nil
	})

	return files, fmt.Errorf("%w", err)
}
