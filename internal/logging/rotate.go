package logging

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// DefaultMaxBackups is the number of rotated files kept when the
// configuration sets none.
const DefaultMaxBackups = 5

// rotatingFile appends to a file. Once the file would grow past max bytes
// it is renamed to <path>.1, older generations shift to .2, .3, ... and the
// generation beyond backups is deleted.
type rotatingFile struct {
	mu      sync.Mutex
	path    string
	max     int64
	backups int
	perm    fs.FileMode
	f       *os.File
	size    int64
	report  io.Writer
}

// OpenRotating opens path for appending, as the log file is opened: once
// the file would grow past maxBytes (0: never), it is renamed to <path>.1,
// older generations shift, and backups of them are kept. report, when not
// nil, receives a line for each rotation that fails. New files get the
// permissions perm, and an existing file with more permissions than perm
// loses the extra ones.
func OpenRotating(path string, maxBytes int64, backups int, perm fs.FileMode, report io.Writer) (io.WriteCloser, error) {
	return openRotating(path, maxBytes, backups, perm, report)
}

func openRotating(path string, max int64, backups int, perm fs.FileMode, report io.Writer) (*rotatingFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if backups <= 0 {
		backups = DefaultMaxBackups
	}
	r := &rotatingFile{path: path, max: max, backups: backups, perm: perm, report: report}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, r.perm)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if extra := st.Mode().Perm() &^ r.perm; extra != 0 && runtime.GOOS != "windows" {
		if err := f.Chmod(st.Mode().Perm() &^ extra); err != nil {
			f.Close()
			return err
		}
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.max > 0 && r.size > 0 && r.size+int64(len(p)) > r.max {
		if err := r.rotate(); err != nil {
			r.reportFailure(err)
		}
	}
	if r.f == nil {
		return 0, errors.New("log file is not open")
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) rotate() error {
	if err := r.f.Close(); err != nil {
		r.f = nil
		return errors.Join(err, r.open())
	}
	r.f = nil
	var errs []error
	gen := func(i int) string { return fmt.Sprintf("%s.%d", r.path, i) }
	if err := os.Remove(gen(r.backups)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	for i := r.backups - 1; i >= 1; i-- {
		if err := os.Rename(gen(i), gen(i+1)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := os.Rename(r.path, gen(1)); err != nil {
		errs = append(errs, err)
	}
	// Reopen even when a rename failed, so that logging continues in the
	// current file.
	if err := r.open(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// reportFailure writes a WARN line in the text format to the report
// writer, because the log file itself may be the thing that failed.
func (r *rotatingFile) reportFailure(err error) {
	if r.report == nil {
		return
	}
	var b bytes.Buffer
	b.WriteString(time.Now().UTC().Format(tsLayout))
	b.WriteString("\t-\tWARN\tLog rotation failed\t{\"file\":")
	writeJSON(&b, r.path)
	b.WriteString(",\"errorType\":")
	writeJSON(&b, ErrorType(err))
	b.WriteString(",\"errorMessage\":")
	writeJSON(&b, err.Error())
	b.WriteString("}\n")
	_, _ = r.report.Write(b.Bytes())
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
