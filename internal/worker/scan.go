package worker

import (
	"errors"
	"io"
	"os"
	"sync"
)

type directoryReader interface {
	ReadDir(int) ([]os.DirEntry, error)
	Close() error
}

// Keep the directory cursor between cycles instead of reopening and rereading an
// ever-growing prefix. Each cycle reads at most 200 entries, regardless of size.
type directoryScan struct {
	mu        sync.Mutex
	directory directoryReader
}

func (s *directoryScan) next(root *os.Root) ([]os.DirEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.directory == nil {
		directory, err := root.Open(".")
		if err != nil {
			return nil, err
		}
		s.directory = directory
	}
	entries, err := s.directory.ReadDir(200)
	if err != nil {
		closeErr := s.closeDirectory()
		if errors.Is(err, io.EOF) {
			err = closeErr
		} else {
			err = errors.Join(err, closeErr)
		}
	}
	return entries, err
}

func (s *directoryScan) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeDirectory()
}

func (s *directoryScan) closeDirectory() error {
	if s.directory == nil {
		return nil
	}
	directory := s.directory
	s.directory = nil
	return directory.Close()
}
