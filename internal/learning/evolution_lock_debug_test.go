package learning

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

func TestEvolutionLockConcurrentCreateHandlesRootRace(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			root, err := os.OpenRoot(dir)
			if err != nil {
				errs <- err
				return
			}
			defer root.Close()
			f, err := openLearningLock(root, ".store.lock")
			if err == nil {
				f.Close()
			} else {
				_, staterr := os.Stat(dir + "/.store.lock")
				errs <- fmt.Errorf("%w; pathStat=%v", err, staterr)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
