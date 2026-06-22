// Package catfile (extension): pool of cat-file --batch workers for
// parallel object reads. Concurrent callers grab a worker from the
// pool and release it when done. zlib decompression is the bottleneck
// and is single-threaded inside each cat-file process; a pool of N
// workers can use N cores in parallel.
package catfile

import "sync"

type Pool struct {
	workers chan *Reader
	once    sync.Once
}

func NewPool(repoPath string, n int) (*Pool, error) {
	p := &Pool{workers: make(chan *Reader, n)}
	for i := 0; i < n; i++ {
		r, err := New(repoPath)
		if err != nil {
			// Best-effort cleanup of any already started.
			close(p.workers)
			for w := range p.workers {
				w.Close()
			}
			return nil, err
		}
		p.workers <- r
	}
	return p, nil
}

func (p *Pool) Object(sha string) ([]byte, error) {
	w := <-p.workers
	defer func() { p.workers <- w }()
	return w.Object(sha)
}

func (p *Pool) Close() error {
	p.once.Do(func() {
		close(p.workers)
		for w := range p.workers {
			w.Close()
		}
	})
	return nil
}
