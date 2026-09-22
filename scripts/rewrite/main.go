// Command rewrite replaces every occurrence of a literal string in the files
// listed on stdin, separated by NUL as `git grep -lz` prints them.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: git grep -lzF OLD | rewrite OLD NEW") //nolint:errcheck // nothing left to do if stderr fails
		os.Exit(2)
	}

	if err := run(os.Stdin, os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "rewrite:", err) //nolint:errcheck // nothing left to do if stderr fails
		os.Exit(1)
	}
}

func run(r io.Reader, from, to string) error {
	if from == "" {
		return errors.New("OLD must not be empty")
	}

	list, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read file list: %w", err)
	}

	for path := range strings.SplitSeq(string(list), "\x00") {
		if path == "" {
			continue
		}

		if err := rewriteFile(path, from, to); err != nil {
			return err
		}
	}

	return nil
}

func rewriteFile(path, from, to string) error {
	path = filepath.Clean(path)

	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	replaced := bytes.ReplaceAll(data, []byte(from), []byte(to))
	if bytes.Equal(replaced, data) {
		return nil
	}

	if err := os.WriteFile(path, replaced, info.Mode().Perm()); err != nil { //nolint:gosec // the paths come from git grep by design
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
