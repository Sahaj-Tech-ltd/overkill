package animation

import "time"

type Spinner struct {
	Name     string
	Frames   []string
	Interval time.Duration
}

var All = []Spinner{
	Braille,
	BrailleWave,
	DNA,
	Scan,
	Rain,
	ScanLine,
	Pulse,
	Snake,
	Sparkle,
	Cascade,
	Columns,
	Orbit,
	Breathe,
	WaveRows,
	Checkerboard,
	Helix,
	FillSweep,
	DiagSwipe,
}

var ByName = func() map[string]Spinner {
	m := make(map[string]Spinner, len(All))
	for _, s := range All {
		m[s.Name] = s
	}
	return m
}()

var Braille = Spinner{
	Name:     "braille",
	Frames:   []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	Interval: 80 * time.Millisecond,
}

var BrailleWave = Spinner{
	Name: "braillewave",
	Frames: []string{
		"⠁⠂⠄⡀", "⠂⠄⡀⢀", "⠄⡀⢀⠠", "⡀⢀⠠⠐",
		"⢀⠠⠐⠈", "⠠⠐⠈⠁", "⠐⠈⠁⠂", "⠈⠁⠂⠄",
	},
	Interval: 100 * time.Millisecond,
}

var DNA = Spinner{
	Name: "dna",
	Frames: []string{
		"⠋⠉⠙⠚", "⠉⠙⠚⠒", "⠙⠚⠒⠂", "⠚⠒⠂⠂",
		"⠒⠂⠂⠒", "⠂⠂⠒⠲", "⠂⠒⠲⠴", "⠒⠲⠴⠤",
		"⠲⠴⠤⠄", "⠴⠤⠄⠋", "⠤⠄⠋⠉", "⠄⠋⠉⠙",
	},
	Interval: 80 * time.Millisecond,
}

var Scan = Spinner{
	Name: "scan",
	Frames: []string{
		"⠀⠀⠀⠀", "⡇⠀⠀⠀", "⣿⠀⠀⠀", "⢸⡇⠀⠀",
		"⠀⣿⠀⠀", "⠀⢸⡇⠀", "⠀⠀⣿⠀", "⠀⠀⢸⡇",
		"⠀⠀⠀⣿", "⠀⠀⠀⢸",
	},
	Interval: 70 * time.Millisecond,
}

var Rain = Spinner{
	Name: "rain",
	Frames: []string{
		"⢁⠂⠔⠈", "⠂⠌⡠⠐", "⠄⡐⢀⠡", "⡈⠠⠀⢂",
		"⠐⢀⠁⠄", "⠠⠁⠊⡀", "⢁⠂⠔⠈", "⠂⠌⡠⠐",
		"⠄⡐⢀⠡", "⡈⠠⠀⢂", "⠐⢀⠁⠄", "⠠⠁⠊⡀",
	},
	Interval: 100 * time.Millisecond,
}

var ScanLine = Spinner{
	Name: "scanline",
	Frames: []string{
		"⠉⠉⠉", "⠓⠓⠓", "⠦⠦⠦", "⣄⣄⣄", "⠦⠦⠦", "⠓⠓⠓",
	},
	Interval: 120 * time.Millisecond,
}

var Pulse = Spinner{
	Name: "pulse",
	Frames: []string{
		"⠀⠶⠀", "⠰⣿⠆", "⢾⣉⡷", "⣏⠀⣹", "⡁⠀⢈",
	},
	Interval: 180 * time.Millisecond,
}

var Snake = Spinner{
	Name: "snake",
	Frames: []string{
		"⣁⡀", "⣉⠀", "⡉⠁", "⠉⠉",
		"⠈⠙", "⠀⠛", "⠐⠚", "⠒⠒",
		"⠖⠂", "⠶⠀", "⠦⠄", "⠤⠤",
		"⠠⢤", "⠀⣤", "⢀⣠", "⣀⣀",
	},
	Interval: 80 * time.Millisecond,
}

var Sparkle = Spinner{
	Name: "sparkle",
	Frames: []string{
		"⡡⠊⢔⠡", "⠊⡰⡡⡘", "⢔⢅⠈⢢", "⡁⢂⠆⡍", "⢔⠨⢑⢐", "⠨⡑⡠⠊",
	},
	Interval: 150 * time.Millisecond,
}

var Cascade = Spinner{
	Name: "cascade",
	Frames: []string{
		"⠀⠀⠀⠀", "⠀⠀⠀⠀", "⠁⠀⠀⠀", "⠋⠀⠀⠀",
		"⠞⠁⠀⠀", "⡴⠋⠀⠀", "⣠⠞⠁⠀", "⢀⡴⠋⠀",
		"⠀⣠⠞⠁", "⠀⢀⡴⠋", "⠀⠀⣠⠞", "⠀⠀⢀⡴",
		"⠀⠀⠀⣠", "⠀⠀⠀⢀",
	},
	Interval: 60 * time.Millisecond,
}

var Columns = Spinner{
	Name: "columns",
	Frames: []string{
		"⡀⠀⠀", "⡄⠀⠀", "⡆⠀⠀", "⡇⠀⠀",
		"⣇⠀⠀", "⣧⠀⠀", "⣷⠀⠀", "⣿⠀⠀",
		"⣿⡀⠀", "⣿⡄⠀", "⣿⡆⠀", "⣿⡇⠀",
		"⣿⣇⠀", "⣿⣧⠀", "⣿⣷⠀", "⣿⣿⠀",
		"⣿⣿⡀", "⣿⣿⡄", "⣿⣿⡆", "⣿⣿⡇",
		"⣿⣿⣇", "⣿⣿⣧", "⣿⣿⣷", "⣿⣿⣿",
		"⣿⣿⣿", "⠀⠀⠀",
	},
	Interval: 60 * time.Millisecond,
}

var Orbit = Spinner{
	Name: "orbit",
	Frames: []string{
		"⠃", "⠉", "⠘", "⠰", "⢠", "⣀", "⡄", "⠆",
	},
	Interval: 100 * time.Millisecond,
}

var Breathe = Spinner{
	Name: "breathe",
	Frames: []string{
		"⠀", "⠂", "⠌", "⡑", "⢕", "⢝", "⣫", "⣟",
		"⣿", "⣟", "⣫", "⢝", "⢕", "⡑", "⠌", "⠂", "⠀",
	},
	Interval: 100 * time.Millisecond,
}

var WaveRows = Spinner{
	Name: "waverows",
	Frames: []string{
		"⠖⠉⠉⠑", "⡠⠖⠉⠉", "⣠⡠⠖⠉", "⣄⣠⡠⠖",
		"⠢⣄⣠⡠", "⠙⠢⣄⣠", "⠉⠙⠢⣄", "⠊⠉⠙⠢",
		"⠜⠊⠉⠙", "⡤⠜⠊⠉", "⣀⡤⠜⠊", "⢤⣀⡤⠜",
		"⠣⢤⣀⡤", "⠑⠣⢤⣀", "⠉⠑⠣⢤", "⠋⠉⠑⠣",
	},
	Interval: 90 * time.Millisecond,
}

var Checkerboard = Spinner{
	Name: "checkerboard",
	Frames: []string{
		"⢕⢕⢕", "⡪⡪⡪", "⢊⠔⡡", "⡡⢊⠔",
	},
	Interval: 250 * time.Millisecond,
}

var Helix = Spinner{
	Name: "helix",
	Frames: []string{
		"⢌⣉⢎⣉", "⣉⡱⣉⡱", "⣉⢎⣉⢎", "⡱⣉⡱⣉",
		"⢎⣉⢎⣉", "⣉⡱⣉⡱", "⣉⢎⣉⢎", "⡱⣉⡱⣉",
		"⢎⣉⢎⣉", "⣉⡱⣉⡱", "⣉⢎⣉⢎", "⡱⣉⡱⣉",
		"⢎⣉⢎⣉", "⣉⡱⣉⡱", "⣉⢎⣉⢎", "⡱⣉⡱⣉",
	},
	Interval: 80 * time.Millisecond,
}

var FillSweep = Spinner{
	Name: "fillsweep",
	Frames: []string{
		"⣀⣀", "⣤⣤", "⣶⣶", "⣿⣿", "⣿⣿", "⣿⣿",
		"⣶⣶", "⣤⣤", "⣀⣀", "⠀⠀", "⠀⠀",
	},
	Interval: 100 * time.Millisecond,
}

var DiagSwipe = Spinner{
	Name: "diagswipe",
	Frames: []string{
		"⠁⠀", "⠋⠀", "⠟⠁", "⡿⠋", "⣿⠟", "⣿⡿",
		"⣿⣿", "⣿⣿", "⣾⣿", "⣴⣿", "⣠⣾", "⢀⣴",
		"⠀⣠", "⠀⢀", "⠀⠀", "⠀⠀",
	},
	Interval: 60 * time.Millisecond,
}
