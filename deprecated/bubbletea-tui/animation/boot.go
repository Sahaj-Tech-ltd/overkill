package animation

import (
	"fmt"
	"os"
	"time"
)

func PlayBoot() {
	sequence := []struct {
		spinner  Spinner
		duration time.Duration
		label    string
	}{
		{Breathe, 600 * time.Millisecond, "Waking up..."},
		{Columns, 500 * time.Millisecond, "Loading config..."},
		{Helix, 500 * time.Millisecond, "Connecting synapses..."},
		{FillSweep, 400 * time.Millisecond, "Ready"},
	}

	hideCursor()
	defer showCursor()

	for _, step := range sequence {
		done := make(chan struct{})
		go func(s Spinner, dur time.Duration, label string) {
			ticker := time.NewTicker(s.Interval)
			defer ticker.Stop()
			start := time.Now()
			var frame int
			for {
				select {
				case <-ticker.C:
					if time.Since(start) > dur {
						close(done)
						return
					}
					fmt.Fprintf(os.Stderr, "\r\033[K  %s %s", s.Frames[frame%len(s.Frames)], label)
					frame++
				}
			}
		}(step.spinner, step.duration, step.label)
		<-done
	}

	fmt.Fprint(os.Stderr, "\r\033[K")
}

func PlayThinking() {
	s := BrailleWave
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	start := time.Now()
	duration := 300 * time.Millisecond

	var frame int
	for {
		select {
		case <-ticker.C:
			if time.Since(start) > duration {
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			}
			fmt.Fprintf(os.Stderr, "\r\033[K  %s Thinking...", s.Frames[frame%len(s.Frames)])
			frame++
		}
	}
}

func PlayDone() {
	s := Checkerboard
	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()
	done := make(chan struct{})
	go func() {
		time.Sleep(800 * time.Millisecond)
		close(done)
	}()

	var frame int
	for {
		select {
		case <-ticker.C:
			fmt.Fprintf(os.Stderr, "\r\033[K  %s ✓ Done\n", s.Frames[frame%len(s.Frames)])
			frame++
		case <-done:
			fmt.Fprint(os.Stderr, "\r\033[K  ✓ Done\n")
			return
		}
	}
}

func hideCursor() {
	fmt.Fprint(os.Stderr, "\033[?25l")
}

func showCursor() {
	fmt.Fprint(os.Stderr, "\033[?25h")
}
