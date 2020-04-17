package client

import (
	"crypto/md5"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Taken from https://blog.golang.org/pipelines/parallel.go @ 2020-04-07T13:03:47+00:00

// A result is the product of reading and summing a file using MD5.
type result struct {
	path string
	sum  [md5.Size]byte
	err  error
}

// sumFiles starts goroutines to walk the directory tree at root and digest each
// regular file.  These goroutines send the results of the digests on the result
// channel and send the result of the walk on the error channel.  If done is
// closed, sumFiles abandons its work.
func sumFiles(done <-chan struct{}, root string) (<-chan result, <-chan error) {
	// For each regular file, start a goroutine that sums the file and sends
	// the result on c.  Send the result of the walk on errc.
	c := make(chan result)
	errc := make(chan error, 1)
	go func() { // HL
		var wg sync.WaitGroup
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			wg.Add(1)
			go func() { // HL
				data, err := ioutil.ReadFile(path)
				select {
				case c <- result{path, md5.Sum(data), err}: // HL
				case <-done: // HL
				}
				wg.Done()
			}()
			// Abort the walk if done is closed.
			select {
			case <-done: // HL
				return errors.New("walk canceled")
			default:
				return nil
			}
		})
		// Walk has returned, so all calls to wg.Add are done.  Start a
		// goroutine to close c once all the sends are done.
		go func() { // HL
			wg.Wait()
			close(c) // HL
		}()
		// No select needed here, since errc is buffered.
		errc <- err // HL
	}()
	return c, errc
}

// MD5All reads all the files in the file tree rooted at root and returns a map
// from file path to the MD5 sum of the file's contents.  If the directory walk
// fails or any read operation fails, MD5All returns an error.  In that case,
// MD5All does not wait for inflight read operations to complete.
func MD5All(root string) (map[string][md5.Size]byte, error) {
	// MD5All closes the done channel when it returns; it may do so before
	// receiving all the values from c and errc.
	done := make(chan struct{}) // HLdone
	defer close(done)           // HLdone

	c, errc := sumFiles(done, root) // HLdone

	m := make(map[string][md5.Size]byte)
	for r := range c { // HLrange
		if r.err != nil {
			return nil, r.err
		}
		m[r.path] = r.sum
	}
	if err := <-errc; err != nil {
		return nil, err
	}
	return m, nil
}

// GetDirectoryHash returns a MD5 sum of a directory, calculated as the hash
// of all contained files' hashes.  If walking the directory or any read
// operation fails, GetDirectoryHash returns an error.  In that case,
// GetDirectoryHash does not wait for inflight read operations to complete.
func GetDirectoryHash(dirPath string) (string, error) {
	m, err := MD5All(dirPath)
	if err != nil {
		return "", err
	}

	h := md5.New()

	var paths []string
	for path := range m {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		fmt.Fprintf(h, "%x", m[path])
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// GetFileHash returns a MD5 sum of a file, calculated using the file's content
func GetFileHash(filePath string) (string, error) {
	data, err := ioutil.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", md5.Sum(data)), nil
}

func GetTemplateHash(paths ...string) (string, error) {
	h := md5.New()

	for _, p := range paths {
		f, err := os.Stat(p)
		if err != nil {
			return "", err
		}

		var hash string
		switch m := f.Mode(); {
		case m.IsDir():
			hash, err = GetDirectoryHash(p)
		case m.IsRegular():
			hash, err = GetFileHash(p)
		default:
			return "", errors.New("invalid file mode for path, cannot generate hash")
		}

		if err != nil {
			return "", err
		}

		fmt.Fprintf(h, "%s", hash)
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
